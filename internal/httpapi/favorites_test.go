package httpapi

import (
	"encoding/json"
	"fmt"
	"testing"

	"tgarchive/internal/events"
	"tgarchive/internal/store"
)

func drain(ch <-chan events.Event) []string {
	var types []string
	for {
		select {
		case ev := <-ch:
			types = append(types, ev.Type)
		default:
			return types
		}
	}
}

func TestFavoritesEndpoints(t *testing.T) {
	e := newReadEnv(t)
	ch, unsub := e.hub.Subscribe()
	defer unsub()
	fav := fmt.Sprintf("/api/messages/%d/favorite", e.photoMsg)
	tags := fmt.Sprintf("/api/messages/%d/tags", e.photoMsg)

	if w := call(e.h, "PUT", tags, map[string]any{"tags": []string{"x"}}); w.Code != 409 {
		t.Fatalf("tags before favoriting = %d %s", w.Code, w.Body)
	}
	w := call(e.h, "PUT", fav, map[string]any{"tags": []string{"旅行"}})
	var info store.FavoriteInfo
	json.Unmarshal(w.Body.Bytes(), &info)
	if w.Code != 200 || info.At == 0 || len(info.Tags) != 1 || info.Tags[0].Name != "旅行" {
		t.Fatalf("favorite = %d %s", w.Code, w.Body)
	}
	if got := drain(ch); len(got) != 2 || got[0] != "message.updated" || got[1] != "favorites.updated" {
		t.Fatalf("events = %v", got)
	}
	if w := call(e.h, "PUT", fav, nil); w.Code != 200 {
		t.Fatalf("favorite without body = %d %s", w.Code, w.Body)
	}
	if w := call(e.h, "PUT", tags, map[string]any{"tags": []string{"a\nb"}}); w.Code != 400 {
		t.Fatalf("bad tag = %d", w.Code)
	}
	if w := call(e.h, "PUT", tags, map[string]any{"tags": []string{"food", "旅行"}}); w.Code != 200 {
		t.Fatalf("tags = %d %s", w.Code, w.Body)
	}
	var page struct {
		Items []store.FavoriteItem `json:"items"`
		Next  int64                `json:"next"`
	}
	json.Unmarshal(call(e.h, "GET", "/api/favorites", nil).Body.Bytes(), &page)
	if len(page.Items) != 1 || page.Items[0].Message.ID != e.photoMsg || len(page.Items[0].Message.Favorite.Tags) != 2 || page.Next != 0 {
		t.Fatalf("favorites = %+v", page)
	}
	var tagList []store.TagCount
	json.Unmarshal(call(e.h, "GET", "/api/tags", nil).Body.Bytes(), &tagList)
	if len(tagList) != 2 || tagList[0].Name != "food" || tagList[0].Count != 1 {
		t.Fatalf("tags = %+v", tagList)
	}
	json.Unmarshal(call(e.h, "GET", fmt.Sprintf("/api/favorites?tag=%d&limit=1", tagList[0].ID), nil).Body.Bytes(), &page)
	if len(page.Items) != 1 || page.Next != page.Items[0].FavID {
		t.Fatalf("filtered = %+v", page)
	}
	if w := call(e.h, "DELETE", fmt.Sprintf("/api/tags/%d", tagList[0].ID), nil); w.Code != 204 {
		t.Fatalf("delete tag = %d", w.Code)
	}
	if w := call(e.h, "DELETE", fmt.Sprintf("/api/tags/%d", tagList[0].ID), nil); w.Code != 404 {
		t.Fatalf("delete tag again = %d", w.Code)
	}
	drain(ch)
	if w := call(e.h, "DELETE", fav, nil); w.Code != 204 {
		t.Fatalf("unfavorite = %d", w.Code)
	}
	if got := drain(ch); len(got) != 2 {
		t.Fatalf("unfavorite events = %v", got)
	}
	for _, c := range []struct{ method, path string }{
		{"PUT", "/api/messages/9999/favorite"},
		{"DELETE", "/api/messages/9999/favorite"},
		{"PUT", "/api/messages/9999/tags"},
	} {
		if w := call(e.h, c.method, c.path, map[string]any{"tags": []string{}}); w.Code != 404 {
			t.Fatalf("%s %s = %d", c.method, c.path, w.Code)
		}
	}
	for _, p := range []string{"/api/favorites?limit=0", "/api/favorites?tag=x", "/api/favorites?before=-1"} {
		if w := call(e.h, "GET", p, nil); w.Code != 400 {
			t.Fatalf("%s = %d", p, w.Code)
		}
	}
	if w := call(e.h, "PUT", fav, "not an object"); w.Code != 400 {
		t.Fatalf("bad body = %d", w.Code)
	}
}
