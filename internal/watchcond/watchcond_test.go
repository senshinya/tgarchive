package watchcond

import (
	"reflect"
	"strings"
	"testing"
)

func mustParse(t *testing.T, s string) *Node {
	t.Helper()
	n, err := Parse([]byte(s))
	if err != nil {
		t.Fatalf("Parse(%s): %v", s, err)
	}
	return n
}

var post = Stats{
	Reactions: map[string]int{"🔥": 23, "👍": 5, "custom:42": 3, "paid": 10},
	Total:     41, Views: 8123, Forwards: 7, Replies: 0,
	Kinds: map[string]bool{"photo": true}, Text: "Hello World",
}

func TestEvalMetrics(t *testing.T) {
	cases := []struct {
		cond string
		want bool
	}{
		{`{"op":"and","items":[{"metric":"reaction","key":"🔥","cmp":"gte","value":10}]}`, true},
		{`{"op":"and","items":[{"metric":"reaction","key":"❤","cmp":"gte","value":1}]}`, false},
		{`{"op":"and","items":[{"metric":"reaction","key":"custom:42","cmp":"gte","value":3}]}`, true},
		{`{"op":"and","items":[{"metric":"reaction","key":"paid","cmp":"lte","value":9}]}`, false},
		{`{"op":"and","items":[{"metric":"total","cmp":"gte","value":41}]}`, true},
		{`{"op":"and","items":[{"metric":"views","cmp":"gte","value":5000}]}`, true},
		{`{"op":"and","items":[{"metric":"forwards","cmp":"lte","value":6}]}`, false},
		{`{"op":"and","items":[{"metric":"replies","cmp":"lte","value":0}]}`, true},
		{`{"op":"and","items":[{"metric":"ratio","num":"🔥","den":"total","cmp":"gte","value":56}]}`, true},
		{`{"op":"and","items":[{"metric":"ratio","num":"🔥","den":"total","cmp":"gte","value":57}]}`, false},
		{`{"op":"and","items":[{"metric":"ratio","num":"total","den":"views","cmp":"gte","value":0.5}]}`, true},
		{`{"op":"and","items":[{"metric":"type","cmp":"is","value":"photo"}]}`, true},
		{`{"op":"and","items":[{"metric":"type","cmp":"not","value":"video"}]}`, true},
		{`{"op":"and","items":[{"metric":"text","cmp":"contains","value":"WORLD"}]}`, true},
		{`{"op":"and","items":[{"metric":"text","cmp":"not_contains","value":"hello"}]}`, false},
		// AND / OR nesting: 🔥 ≥ 100 OR (views ≥ 8000 AND 👍 ≥ 5)
		{`{"op":"or","items":[{"metric":"reaction","key":"🔥","cmp":"gte","value":100},
			{"op":"and","items":[{"metric":"views","cmp":"gte","value":8000},{"metric":"reaction","key":"👍","cmp":"gte","value":5}]}]}`, true},
		{`{"op":"and","items":[{"metric":"reaction","key":"🔥","cmp":"gte","value":100},
			{"op":"or","items":[{"metric":"views","cmp":"gte","value":8000}]}]}`, false},
	}
	for _, c := range cases {
		if got := mustParse(t, c.cond).Eval(post); got != c.want {
			t.Errorf("%s = %v, want %v", c.cond, got, c.want)
		}
	}
}

func TestRatioZeroDenominatorIsFalse(t *testing.T) {
	n := mustParse(t, `{"op":"or","items":[{"metric":"ratio","num":"🔥","den":"total","cmp":"lte","value":100}]}`)
	if n.Eval(Stats{}) {
		t.Fatal("ratio over zero total must be false")
	}
	if n.Eval(Stats{Views: 10}) {
		t.Fatal("ratio over zero total must be false")
	}
}

