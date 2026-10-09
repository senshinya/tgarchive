package store

import "testing"

func TestDownloadQueries(t *testing.T) {
	s := newStore(t)
	bot := seedBot(t, s, 777)
	first := ingest(t, s, bot, photoMsg(1, "bot:a"))
	a := mediaIDs(t, s, first.MessageID)[0]
	// The same file sent again: one media row, now used by two messages.
	again := ingest(t, s, bot, photoMsg(2, "bot:a"))
	b := mediaIDs(t, s, ingest(t, s, bot, photoMsg(3, "bot:b")).MessageID)[0]
	c := mediaIDs(t, s, ingest(t, s, bot, photoMsg(4, "bot:c")).MessageID)[0]
	gone := ingest(t, s, bot, photoMsg(5, "bot:gone"))
	g := mediaIDs(t, s, gone.MessageID)[0]

	items, err := s.DownloadItems(ctx, []int64{a, b, 9999})
	if err != nil || len(items) != 2 {
		t.Fatalf("items = %+v, %v", items, err)
	}
	if it := items[a]; it.MessageID != again.MessageID || it.ChatID != first.ChatID || it.Kind != "photo" || it.Size != 4 {
		t.Fatalf("item a = %+v (want the newest message using it)", it)
	}

	if n, bytes, err := s.QueueStats(ctx, []int64{b}); err != nil || n != 3 || bytes != 12 {
		t.Fatalf("queue excluding b = %d %d %v", n, bytes, err)
	}
	if n, _, _ := s.QueueStats(ctx, nil); n != 4 {
		t.Fatalf("queue = %d", n)
	}

	s.MarkMediaFailed(ctx, b, mediaKey(t, s, b), 3, "HTTP 500")
	s.MarkMediaFailed(ctx, c, mediaKey(t, s, c), 3, "boom")
	s.MarkMediaFailed(ctx, g, mediaKey(t, s, g), 3, "x")
	s.DeleteMessage(ctx, gone.MessageID, 9) // its media is gone with it
	failed, err := s.FailedDownloads(ctx, 50)
	if err != nil || len(failed) != 2 || failed[0].MediaID != c || failed[1].Error != "HTTP 500" {
		t.Fatalf("failed = %+v, %v", failed, err)
	}
	if one, _ := s.FailedDownloads(ctx, 1); len(one) != 1 {
		t.Fatalf("limit ignored: %+v", one)
	}
	if n, _, _ := s.QueueStats(ctx, nil); n != 1 {
		t.Fatalf("failed media still queued: %d", n)
	}
}
