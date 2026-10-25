package videocover

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"tgarchive/internal/store"
)

type fakeStore struct {
	mu         sync.Mutex
	jobs       []store.VideoCoverJob
	installed  map[int64]store.VideoCover
	reject     bool
	installErr error
}

func (s *fakeStore) NextVideoCoverJob(ctx context.Context, after int64) (*store.VideoCoverJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.ID > after {
			if _, ok := s.installed[j.ID]; !ok {
				return &j, nil
			}
		}
	}
	return nil, store.ErrNotFound
}
func (s *fakeStore) InstallVideoCover(ctx context.Context, job store.VideoCoverJob, cover store.VideoCover) (int64, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.installErr != nil {
		return 0, false, s.installErr
	}
	if s.reject {
		return 0, false, nil
	}
	s.installed[job.ID] = cover
	return 100 + job.ID, true, nil
}
func writeJPEG(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return jpeg.Encode(f, image.NewRGBA(image.Rect(0, 0, 640, 360)), nil)
}
func setup(t *testing.T, jobs ...store.VideoCoverJob) (*fakeStore, *Worker, string, *[]int64) {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "1"), 0755)
	for _, j := range jobs {
		if filepath.IsLocal(j.Path) {
			os.WriteFile(filepath.Join(dir, j.Path), []byte("original"), 0644)
		}
	}
	st := &fakeStore{jobs: jobs, installed: map[int64]store.VideoCover{}}
	var done []int64
	w := newWorker(st, "ffmpeg", dir, func(id int64) { done = append(done, id) })
	w.run = func(ctx context.Context, name string, args ...string) error { return writeJPEG(args[len(args)-1]) }
	return st, w, dir, &done
}
func TestWorkerCommandAtomicOutputAndOriginalPreserved(t *testing.T) {
	job := store.VideoCoverJob{ID: 1, DedupeKey: "bot:video", Path: "1/video.mp4", Duration: 400}
	st, w, dir, done := setup(t, job)
	var args []string
	w.run = func(ctx context.Context, name string, a ...string) error {
		args = append([]string{}, a...)
		if name != "ffmpeg" {
			t.Fatalf("binary %q", name)
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 30*time.Second || time.Until(deadline) < 29*time.Second {
			t.Fatal("missing 30s timeout")
		}
		if _, err := os.Stat(filepath.Join(dir, store.VideoCoverRel(job.DedupeKey))); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("destination visible before atomic install: %v", err)
		}
		return writeJPEG(a[len(a)-1])
	}
	if !w.Step(context.Background()) {
		t.Fatal("job not processed")
	}
	resolvedSource, _ := filepath.EvalSymlinks(filepath.Join(dir, job.Path))
	expected := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-threads", "1", "-ss", "30", "-protocol_whitelist", "file", "-i", resolvedSource, "-map", "0:v:0", "-frames:v", "1", "-vf", "scale=640:360:force_original_aspect_ratio=decrease", "-c:v", "mjpeg", "-q:v", "3", "-threads", "1", "-f", "image2", "-update", "1"}
	if len(args) != len(expected)+1 || !reflect.DeepEqual(args[:len(expected)], expected) {
		t.Fatalf("args=%v", args)
	}
	cover, ok := st.installed[1]
	if !ok || cover.Path != store.VideoCoverRel(job.DedupeKey) || cover.Width != 640 || cover.Height != 360 || cover.Size == 0 {
		t.Fatal(cover)
	}
	if b, err := os.ReadFile(filepath.Join(dir, job.Path)); err != nil || string(b) != "original" {
		t.Fatal(string(b), err)
	}
	if len(*done) != 1 || (*done)[0] != 101 {
		t.Fatal(*done)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "covers", ".video-cover-*"))
	if len(files) != 0 {
		t.Fatalf("temporary files %v", files)
	}
}
func TestWorkerFailureBackoffDoesNotBlockLaterJobs(t *testing.T) {
	jobs := []store.VideoCoverJob{{ID: 1, DedupeKey: "bad", Path: "1/bad.mp4"}, {ID: 2, DedupeKey: "good", Path: "1/good.mp4"}}
	st, w, _, _ := setup(t, jobs...)
	resolved, _ := filepath.EvalSymlinks(filepath.Join(w.mediaDir, "1/bad.mp4"))
	now := time.Unix(100, 0)
	w.now = func() time.Time { return now }
	calls := map[string]int{}
	w.run = func(ctx context.Context, name string, args ...string) error {
		src := args[11]
		calls[src]++
		if strings.HasSuffix(src, "bad.mp4") {
			return errors.New("broken video")
		}
		return writeJPEG(args[len(args)-1])
	}
	for i := 0; i < 4 && w.Step(context.Background()); i++ {
	}
	if _, ok := st.installed[2]; !ok {
		t.Fatal("failed first job blocked second")
	}
	// A new sweep sees the failure again, but does not run ffmpeg during the hour backoff.
	for i := 0; i < 3 && w.Step(context.Background()); i++ {
	}
	if calls[resolved] != 1 {
		t.Fatal(calls)
	}
	now = now.Add(time.Hour)
	for i := 0; i < 3 && w.Step(context.Background()); i++ {
	}
	if calls[resolved] != 2 {
		t.Fatal(calls)
	}
}
func TestWorkerRejectsUnsafePathsAndInvalidJPEG(t *testing.T) {
	for _, path := range []string{"../outside.mp4", "/tmp/outside.mp4", "1/a.mp4"} {
		t.Run(path, func(t *testing.T) {
			st, w, dir, _ := setup(t, store.VideoCoverJob{ID: 1, DedupeKey: "bad", Path: path})
			calls := 0
			w.run = func(ctx context.Context, name string, args ...string) error {
				calls++
				return os.WriteFile(args[len(args)-1], []byte("not JPEG"), 0644)
			}
			w.Step(context.Background())
			if len(st.installed) != 0 {
				t.Fatal(st.installed)
			}
			if path != "1/a.mp4" && calls != 0 {
				t.Fatal("unsafe file sent to ffmpeg")
			}
			matches, _ := filepath.Glob(filepath.Join(dir, "covers", "*"))
			if len(matches) != 0 {
				t.Fatalf("invalid output retained %v", matches)
			}
		})
	}
}
func TestWorkerTimeoutCancellationAndStaleInstallCleanFiles(t *testing.T) {
	for _, mode := range []string{"timeout", "cancel", "deleted", "db_error"} {
		t.Run(mode, func(t *testing.T) {
			st, w, dir, done := setup(t, store.VideoCoverJob{ID: 1, DedupeKey: "video", Path: "1/a.mp4"})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "timeout":
				w.timeout = time.Millisecond
				w.run = func(ctx context.Context, name string, args ...string) error {
					os.WriteFile(args[len(args)-1], []byte("partial"), 0644)
					<-ctx.Done()
					return ctx.Err()
				}
			case "cancel":
				w.run = func(ctx context.Context, name string, args ...string) error {
					os.WriteFile(args[len(args)-1], []byte("partial"), 0644)
					cancel()
					<-ctx.Done()
					return ctx.Err()
				}
			case "deleted":
				st.reject = true
			case "db_error":
				st.installErr = errors.New("database failed")
			}
			w.Step(ctx)
			if len(st.installed) != 0 || len(*done) != 0 {
				t.Fatal(st.installed, *done)
			}
			files, _ := filepath.Glob(filepath.Join(dir, "covers", "*"))
			if len(files) != 0 {
				t.Fatalf("files retained %v", files)
			}
		})
	}
}
func TestWorkerReusesSharedCoverWithoutFFmpeg(t *testing.T) {
	job := store.VideoCoverJob{ID: 1, DedupeKey: "shared", Path: "1/shared.mp4"}
	job.CoverPath = store.VideoCoverRel(job.DedupeKey)
	st, w, dir, _ := setup(t, job)
	os.MkdirAll(filepath.Join(dir, "covers"), 0755)
	writeJPEG(filepath.Join(dir, job.CoverPath))
	w.run = func(context.Context, string, ...string) error {
		t.Fatal("shared generated cover must be reused")
		return nil
	}
	if !w.Step(context.Background()) {
		t.Fatal("reuse not processed")
	}
	if len(st.installed) != 1 {
		t.Fatal(st.installed)
	}
	// A native thumbnail won the race on a later reference: preserve the still-shared file.
	st.installed = map[int64]store.VideoCover{}
	st.reject = true
	w.after = 0
	w.Step(context.Background())
	if _, err := os.Stat(filepath.Join(dir, job.CoverPath)); err != nil {
		t.Fatal(err)
	}
}
func TestWorkerRunScansAtStartupAndStops(t *testing.T) {
	_, w, _, _ := setup(t, store.VideoCoverJob{ID: 1, DedupeKey: "startup", Path: "1/a.mp4"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	w.onDone = func(int64) { cancel() }
	go func() { w.Run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("startup scan or shutdown stalled")
	}
}
func TestNewWorkerDisablesOnlyWhenFFmpegUnavailable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if NewWorker(&fakeStore{}, t.TempDir(), nil) != nil {
		t.Fatal("worker must disable without ffmpeg")
	}
}
func TestFFmpegIntegration(t *testing.T) {
	binary, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	job := store.VideoCoverJob{ID: 1, DedupeKey: "integration", Path: "1/tiny.mp4", Duration: 1}
	st, w, dir, _ := setup(t, job)
	cmd := exec.Command(binary, "-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "color=c=blue:s=320x180:d=1", "-c:v", "mpeg4", "-threads", "1", filepath.Join(dir, job.Path))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture video: %s %v", output, err)
	}
	w = newWorker(st, binary, dir, nil)
	if !w.Step(context.Background()) {
		t.Fatal("integration not processed")
	}
	if len(st.installed) != 1 {
		t.Fatal(st.installed)
	}
}

