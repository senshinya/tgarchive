package httpapi

import (
	"encoding/json"
	"testing"
)

func TestAllMediaEndpoint(t *testing.T) {
	e := newReadEnv(t)
	for _, p := range []string{"/api/media", "/api/media?type=photo&source=private&limit=5", "/api/media?type=all&source=all"} {
		w := do(e.h, "GET", p, nil)
		var msgs []struct {
			ID int64 `json:"id"`
		}
		json.Unmarshal(w.Body.Bytes(), &msgs)
		if w.Code != 200 || len(msgs) != 1 || msgs[0].ID != e.photoMsg {
			t.Fatalf("%s = %d %s", p, w.Code, w.Body)
		}
	}
	if w := do(e.h, "GET", "/api/media?source=channel", nil); w.Code != 200 || w.Body.String() != "[]\n" {
		t.Fatalf("channels = %d %q", w.Code, w.Body)
	}
	if w := do(e.h, "GET", "/api/media?type=video&before=1", nil); w.Code != 200 || w.Body.String() != "[]\n" {
		t.Fatalf("before = %d %q", w.Code, w.Body)
	}
	for _, p := range []string{"/api/media?type=file", "/api/media?source=x", "/api/media?limit=101", "/api/media?before=-1"} {
		if w := do(e.h, "GET", p, nil); w.Code != 400 {
			t.Fatalf("%s = %d", p, w.Code)
		}
	}
}

func TestStatsEndpoint(t *testing.T) {
	e := newReadEnv(t)
	e.srv.Cfg.DataDir = t.TempDir()
	w := do(e.h, "GET", "/api/stats?tz=480", nil)
	var st struct {
		Totals struct {
			Messages   int64 `json:"messages"`
			MediaBytes int64 `json:"media_bytes"`
			DiskTotal  int64 `json:"disk_total"`
			DiskFree   int64 `json:"disk_free"`
		} `json:"totals"`
		Daily       []json.RawMessage `json:"daily"`
		MediaStates map[string]int64  `json:"media_states"`
		Watches     []json.RawMessage `json:"watches"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil || w.Code != 200 {
		t.Fatalf("stats = %d %s", w.Code, w.Body)
	}
	if st.Totals.Messages != 2 || st.Totals.MediaBytes != 11 || st.Totals.DiskTotal <= 0 || st.Totals.DiskFree <= 0 ||
		st.MediaStates["done"] != 1 || st.Watches == nil {
		t.Fatalf("stats = %s", w.Body)
	}
	if w := do(e.h, "GET", "/api/stats", nil); w.Code != 200 {
		t.Fatalf("no tz = %d", w.Code)
	}
	for _, p := range []string{"/api/stats?tz=841", "/api/stats?tz=-841", "/api/stats?tz=x"} {
		if w := do(e.h, "GET", p, nil); w.Code != 400 {
			t.Fatalf("%s = %d", p, w.Code)
		}
	}
}
