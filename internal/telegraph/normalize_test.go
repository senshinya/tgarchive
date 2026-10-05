package telegraph

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func parse(t *testing.T, s string) []Node {
	t.Helper()
	var nodes []Node
	if err := json.Unmarshal([]byte(s), &nodes); err != nil {
		t.Fatalf("unmarshal %s: %v", s, err)
	}
	return nodes
}

func dump(t *testing.T, nodes []Node) string {
	t.Helper()
	b, err := json.Marshal(nodes)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestNodeJSONRoundTrip(t *testing.T) {
	in := `["hi",{"tag":"p","attrs":{"class":"x","n":5},"children":["a",{"tag":"br"}]}]`
	got := dump(t, parse(t, in))
	if want := `["hi",{"tag":"p","attrs":{"class":"x"},"children":["a",{"tag":"br"}]}]`; got != want {
		t.Fatalf("round trip = %s, want %s", got, want)
	}
	var n []Node
	if err := json.Unmarshal([]byte(`[{"attrs":{}}]`), &n); err == nil {
		t.Fatal("element without tag must be rejected")
	}
}

func TestNormalizeTagsAndAttrs(t *testing.T) {
	in := parse(t, `[
		{"tag":"p","attrs":{"class":"lead","style":"x"},"children":["Hello ",{"tag":"strong","children":["world"]}]},
		{"tag":"div","children":[{"tag":"span","children":["unwrapped"]},{"tag":"script","children":["alert(1)"]}]},
		{"tag":"H3","attrs":{"id":"t"},"children":["Title"]},
		{"tag":"a","attrs":{"href":"/Other-Page","target":"_blank"},"children":["rel"]},
		{"tag":"a","attrs":{"href":"javascript:alert(1)"},"children":["js"]},
		{"tag":"a","attrs":{"href":"#anchor"},"children":["anchor"]},
		{"tag":"a","attrs":{"href":"mailto:a@b.c"},"children":["mail"]},
		{"tag":"hr","children":["ignored"]},
		{"tag":"blockquote","children":["q"]},{"tag":"aside","children":["pull"]},
		{"tag":"pre","children":["code"]},{"tag":"ul","children":[{"tag":"li","children":["one"]}]}
	]`)
	got, refs := Normalize(in)
	want := `[{"tag":"p","children":["Hello ",{"tag":"strong","children":["world"]}]},` +
		`"unwrapped","alert(1)",` +
		`{"tag":"h3","children":["Title"]},` +
		`{"tag":"a","attrs":{"href":"https://telegra.ph/Other-Page"},"children":["rel"]},` +
		`{"tag":"a","children":["js"]},{"tag":"a","children":["anchor"]},` +
		`{"tag":"a","attrs":{"href":"mailto:a@b.c"},"children":["mail"]},` +
		`{"tag":"hr"},{"tag":"blockquote","children":["q"]},{"tag":"aside","children":["pull"]},` +
		`{"tag":"pre","children":["code"]},{"tag":"ul","children":[{"tag":"li","children":["one"]}]}]`
	if s := dump(t, got); s != want {
		t.Fatalf("normalized =\n%s\nwant\n%s", s, want)
	}
	if len(refs) != 0 {
		t.Fatalf("refs = %+v", refs)
	}
}

func TestNormalizeMedia(t *testing.T) {
	in := parse(t, `[
		{"tag":"figure","children":[{"tag":"img","attrs":{"src":"/file/abc.jpg","alt":"x"}},{"tag":"figcaption","children":["cap"]}]},
		{"tag":"img","attrs":{"src":"https://cdn.example.com/a.png"}},
		{"tag":"img","attrs":{"src":"/file/abc.jpg"}},
		{"tag":"video","attrs":{"src":"//cdn.example.com/v.mp4","autoplay":"autoplay"}},
		{"tag":"img","attrs":{"src":"data:image/png;base64,AAAA"}},
		{"tag":"img"}
	]`)
	got, refs := Normalize(in)
	want := `[{"tag":"figure","children":[{"tag":"img","attrs":{"data-src":"https://telegra.ph/file/abc.jpg"}},{"tag":"figcaption","children":["cap"]}]},` +
		`{"tag":"img","attrs":{"data-src":"https://cdn.example.com/a.png"}},` +
		`{"tag":"img","attrs":{"data-src":"https://telegra.ph/file/abc.jpg"}},` +
		`{"tag":"video","attrs":{"data-src":"https://cdn.example.com/v.mp4"}}]`
	if s := dump(t, got); s != want {
		t.Fatalf("normalized =\n%s\nwant\n%s", s, want)
	}
	wantRefs := []MediaRef{
		{URL: "https://telegra.ph/file/abc.jpg", Kind: KindPhoto},
		{URL: "https://cdn.example.com/a.png", Kind: KindPhoto},
		{URL: "https://cdn.example.com/v.mp4", Kind: KindVideo},
	}
	if !reflect.DeepEqual(refs, wantRefs) {
		t.Fatalf("refs = %+v", refs)
	}
	AttachMediaIDs(got, map[string]int64{"https://telegra.ph/file/abc.jpg": 7, "https://cdn.example.com/v.mp4": 9})
	s := dump(t, got)
	for _, frag := range []string{
		`{"tag":"img","attrs":{"data-media-id":"7","data-src":"https://telegra.ph/file/abc.jpg"}}`,
		`{"tag":"img","attrs":{"data-src":"https://cdn.example.com/a.png"}}`,
		`{"tag":"video","attrs":{"data-media-id":"9","data-src":"https://cdn.example.com/v.mp4"}}`,
	} {
		if !strings.Contains(s, frag) {
			t.Fatalf("missing %s in %s", frag, s)
		}
	}
}

func TestNormalizeEmbeds(t *testing.T) {
	cases := []struct{ src, href string }{
		{"/embed/youtube?url=https%3A%2F%2Fwww.youtube.com%2Fwatch%3Fv%3DdQw4w9WgXcQ", "https://www.youtube.com/watch?v=dQw4w9WgXcQ"},
		{"https://www.youtube.com/embed/dQw4w9WgXcQ", "https://www.youtube.com/watch?v=dQw4w9WgXcQ"},
		{"https://player.vimeo.com/video/76979871", "https://vimeo.com/76979871"},
		{"https://platform.twitter.com/embed/Tweet.html?id=20", "https://twitter.com/i/status/20"},
		{"/embed/youtube?url=javascript%3Aalert(1)", "https://telegra.ph/embed/youtube?url=javascript%3Aalert(1)"},
		{"https://maps.example.com/embed?q=1", "https://maps.example.com/embed?q=1"},
	}
	for _, c := range cases {
		in := []Node{{Tag: "figure", Children: []Node{{Tag: "iframe", Attrs: map[string]string{"src": c.src, "width": "640"}}}}}
		got, refs := Normalize(in)
		emb := got[0].Children[0]
		if emb.Tag != "embed" || emb.Attrs["href"] != c.href || len(emb.Attrs) != 2 || len(refs) != 0 {
			t.Errorf("iframe %s -> %+v (refs %v); want href %s", c.src, emb, refs, c.href)
		}
	}
	if got, _ := Normalize([]Node{{Tag: "iframe", Attrs: map[string]string{"src": "javascript:alert(1)"}}}); len(got) != 0 {
		t.Fatalf("javascript iframe kept: %+v", got)
	}
}

func TestNormalizeDepthLimit(t *testing.T) {
	n := Node{Text: "deep"}
	for i := 0; i < 200; i++ {
		n = Node{Tag: "b", Children: []Node{n}}
	}
	got, _ := Normalize([]Node{n})
	if strings.Contains(dump(t, got), "deep") {
		t.Fatal("text below maxDepth must be dropped")
	}
}

func TestWebKey(t *testing.T) {
	k := WebKey("https://telegra.ph/file/abc.jpg")
	if !strings.HasPrefix(k, "web:") || len(k) != 4+64 || k != WebKey("https://telegra.ph/file/abc.jpg") || k == WebKey("https://telegra.ph/file/abd.jpg") {
		t.Fatalf("WebKey = %q", k)
	}
}
