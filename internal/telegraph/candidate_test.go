package telegraph

import "testing"

func TestCandidate(t *testing.T) {
	ok := []struct{ in, want string }{
		{"https://telegra.ph/Sample-Page-12-15", "Sample-Page-12-15"},
		{"  telegra.ph/Sample-Page-12-15\n", "Sample-Page-12-15"},
		{"http://telegra.ph/Sample", "Sample"},
		{"HTTPS://TELEGRA.PH/Sample", "Sample"},
		{"https://www.telegra.ph/Sample", "Sample"},
		{"https://graph.org/Sample-10-05", "Sample-10-05"},
		{"https://telegra.ph/Sample/", "Sample"},
		{"https://telegra.ph/Sample?x=1#frag", "Sample"},
		{"https://telegra.ph/%E4%BD%A0%E5%A5%BD-10-05", "你好-10-05"},
	}
	for _, c := range ok {
		got, isOK := Candidate(c.in)
		if !isOK || got != c.want {
			t.Errorf("Candidate(%q) = %q, %v; want %q", c.in, got, isOK, c.want)
		}
	}
	bad := []string{
		"", "hello", "see https://telegra.ph/Sample", "https://telegra.ph/Sample https://telegra.ph/B",
		"https://telegra.ph/Sample x", "https://telegra.ph/", "https://telegra.ph", "https://telegra.ph/a/b",
		"https://telegra.ph/file/abc.jpg", "https://telegra.ph/api", "https://telegra.ph/edit", "https://telegra.ph/FILE",
		"https://telegra.ph/a%2Fb", "https://example.com/Sample", "https://telegra.ph.evil.com/Sample",
		"https://telegra.ph:8443/Sample", "https://user@telegra.ph/Sample", "ftp://telegra.ph/Sample", "https://t.me/durov/1",
		"https://telegra.ph/..", "https://telegra.ph/.", "https://telegra.ph/%2e%2e", "https://telegra.ph/%2e",
	}
	for _, in := range bad {
		if got, isOK := Candidate(in); isOK {
			t.Errorf("Candidate(%q) = %q, true; want false", in, got)
		}
	}
}
