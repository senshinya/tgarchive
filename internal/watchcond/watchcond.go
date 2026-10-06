// Package watchcond parses, validates and evaluates a channel watch's condition tree against a
// post's counters (reactions, views, forwards, replies) and content.
package watchcond

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

const (
	MaxDepth  = 3  // nested groups, the root included
	MaxLeaves = 30 // conditions in the whole tree
	MaxText   = 100

	KeyPaid         = "paid"
	CustomKeyPrefix = "custom:"
)

// Node is a group (Op + Items) or a condition (Metric + its fields).
type Node struct {
	Op     string          `json:"op,omitempty"` // and / or
	Items  []Node          `json:"items,omitempty"`
	Metric string          `json:"metric,omitempty"`
	Key    string          `json:"key,omitempty"` // reaction
	Num    string          `json:"num,omitempty"` // ratio: "total" or a reaction key
	Den    string          `json:"den,omitempty"` // ratio: "total" / "views"
	Cmp    string          `json:"cmp,omitempty"`
	Value  json.RawMessage `json:"value,omitempty"`

	num  float64 // parsed numeric value
	str  string  // parsed string value (lower-cased keyword)
	orig string  // the keyword as typed, for explanations
}

// Stats is what a post (or an album, metrics taken at their maximum) is judged on.
type Stats struct {
	Reactions map[string]int // emoji text, "custom:<document id>" or "paid"
	Total     int            // sum of all reactions
	Views     int
	Forwards  int
	Replies   int
	Kinds     map[string]bool // photo / video / file / text
	Text      string
}

var customKey = regexp.MustCompile(`^custom:[1-9][0-9]{0,19}$`)

// Parse decodes and validates a condition tree; errors are user-facing (Chinese).
func Parse(data []byte) (*Node, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var n Node
	if err := dec.Decode(&n); err != nil {
		return nil, errors.New("条件格式错误")
	}
	if dec.More() {
		return nil, errors.New("条件格式错误")
	}
	if n.Op == "" {
		return nil, errors.New("最外层必须是条件组")
	}
	leaves := 0
	if err := n.check(1, &leaves); err != nil {
		return nil, err
	}
	return &n, nil
}

func (n *Node) check(depth int, leaves *int) error {
	if n.Op != "" {
		if n.Op != "and" && n.Op != "or" {
			return errors.New("条件组只能是「全部满足」或「任一满足」")
		}
		if n.Metric != "" || n.Key != "" || n.Num != "" || n.Den != "" || n.Cmp != "" || len(n.Value) > 0 {
			return errors.New("条件格式错误")
		}
		if depth > MaxDepth {
			return fmt.Errorf("条件组最多嵌套 %d 层", MaxDepth)
		}
		if len(n.Items) == 0 {
			return errors.New("条件组不能为空")
		}
		for i := range n.Items {
			if err := n.Items[i].check(depth+1, leaves); err != nil {
				return err
			}
		}
		return nil
	}
	if len(n.Items) > 0 {
		return errors.New("条件格式错误")
	}
	*leaves++
	if *leaves > MaxLeaves {
		return fmt.Errorf("条件最多 %d 个", MaxLeaves)
	}
	switch n.Metric {
	case "reaction":
		if err := checkKey(n.Key); err != nil {
			return err
		}
		return n.countValue()
	case "total", "views", "forwards", "replies":
		if n.Key != "" {
			return errors.New("条件格式错误")
		}
		return n.countValue()
	case "ratio":
		if n.Num != "total" {
			if err := checkKey(n.Num); err != nil {
				return err
			}
		}
		if n.Den != "total" && n.Den != "views" {
			return errors.New("比率的分母只能是 reaction 总数或浏览量")
		}
		if n.Num == "total" && n.Den == "total" {
			return errors.New("比率的分子和分母不能相同")
		}
		if err := n.cmpOf("gte", "lte"); err != nil {
			return err
		}
		v, err := number(n.Value)
		if err != nil || v < 0 || v > 100 {
			return errors.New("比率须为 0–100 之间的百分数")
		}
		n.num = v
		return nil
	case "type":
		if err := n.cmpOf("is", "not"); err != nil {
			return err
		}
		s, err := text(n.Value)
		if err != nil || (s != "photo" && s != "video" && s != "file" && s != "text") {
			return errors.New("内容类型只能是图片、视频、文件或纯文字")
		}
		n.str = s
		return nil
	case "text":
		if err := n.cmpOf("contains", "not_contains"); err != nil {
			return err
		}
		s, err := text(n.Value)
		s = strings.TrimSpace(s)
		if err != nil || s == "" || len([]rune(s)) > MaxText {
			return fmt.Errorf("关键词须为 1–%d 个字符", MaxText)
		}
		n.str, n.orig = strings.ToLower(s), s
		return nil
	}
	return errors.New("未知的条件类型")
}