func TestWorkerRejectsSymlinkEscapes(t *testing.T) {
	for _, target := range []string{"input", "covers"} {
		t.Run(target, func(t *testing.T) {
			st, w, dir, _ := setup(t, store.VideoCoverJob{ID: 1, DedupeKey: "symlink", Path: "1/a.mp4"})
			outside := t.TempDir()
			if target == "input" {
				os.WriteFile(filepath.Join(outside, "secret.mp4"), []byte("secret"), 0644)
				os.Remove(filepath.Join(dir, "1/a.mp4"))
				if err := os.Symlink(filepath.Join(outside, "secret.mp4"), filepath.Join(dir, "1/a.mp4")); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Symlink(outside, filepath.Join(dir, "covers")); err != nil {
					t.Fatal(err)
				}
			}
			w.run = func(context.Context, string, ...string) error { t.Fatal("escaping symlink sent to ffmpeg"); return nil }
			w.Step(context.Background())
			if len(st.installed) != 0 {
				t.Fatal(st.installed)
			}
			files, err := os.ReadDir(outside)
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if target == "input" {
				want = 1
			}
			if len(files) != want {
				t.Fatalf("external directory changed: %v", files)
			}
		})
	}
}

func TestWorkerWakeScansNewJobsAndStops(t *testing.T) {
	st, w, dir, _ := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	finished := make(chan struct{})
	w.onDone = func(int64) { cancel() }
	go func() { close(started); w.Run(ctx); close(finished) }()
	<-started
	os.WriteFile(filepath.Join(dir, "1/new.mp4"), []byte("original"), 0644)
	st.mu.Lock()
	st.jobs = append(st.jobs, store.VideoCoverJob{ID: 1, DedupeKey: "wake", Path: "1/new.mp4"})
	st.mu.Unlock()
	w.Wake()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("wake or shutdown stalled")
	}
}