func TestExplain(t *testing.T) {
	n := mustParse(t, `{"op":"or","items":[
		{"metric":"reaction","key":"🔥","cmp":"gte","value":10},
		{"metric":"views","cmp":"gte","value":5000},
		{"metric":"forwards","cmp":"gte","value":100},
		{"metric":"ratio","num":"🔥","den":"total","cmp":"gte","value":50},
		{"metric":"ratio","num":"total","den":"views","cmp":"gte","value":0.5},
		{"metric":"reaction","key":"paid","cmp":"gte","value":1},
		{"metric":"type","cmp":"is","value":"photo"},
		{"metric":"text","cmp":"contains","value":"World"}]}`)
	want := []string{"🔥 23 ≥ 10", "浏览 8.1K ≥ 5000", "🔥 占比 56.1% ≥ 50%", "reaction 总数/浏览 0.5% ≥ 0.5%", "⭐ 10 ≥ 1",
		"类型为图片", "包含「World」"}
	if got := n.Explain(post); !reflect.DeepEqual(got, want) {
		t.Fatalf("Explain = %q\nwant      %q", got, want)
	}
}

func TestCount(t *testing.T) {
	for v, want := range map[int]string{0: "0", 999: "999", 1000: "1K", 1250: "1.2K", 8123: "8.1K", 12500: "12K", 1500000: "1.5M"} {
		if got := Count(v); got != want {
			t.Errorf("Count(%d) = %s, want %s", v, got, want)
		}
	}
}

func TestParseRejects(t *testing.T) {
	deep := `{"op":"and","items":[{"op":"and","items":[{"op":"and","items":[{"op":"and","items":[{"metric":"views","cmp":"gte","value":1}]}]}]}]}`
	many := `{"op":"and","items":[` + strings.TrimSuffix(strings.Repeat(`{"metric":"views","cmp":"gte","value":1},`, 31), ",") + `]}`
	cases := map[string]string{
		`not json`: "条件格式错误",
		`{"metric":"views","cmp":"gte","value":1}`:      "最外层必须是条件组",
		`{"op":"xor","items":[]}`:                       "条件组只能是",
		`{"op":"and","items":[]}`:                       "条件组不能为空",
		`{"op":"and","items":[{"op":"or","items":[]}]}`: "条件组不能为空",
		deep: "最多嵌套 3 层",
		many: "条件最多 30 个",
		`{"op":"and","items":[{"metric":"likes","cmp":"gte","value":1}]}`:                                      "未知的条件类型",
		`{"op":"and","items":[{"metric":"views","cmp":"gt","value":1}]}`:                                       "比较方式错误",
		`{"op":"and","items":[{"metric":"views","cmp":"gte","value":-1}]}`:                                     "非负整数",
		`{"op":"and","items":[{"metric":"views","cmp":"gte","value":1.5}]}`:                                    "非负整数",
		`{"op":"and","items":[{"metric":"views","cmp":"gte","value":"1"}]}`:                                    "非负整数",
		`{"op":"and","items":[{"metric":"reaction","cmp":"gte","value":1}]}`:                                   "请选择表情",
		`{"op":"and","items":[{"metric":"reaction","key":"custom:x","cmp":"gte","value":1}]}`:                  "自定义表情格式错误",
		`{"op":"and","items":[{"metric":"ratio","num":"🔥","den":"forwards","cmp":"gte","value":1}]}`:           "分母",
		`{"op":"and","items":[{"metric":"ratio","num":"total","den":"total","cmp":"gte","value":1}]}`:          "不能相同",
		`{"op":"and","items":[{"metric":"ratio","num":"🔥","den":"total","cmp":"gte","value":101}]}`:            "0–100",
		`{"op":"and","items":[{"metric":"type","cmp":"is","value":"gif"}]}`:                                    "内容类型",
		`{"op":"and","items":[{"metric":"text","cmp":"contains","value":"  "}]}`:                               "关键词",
		`{"op":"and","items":[{"metric":"text","cmp":"contains","value":"` + strings.Repeat("字", 101) + `"}]}`: "关键词",
		`{"op":"and","items":[{"metric":"views","cmp":"gte","value":1,"extra":1}]}`:                            "条件格式错误",
		`{"op":"and","items":[{"metric":"views","cmp":"gte","value":1}],"metric":"views"}`:                     "条件格式错误",
		`{"op":"and","items":[{"metric":"views","cmp":"gte","value":1,"items":[{"metric":"views"}]}]}`:         "条件格式错误",
	}
	for in, want := range cases {
		_, err := Parse([]byte(in))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%.60s) = %v, want %q", in, err, want)
		}
	}
}
