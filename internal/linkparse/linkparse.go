// Package linkparse recognises Telegram message links (t.me/...) that the userbot can fetch.
package linkparse

import (
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Link is a parsed message link. Exactly one of Username / ChannelID is set.
type Link struct {
	Username  string // public link: t.me/<username>/...
	ChannelID int64  // private link: t.me/c/<channel_id>/...
	TopicID   int64
	MsgID     int64
	Single    bool // ?single: only this message, not its whole album
}

var (
	ErrNotLink     = errors.New("not a telegram link")
	ErrUnsupported = errors.New("不支持的链接格式")
)

var usernameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{3,31}$`)

// reserved are first path segments that look like usernames but are Telegram service paths.
var reserved = map[string]bool{
	"joinchat": true, "addstickers": true, "addemoji": true, "addlist": true, "addtheme": true, "share": true,
	"proxy": true, "socks": true, "boost": true, "login": true, "invoice": true, "giftcode": true,
	"setlanguage": true, "confirmphone": true, "contact": true,
}

// Candidate reports whether text, trimmed, is exactly one t.me / telegram.me URL and returns it.
func Candidate(text string) (string, bool) {
	s := strings.TrimSpace(text)
	if s == "" || strings.ContainsAny(s, " \t\r\n") {
		return "", false
	}
	if _, _, err := split(s); err != nil {
		return "", false
	}
	return s, true
}

// Parse parses a message link. Non-Telegram input yields ErrNotLink; Telegram URLs that are not
// message links yield ErrUnsupported.
func Parse(s string) (Link, error) {
	segs, q, err := split(strings.TrimSpace(s))
	if err != nil {
		return Link{}, err
	}
	var l Link
	_, l.Single = q["single"]
	if len(segs) > 0 && segs[0] == "s" { // t.me/s/<username>/<msg> is the web preview of the same post
		segs = segs[1:]
	}
	if len(segs) > 0 && segs[0] == "c" {
		if len(segs) != 3 && len(segs) != 4 {
			return Link{}, ErrUnsupported
		}
		id, ok := positive(segs[1])
		if !ok {
			return Link{}, ErrUnsupported
		}
		l.ChannelID, segs = id, segs[2:]
	} else {
		if (len(segs) != 2 && len(segs) != 3) || !usernameRe.MatchString(segs[0]) || reserved[strings.ToLower(segs[0])] {
			return Link{}, ErrUnsupported
		}
		l.Username, segs = segs[0], segs[1:]
	}
	if len(segs) == 2 {
		topic, ok := positive(segs[0])
		if !ok {
			return Link{}, ErrUnsupported
		}
		l.TopicID, segs = topic, segs[1:]
	}
	msg, ok := positive(segs[0])
	if !ok {
		return Link{}, ErrUnsupported
	}
	l.MsgID = msg
	return l, nil
}

func split(s string) ([]string, url.Values, error) {
	rest := s
	switch lower := strings.ToLower(s); {
	case strings.HasPrefix(lower, "https://"):
		rest = s[len("https://"):]
	case strings.HasPrefix(lower, "http://"):
		rest = s[len("http://"):]
	case strings.Contains(lower, "://"):
		return nil, nil, ErrNotLink
	}
	u, err := url.Parse("https://" + rest)
	if err != nil || u.User != nil || u.Port() != "" {
		return nil, nil, ErrNotLink
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	if host != "t.me" && host != "telegram.me" {
		return nil, nil, ErrNotLink
	}
	p := strings.Trim(u.Path, "/")
	var segs []string
	if p != "" {
		segs = strings.Split(p, "/")
	}
	return segs, u.Query(), nil
}

func positive(s string) (int64, bool) {
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n > 0
}