func TestWorkerRejectsTruncatedJPEGAndRegeneratesExisting(t *testing.T) {
	truncated, err := hex.DecodeString("ffd8ffe000104a46494600010100000100010000ffc00011080168028003012200021101031101")
	if err != nil {
		t.Fatal(err)
	}
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprint(existing), func(t *testing.T) {
			job := store.VideoCoverJob{ID: 1, DedupeKey: "truncated", Path: "1/a.mp4"}
			if existing {
				job.CoverPath = store.VideoCoverRel(job.DedupeKey)
			}
			st, w, dir, _ := setup(t, job)
			calls := 0
			if existing {
				os.MkdirAll(filepath.Join(dir, "covers"), 0755)
				if err := os.WriteFile(filepath.Join(dir, job.CoverPath), truncated, 0644); err != nil {
					t.Fatal(err)
				}
			}
			w.run = func(ctx context.Context, name string, args ...string) error {
				calls++
				if existing {
					return writeJPEG(args[len(args)-1])
				}
				return os.WriteFile(args[len(args)-1], truncated, 0644)
			}
			w.Step(context.Background())
			if calls != 1 {
				t.Fatalf("ffmpeg called %d times: truncated existing JPEG must be regenerated", calls)
			}
			if existing {
				cover, ok := st.installed[1]
				if !ok || cover.Size <= int64(len(truncated)) {
					t.Fatalf("bad existing file installed as done: %+v", st.installed)
				}
				f, err := os.Open(filepath.Join(dir, cover.Path))
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				if _, err := jpeg.Decode(f); err != nil {
					t.Fatalf("regenerated file not decodable: %v", err)
				}
			} else {
				if len(st.installed) != 0 {
					t.Fatal("truncated new JPEG was installed as done")
				}
				files, _ := filepath.Glob(filepath.Join(dir, "covers", "*"))
				if len(files) != 0 {
					t.Fatalf("truncated new output was not cleaned: %v", files)
				}
			}
		})
	}
}
