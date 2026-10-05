package downloader

import (
	"bytes"
	"context"
	"testing"
	"time"
)

func TestTrackerLifecycleAndSpeed(t *testing.T) {
	tr := NewTracker()
	t0 := time.Unix(1000, 0)
	tr.start(2, 100, t0.Add(time.Second))
	tr.start(1, 0, t0)
	if items, speed := tr.Tick(t0); len(items) != 2 || items[0].MediaID != 1 || speed != 0 {
		t.Fatalf("start = %+v %d", items, speed)
	}
	tr.update(1, 400, 1000)
	tr.update(2, 100, 0)
	tr.update(9, 50, 50) // unknown id: ignored
	items, speed := tr.Tick(t0.Add(time.Second))
	if items[0].Done != 400 || items[0].Total != 1000 || items[1].Total != 100 || speed != 500 {
		t.Fatalf("after update = %+v speed %d", items, speed)
	}
	tr.finish(2)
	// Bytes of a finished download still count toward the window's speed.
	if items, speed := tr.Tick(t0.Add(2 * time.Second)); len(items) != 1 || speed != 250 {
		t.Fatalf("after finish = %+v speed %d", items, speed)
	}
	// Old samples fall out of the window.
	if _, speed := tr.Tick(t0.Add(20 * time.Second)); speed != 0 {
		t.Fatalf("idle speed = %d", speed)
	}
	if tr.Speed() != 0 || len(tr.Snapshot()) != 1 {
		t.Fatal("Speed/Snapshot disagree with Tick")
	}
}

func TestTrackerIgnoresGoingBackwards(t *testing.T) {
	tr := NewTracker()
	t0 := time.Unix(1000, 0)
	tr.start(1, 0, t0)
	tr.Tick(t0)
	tr.update(1, 500, 0)
	tr.update(1, 0, 0) // a source restarting its count must not subtract from the speed
	tr.update(1, 200, 0)
	if _, speed := tr.Tick(t0.Add(time.Second)); speed != 700 {
		t.Fatalf("speed = %d", speed)
	}
}

func TestCountingWriterReports(t *testing.T) {
	var got [][2]int64
	c := WithProgress(context.Background(), func(done, total int64) { got = append(got, [2]int64{done, total}) })
	var buf bytes.Buffer
	w := CountingWriter(c, &buf, 10)
	w.Write([]byte("abc"))
	w.Write([]byte("defg"))
	if buf.String() != "abcdefg" || len(got) != 2 || got[1] != [2]int64{7, 10} {
		t.Fatalf("buf %q reports %v", buf.String(), got)
	}
	// Without a progress function it is a plain writer.
	CountingWriter(context.Background(), &buf, 0).Write([]byte("h"))
	Report(context.Background(), 1, 1)
}