func checkKey(k string) error {
	if k == "" || len(k) > 64 {
		return errors.New("请选择表情")
	}
	if strings.HasPrefix(k, CustomKeyPrefix) && !customKey.MatchString(k) {
		return errors.New("自定义表情格式错误")
	}
	return nil
}

func (n *Node) cmpOf(allowed ...string) error {
	for _, a := range allowed {
		if n.Cmp == a {
			return nil
		}
	}
	return errors.New("比较方式错误")
}

func (n *Node) countValue() error {
	if err := n.cmpOf("gte", "lte"); err != nil {
		return err
	}
	v, err := number(n.Value)
	if err != nil || v < 0 || v != math.Trunc(v) || v > 1e12 {
		return errors.New("数量须为非负整数")
	}
	n.num = v
	return nil
}

func number(raw json.RawMessage) (float64, error) {
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return 0, err
	}
	return f, nil
}

func text(raw json.RawMessage) (string, error) {
	var s string
	err := json.Unmarshal(raw, &s)
	return s, err
}

// Eval reports whether st satisfies the (validated) tree.
func (n *Node) Eval(st Stats) bool {
	switch n.Op {
	case "and":
		for i := range n.Items {
			if !n.Items[i].Eval(st) {
				return false
			}
		}
		return true
	case "or":
		for i := range n.Items {
			if n.Items[i].Eval(st) {
				return true
			}
		}
		return false
	}
	ok, _ := n.leaf(st)
	return ok
}

// Explain describes every satisfied condition, e.g. "🔥 23 ≥ 10".
func (n *Node) Explain(st Stats) []string {
	out := []string{}
	n.explain(st, &out)
	return out
}

func (n *Node) explain(st Stats, out *[]string) {
	if n.Op != "" {
		for i := range n.Items {
			n.Items[i].explain(st, out)
		}
		return
	}
	if ok, s := n.leaf(st); ok {
		*out = append(*out, s)
	}
}

var metricNames = map[string]string{"total": "reaction 总数", "views": "浏览", "forwards": "转发", "replies": "评论"}
var kindNames = map[string]string{"photo": "图片", "video": "视频", "file": "文件", "text": "纯文字"}

func (n *Node) leaf(st Stats) (bool, string) {
	sym := "≥"
	cmp := func(v float64) bool { return v >= n.num }
	if n.Cmp == "lte" {
		sym = "≤"
		cmp = func(v float64) bool { return v <= n.num }
	}
	switch n.Metric {
	case "reaction", "total", "views", "forwards", "replies":
		v, label := st.count(n.Metric, n.Key)
		return cmp(float64(v)), fmt.Sprintf("%s %s %s %d", label, Count(v), sym, int64(n.num))
	case "ratio":
		num, label := st.count("reaction", n.Num)
		if n.Num == "total" {
			num, label = st.Total, "reaction 总数"
		}
		den := st.Total
		if n.Den == "views" {
			den = st.Views
		}
		if den == 0 {
			return false, ""
		}
		pct := float64(num) * 100 / float64(den)
		label += " 占比"
		if n.Den == "views" {
			label = strings.TrimSuffix(label, " 占比") + "/浏览"
		}
		return cmp(pct), fmt.Sprintf("%s %s %s %s", label, percent(pct), sym, percent(n.num))
	case "type":
		has := st.Kinds[n.str]
		if n.Cmp == "is" {
			return has, "类型为" + kindNames[n.str]
		}
		return !has, "类型不是" + kindNames[n.str]
	case "text":
		has := strings.Contains(strings.ToLower(st.Text), n.str)
		if n.Cmp == "contains" {
			return has, "包含「" + n.orig + "」"
		}
		return !has, "不包含「" + n.orig + "」"
	}
	return false, ""
}

func (st Stats) count(metric, key string) (int, string) {
	switch metric {
	case "total":
		return st.Total, metricNames[metric]
	case "views":
		return st.Views, metricNames[metric]
	case "forwards":
		return st.Forwards, metricNames[metric]
	case "replies":
		return st.Replies, metricNames[metric]
	}
	return st.Reactions[key], KeyLabel(key)
}

// KeyLabel is how a reaction key reads in explanations.
func KeyLabel(key string) string {
	switch {
	case key == KeyPaid:
		return "⭐"
	case strings.HasPrefix(key, CustomKeyPrefix):
		return "自定义表情"
	}
	return key
}

// Count formats a counter Telegram-style: 999, 1.2k, 8.1k, 12k, 1.5M.
func Count(v int) string {
	f := float64(v)
	switch {
	case v < 1000:
		return strconv.Itoa(v)
	case v < 1000000:
		return short(f/1000) + "k"
	}
	return short(f/1000000) + "M"
}

func short(f float64) string {
	if f >= 10 {
		return strconv.Itoa(int(f))
	}
	return strings.TrimSuffix(strconv.FormatFloat(math.Floor(f*10)/10, 'f', 1, 64), ".0")
}

func percent(f float64) string {
	return strings.TrimSuffix(strconv.FormatFloat(math.Round(f*10)/10, 'f', 1, 64), ".0") + "%"
}
