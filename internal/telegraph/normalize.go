package telegraph

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// SiteURL resolves relative URLs found in article content (/file/...).
const SiteURL = "https://telegra.ph"

const (
	KindPhoto = "photo"
	KindVideo = "video"
)

// maxDepth bounds recursion on hostile nesting; deeper elements are dropped.
const maxDepth = 64

// MediaRef is one image or video an article references, in first-appearance order.
type MediaRef struct {
	URL  string // absolute http(s) URL as found in the article
	Kind string // KindPhoto or KindVideo
}

// WebKey is the media dedupe_key for a web URL: "web:" + hex(sha256(url)).
func WebKey(rawURL string) string {
	sum := sha256.Sum256([]byte(rawURL))
	return "web:" + hex.EncodeToString(sum[:])
}

var allowedTags = map[string]bool{
	"a": true, "aside": true, "b": true, "blockquote": true, "br": true, "code": true, "em": true,
	"figcaption": true, "figure": true, "h3": true, "h4": true, "hr": true, "i": true, "iframe": true,
	"img": true, "li": true, "ol": true, "p": true, "pre": true, "s": true, "strong": true, "u": true,
	"ul": true, "video": true,
}

var base, _ = url.Parse(SiteURL + "/")

// Normalize cleans a Telegraph node tree for storage (spec §5): unknown tags are unwrapped,
// only a.href / img.src / video.src / iframe.src survive, relative URLs resolve against
// telegra.ph, img/video carry data-src (absolute URL) instead of src, and iframe becomes
// {tag:"embed", attrs:{href, src}}. It also returns the media the tree references, deduplicated.
func Normalize(nodes []Node) ([]Node, []MediaRef) {
	n := &normalizer{seen: map[string]bool{}}
	return n.list(nodes, 0), n.refs
}

type normalizer struct {
	refs []MediaRef
	seen map[string]bool
}

func (n *normalizer) list(in []Node, depth int) []Node {
	out := []Node{}
	for _, c := range in {
		out = append(out, n.node(c, depth)...)
	}
	return out
}

func (n *normalizer) node(in Node, depth int) []Node {
	if in.Tag == "" {
		if in.Text == "" {
			return nil
		}
		return []Node{{Text: in.Text}}
	}
	if depth >= maxDepth {
		return nil
	}
	tag := strings.ToLower(in.Tag)
	if !allowedTags[tag] {
		return n.list(in.Children, depth+1)
	}
	switch tag {
	case "img", "video":
		src, ok := absURL(in.Attrs["src"])
		if !ok {
			return nil
		}
		kind := KindPhoto
		if tag == "video" {
			kind = KindVideo
		}
		if !n.seen[src] {
			n.seen[src] = true
			n.refs = append(n.refs, MediaRef{URL: src, Kind: kind})
		}
		return []Node{{Tag: tag, Attrs: map[string]string{"data-src": src}}}
	case "iframe":
		src, ok := absURL(in.Attrs["src"])
		if !ok {
			return nil
		}
		return []Node{{Tag: "embed", Attrs: map[string]string{"href": embedHref(src), "src": src}}}
	case "br", "hr":
		return []Node{{Tag: tag}}
	}
	out := Node{Tag: tag, Children: n.list(in.Children, depth+1)}
	if tag == "a" {
		if href, ok := linkURL(in.Attrs["href"]); ok {
			out.Attrs = map[string]string{"href": href}
		}
	}
	return []Node{out}
}

// AttachMediaIDs sets data-media-id on every img/video whose data-src has an id in ids.
func AttachMediaIDs(nodes []Node, ids map[string]int64) {
	for i := range nodes {
		nd := &nodes[i]
		if (nd.Tag == "img" || nd.Tag == "video") && nd.Attrs != nil {
			if id, ok := ids[nd.Attrs["data-src"]]; ok {
				nd.Attrs["data-media-id"] = strconv.FormatInt(id, 10)
			}
		}
		AttachMediaIDs(nd.Children, ids)
	}
}

// absURL resolves raw against telegra.ph and accepts only absolute http(s) URLs with a host.
func absURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	r := base.ResolveReference(u)
	if (r.Scheme != "http" && r.Scheme != "https") || r.Host == "" {
		return "", false
	}
	return r.String(), true
}

// strictURL accepts only an already-absolute http(s) URL (no resolution against telegra.ph).
func strictURL(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", false
	}
	return u.String(), true
}

// linkURL keeps http(s), mailto: and tg: links; in-page anchors and other schemes are dropped.
func linkURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "#") {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	switch strings.ToLower(u.Scheme) {
	case "mailto", "tg":
		return u.String(), true
	}
	return absURL(raw)
}

var (
	digits    = regexp.MustCompile(`^[0-9]+$`)
	youtubeID = regexp.MustCompile(`^[A-Za-z0-9_-]{6,20}$`)
)

// embedHref guesses the original page of an embed: Telegraph's own /embed/<kind>?url=<page>,
// YouTube embed/<id>, Vimeo video/<id>, Twitter ?id=<id>; anything else keeps src.
func embedHref(src string) string {
	u, err := url.Parse(src)
	if err != nil {
		return src
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	switch host {
	case "telegra.ph", "graph.org":
		if strings.HasPrefix(u.Path, "/embed/") {
			if inner, ok := strictURL(u.Query().Get("url")); ok {
				return inner
			}
		}
	case "youtube.com", "youtube-nocookie.com":
		if id, ok := strings.CutPrefix(u.Path, "/embed/"); ok && youtubeID.MatchString(id) {
			return "https://www.youtube.com/watch?v=" + id
		}
	case "player.vimeo.com":
		if id, ok := strings.CutPrefix(u.Path, "/video/"); ok && digits.MatchString(id) {
			return "https://vimeo.com/" + id
		}
	case "platform.twitter.com":
		if id := u.Query().Get("id"); digits.MatchString(id) {
			return "https://twitter.com/i/status/" + id
		}
	}
	return src
}
