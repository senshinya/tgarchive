package transcode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"tgarchive/internal/store"
)

type record struct{ state, codec, path, err string }

type fakeStore struct {
	jobs    []store.CompatJob
	set     map[int64]record
	gone    map[int64]bool
	nextErr error
	reset   int
}

func (f *fakeStore) NextCompatJob(ctx context.Context) (*store.CompatJob, error) {
	if f.nextErr != nil {
		return nil, f.nextErr
	}
	for _, j := range f.jobs {
		if _, done := f.set[j.ID]; !done && !f.gone[j.ID] {
			return &j, nil
		}
	}
	return nil, store.ErrNotFound
}

func (f *fakeStore) SetCompat(ctx context.Context, id int64, state, codec, path, errMsg string) (bool, error) {
	if f.gone[id] {
		return false, nil
	}
	f.set[id] = record{state, codec, path, errMsg}
	return true, nil
}

func (f *fakeStore) ResetFailedCompat(ctx context.Context) error { f.reset++; return nil }

type fakeMedia struct {
	streams map[string]Streams
	convErr error
	onConv  func()
}

func (m *fakeMedia) Probe(ctx context.Context, path string) (Streams, error) {
	s, ok := m.streams[filepath.Base(path)]
	if !ok {
		return Streams{}, errors.New("invalid data")
	}
	return s, nil
}

func (m *fakeMedia) Convert(ctx context.Context, src, dst string) error {
	if m.onConv != nil {
		m.onConv()
	}
	if m.convErr != nil {
		os.WriteFile(dst, []byte("partial"), 0o644)
		return m.convErr
	}
	return os.WriteFile(dst, []byte("h264"), 0o644)
}

func setup(t *testing.T, jobs ...store.CompatJob) (*fakeStore, *fakeMedia, *Worker, string, *[]int64) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "1"), 0o755)
	st := &fakeStore{jobs: jobs, set: map[int64]record{}, gone: map[int64]bool{}}
	md := &fakeMedia{streams: map[string]Streams{
		"a.mp4": {"av1", "aac", mp4}, "h.mp4": {"h264", "aac", mp4}, "m.mov": {"mpeg4", "mp3", mp4}, "d.mp4": {"hevc", "ac3", mp4},
	}}
	var done []int64
	w := NewWorker(st, md, dir, func(id int64) { done = append(done, id) })
	return st, md, w, dir, &done
}

func TestWorkerConvertsOnlyWhatBrowsersCannotPlay(t *testing.T) {
	st, _, w, dir, done := setup(t, store.CompatJob{ID: 4, Path: "1/d.mp4"},
		store.CompatJob{ID: 3, Path: "1/a.mp4"}, store.CompatJob{ID: 2, Path: "1/h.mp4"}, store.CompatJob{ID: 1, Path: "1/m.mov"})
	ctx := context.Background()
	for w.Step(ctx) {
	}
	if r := st.set[3]; r != (record{store.CompatDone, "av1", "1/a.compat.mp4", ""}) {
		t.Fatalf("av1 = %+v", r)
	}
	if r := st.set[2]; r != (record{store.CompatNone, "h264", "", ""}) {
		t.Fatalf("h264 = %+v", r)
	}
	if r := st.set[1]; r != (record{store.CompatDone, "mpeg4", "1/m.compat.mp4", ""}) {
		t.Fatalf("mpeg4 = %+v", r)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "1", "a.compat.mp4")); err != nil || string(b) != "h264" {
		t.Fatalf("copy = %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "1", "a.compat.mp4.part")); err == nil {
		t.Fatal("part file must be renamed away")
	}
	if r := st.set[4]; r.state != store.CompatDone || r.codec == "hevc" {
		t.Fatalf("hevc with ac3 must always play the copy, got %+v", r)
	}
	if len(*done) != 3 {
		t.Fatalf("onDone calls = %v", *done)
	}
}

func TestWorkerRecordsFailures(t *testing.T) {
	st, md, w, dir, done := setup(t, store.CompatJob{ID: 1, Path: "1/broken.mp4"}, store.CompatJob{ID: 2, Path: "1/a.mp4"})
	md.convErr = errors.New("encoder exploded")
	ctx := context.Background()
	for w.Step(ctx) {
	}
	if r := st.set[1]; r.state != store.CompatFailed || r.err != "invalid data" {
		t.Fatalf("unprobeable = %+v", r)
	}
	if r := st.set[2]; r.state != store.CompatFailed || r.codec != "av1" || r.err != "encoder exploded" {
		t.Fatalf("failed conversion = %+v", r)
	}
	if _, err := os.Stat(filepath.Join(dir, "1", "a.compat.mp4.part")); err == nil {
		t.Fatal("partial output must be removed")
	}
	if len(*done) != 0 {
		t.Fatalf("onDone calls = %v", *done)
	}
}

func TestWorkerDropsCopyOfDeletedMedia(t *testing.T) {
	st, md, w, dir, done := setup(t, store.CompatJob{ID: 1, Path: "1/a.mp4"})
	md.onConv = func() { st.gone[1] = true }
	w.Step(context.Background())
	if _, err := os.Stat(filepath.Join(dir, "1", "a.compat.mp4")); err == nil {
		t.Fatal("copy of a deleted media must be removed")
	}
	if len(*done) != 0 {
		t.Fatalf("onDone calls = %v", *done)
	}
}

func TestWorkerLeavesJobUncheckedOnShutdown(t *testing.T) {
	st, md, w, _, _ := setup(t, store.CompatJob{ID: 1, Path: "1/a.mp4"})
	ctx, cancel := context.WithCancel(context.Background())
	md.onConv = cancel
	md.convErr = context.Canceled
	if w.Step(ctx) {
		t.Fatal("Step must stop on shutdown")
	}
	if _, ok := st.set[1]; ok {
		t.Fatalf("job must stay unchecked, got %+v", st.set[1])
	}
}

func TestWorkerBacksOffOnStoreErrors(t *testing.T) {
	st, _, w, _, _ := setup(t)
	st.nextErr = errors.New("database is locked")
	if w.Step(context.Background()) {
		t.Fatal("Step must report no progress on a store error")
	}
}

func TestRunResetsFailuresAndStops(t *testing.T) {
	st, _, w, _, _ := setup(t, store.CompatJob{ID: 1, Path: "1/h.mp4"})
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { w.Run(ctx); close(stopped) }()
	w.Wake()
	cancel()
	<-stopped
	if st.reset != 1 {
		t.Fatalf("ResetFailedCompat calls = %d", st.reset)
	}
}
