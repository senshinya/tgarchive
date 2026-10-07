package store

import (
	"errors"
	"strings"
	"testing"
)

func tagNames(tags []TagRef) []string {
	out := make([]string, len(tags))
	for i, t := range tags {
		out[i] = t.Name
	}
	return out
}

func sameStrings(a, b []string) bool {
	return strings.Join(a, "|") == strings.Join(b, "|")
}

func TestNormalizeTags(t *testing.T) {
	got, err := NormalizeTags([]string{" 旅行 ", "旅行", "TRAVEL", "travel", ""})
	if err != nil || !sameStrings(got, []string{"旅行", "TRAVEL"}) {
		t.Fatalf("NormalizeTags = %q, %v", got, err)
	}
	for _, bad := range [][]string{
		{strings.Repeat("长", 33)},
		{"a\nb"},
		{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11"},
	} {
		if _, err := NormalizeTags(bad); !errors.Is(err, ErrBadTag) {
			t.Fatalf("NormalizeTags(%q) err = %v", bad, err)
		}
	}
	if got, err := NormalizeTags([]string{strings.Repeat("长", 32)}); err != nil || len(got) != 1 {
		t.Fatalf("32 runes is fine: %v", err)
	}
}

func TestFavorites(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	a := ingest(t, s, bot, textMsg(1, "a"))
	b := ingest(t, s, bot, textMsg(2, "b")).MessageID
	c := ingest(t, s, bot, textMsg(3, "c")).MessageID

	info, chat, err := s.Favorite(ctx, a.MessageID, 100, nil)
	if err != nil || info.At != 100 || len(info.Tags) != 0 || chat != a.ChatID {
		t.Fatalf("Favorite = %+v %d %v", info, chat, err)
	}
	if info, _, _ = s.Favorite(ctx, a.MessageID, 200, nil); info.At != 100 {
		t.Fatalf("favoriting again keeps the time: %+v", info)
	}
	if info, _, err = s.Favorite(ctx, b, 300, []string{"旅行", "food"}); err != nil || !sameStrings(tagNames(info.Tags), []string{"food", "旅行"}) {
		t.Fatalf("with tags = %+v %v", info, err)
	}
	if _, _, err := s.SetTags(ctx, c, []string{"x"}); !errors.Is(err, ErrNotFavorite) {
		t.Fatalf("tags on a non-favorite: %v", err)
	}
	if _, _, err := s.Favorite(ctx, 9999, 1, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing message: %v", err)
	}
	tags, _, err := s.SetTags(ctx, a.MessageID, []string{"FOOD"})
	if err != nil || !sameStrings(tagNames(tags), []string{"food"}) {
		t.Fatalf("SetTags reuses tags case-insensitively: %+v %v", tags, err)
	}
	if _, _, err := s.SetTags(ctx, a.MessageID, []string{"a\nb"}); !errors.Is(err, ErrBadTag) {
		t.Fatalf("bad tag: %v", err)
	}

	counts, _ := s.ListTags(ctx)
	if len(counts) != 2 || counts[0].Name != "food" || counts[0].Count != 2 || counts[1].Name != "旅行" || counts[1].Count != 1 {
		t.Fatalf("ListTags = %+v", counts)
	}
	food := counts[0].ID

	items, err := s.ListFavorites(ctx, 0, 0, 10)
	if err != nil || len(items) != 2 || items[0].Message.ID != b || items[1].Message.ID != a.MessageID {
		t.Fatalf("ListFavorites = %+v %v", items, err)
	}
	if f := items[0].Message.Favorite; f == nil || f.At != 300 || len(f.Tags) != 2 {
		t.Fatalf("favorite info = %+v", f)
	}
	if page, _ := s.ListFavorites(ctx, 0, items[0].FavID, 10); len(page) != 1 || page[0].Message.ID != a.MessageID {
		t.Fatalf("paging = %+v", page)
	}
	if page, _ := s.ListFavorites(ctx, counts[1].ID, 0, 10); len(page) != 1 || page[0].Message.ID != b {
		t.Fatalf("tag filter = %+v", page)
	}
	if v, _ := s.GetMessageView(ctx, c); v.Favorite != nil {
		t.Fatalf("not a favorite: %+v", v.Favorite)
	}

	if err := s.DeleteTag(ctx, food); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.GetMessageView(ctx, a.MessageID); v.Favorite == nil || len(v.Favorite.Tags) != 0 {
		t.Fatalf("deleting a tag keeps the favorite: %+v", v.Favorite)
	}
	if err := s.DeleteTag(ctx, food); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second DeleteTag: %v", err)
	}

	if _, _, err := s.DeleteMessage(ctx, b, 9000); err != nil {
		t.Fatal(err)
	}
	if items, _ := s.ListFavorites(ctx, 0, 0, 10); len(items) != 1 {
		t.Fatalf("a deleted message leaves the favorites: %+v", items)
	}
	if counts, _ := s.ListTags(ctx); len(counts) != 1 || counts[0].Count != 0 {
		t.Fatalf("tag counts after delete: %+v", counts)
	}

	if chat, err := s.Unfavorite(ctx, a.MessageID); err != nil || chat != a.ChatID {
		t.Fatalf("Unfavorite = %d %v", chat, err)
	}
	if _, err := s.Unfavorite(ctx, a.MessageID); err != nil {
		t.Fatalf("Unfavorite is idempotent: %v", err)
	}
	if _, err := s.Unfavorite(ctx, 9999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Unfavorite missing: %v", err)
	}
}
