package linkparse

import (
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	ok := []struct {
		in   string
		want Link
	}{
		{"https://t.me/durov/123", Link{Username: "durov", MsgID: 123}},
		{"t.me/durov/123", Link{Username: "durov", MsgID: 123}},
		{"http://telegram.me/durov/123", Link{Username: "durov", MsgID: 123}},
		{"https://www.t.me/durov/123", Link{Username: "durov", MsgID: 123}},
		{"HTTPS://T.ME/durov/123", Link{Username: "durov", MsgID: 123}},
		{"https://t.me/durov/123?single", Link{Username: "durov", MsgID: 123, Single: true}},
		{"https://t.me/durov/123?single=1&comment=5", Link{Username: "durov", MsgID: 123, Single: true}},
		{"https://t.me/durov/9/123", Link{Username: "durov", TopicID: 9, MsgID: 123}},
		{"https://t.me/s/durov/123", Link{Username: "durov", MsgID: 123}},
		{"https://t.me/c/1234567890/55", Link{ChannelID: 1234567890, MsgID: 55}},
		{"https://t.me/c/1234567890/7/55?single", Link{ChannelID: 1234567890, TopicID: 7, MsgID: 55, Single: true}},
		{"https://t.me/durov/123/", Link{Username: "durov", MsgID: 123}},
	}
	for _, c := range ok {
		got, err := Parse(c.in)
		if err != nil || got != c.want {
			t.Errorf("Parse(%q) = %+v, %v; want %+v", c.in, got, err, c.want)
		}
	}
	unsupported := []string{
		"https://t.me/durov", "https://t.me/", "https://t.me/joinchat/AAAA", "https://t.me/+AbCdEf",
		"https://t.me/addstickers/foo", "https://t.me/c/123", "https://t.me/c/abc/5", "https://t.me/durov/0",
		"https://t.me/durov/-1", "https://t.me/durov/x", "https://t.me/ab/5", "https://t.me/durov/1/2/3",
		"https://t.me/share/url/5",
	}
	for _, in := range unsupported {
		if _, err := Parse(in); !errors.Is(err, ErrUnsupported) {
			t.Errorf("Parse(%q) err = %v, want ErrUnsupported", in, err)
		}
	}
	for _, in := range []string{"hello", "https://example.com/durov/1", "https://t.me.evil.com/durov/1", "https://t.me:8443/durov/1", "ftp://t.me/durov/1"} {
		if _, err := Parse(in); !errors.Is(err, ErrNotLink) {
			t.Errorf("Parse(%q) err = %v, want ErrNotLink", in, err)
		}
	}
	if ErrUnsupported.Error() != "不支持的链接格式" {
		t.Fatal("ErrUnsupported text changed")
	}
}

func TestCandidate(t *testing.T) {
	for _, in := range []string{"https://t.me/durov/1", "  t.me/durov/1\n", "https://t.me/joinchat/x", "https://t.me/durov"} {
		if _, ok := Candidate(in); !ok {
			t.Errorf("Candidate(%q) = false", in)
		}
	}
	for _, in := range []string{"", "hello", "see https://t.me/durov/1", "https://t.me/durov/1 https://t.me/durov/2", "https://example.com/a/1"} {
		if _, ok := Candidate(in); ok {
			t.Errorf("Candidate(%q) = true", in)
		}
	}
	if got, _ := Candidate("  t.me/durov/1 "); got != "t.me/durov/1" {
		t.Fatalf("Candidate trims to %q", got)
	}
}
