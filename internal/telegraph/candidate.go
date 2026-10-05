// Package telegraph archives Telegraph (telegra.ph / graph.org) articles: link recognition,
// content normalization, the getPage client and the serial fetch worker.
package telegraph

import (
	"net/url"
	"strings"
	"unicode"
)

// reserved are first path segments of telegra.ph that are site paths, not articles.
var reserved = map[string]bool{
	"file": true, "api": true, "edit": true, "embed": true, "upload": true, "auth": true,
	"js": true, "css": true, "images": true, "img": true,
}

// Candidate reports whether text, trimmed, is exactly one Telegraph article URL and returns the
// article path (one segment, URL-decoded, no leading slash). The scheme may be omitted; host is
// telegra.ph or graph.org (any case, optional www.); query and fragment are ignored.
func Candidate(text string) (string, bool) {
	s := strings.TrimSpace(text)
	if s == "" || strings.IndexFunc(s, unicode.IsSpace) >= 0 {
		return "", false
	}
	rest := s
	switch lower := strings.ToLower(s); {
	case strings.HasPrefix(lower, "https://"):
		rest = s[len("https://"):]
	case strings.HasPrefix(lower, "http://"):
		rest = s[len("http://"):]
	case strings.Contains(lower, "://"):
		return "", false
	}
	u, err := url.Parse("https://" + rest)
	if err != nil || u.User != nil || u.Port() != "" {
		return "", false
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	if host != "telegra.ph" && host != "graph.org" {
		return "", false
	}
	p := strings.Trim(u.Path, "/")
	if p == "" || strings.Contains(p, "/") || reserved[strings.ToLower(p)] {
		return "", false
	}
	return p, true
}
