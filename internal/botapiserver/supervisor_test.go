package botapiserver

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"tgarchive/internal/tgapp"
)

func TestMain(m *testing.M) {
	if os.Getenv("FAKE_BOTAPI") == "1" {
		f, _ := os.OpenFile(os.Getenv("FAKE_RECORD"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		fmt.Fprintf(f, "%s %s %s token=%q\n", os.Getenv("TELEGRAM_API_ID"), os.Getenv("TELEGRAM_API_HASH"), strings.Join(os.Args[1:], " "), os.Getenv("TOKEN_ENC_KEY"))
		f.Close()
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM)
		if ms, err := strconv.Atoi(os.Getenv("FAKE_EXIT_AFTER_MS")); err == nil {
			select {
			case <-sig:
				os.Exit(0)
			case <-time.After(time.Duration(ms) * time.Millisecond):
				os.Exit(1)
			}
		}
		<-sig
		os.Exit(0)
	}
	os.Exit(m.Run())
}

const hash1 = "11111111111111111111111111111111"
const hash2 = "22222222222222222222222222222222"

func newSup(t *testing.T, extra ...string) (*Supervisor, string) {
	t.Helper()
	dir := t.TempDir()
	record := filepath.Join(dir, "record.txt")
	s := New(os.Args[0], filepath.Join(dir, "botapi"), filepath.Join(dir, "tmp"), 18081)
	s.Env = append([]string{"FAKE_BOTAPI=1", "FAKE_RECORD=" + record}, extra...)
	s.MinBackoff, s.MaxBackoff = 10*time.Millisecond, 50*time.Millisecond
	return s, record
}

func lines(path string) []string {
	b, _ := os.ReadFile(path)
	s := strings.TrimSpace(string(b))
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStartsOnApplyAndRestartsOnChange(t *testing.T) {
	t.Setenv("TOKEN_ENC_KEY", "must-not-leak")
	s, record := newSup(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx, nil); close(done) }()

	eventually(t, "unconfigured", func() bool { st, _ := s.Status(); return st == StateUnconfigured })
	if len(lines(record)) != 0 {
		t.Fatal("must not start without credentials")
	}

	s.Apply(tgapp.Credentials{APIID: 1, APIHash: hash1})
	eventually(t, "first start", func() bool { return len(lines(record)) == 1 })
	first := lines(record)[0]
	for _, want := range []string{"1 " + hash1, "--local", "--http-ip-address=127.0.0.1", "--http-port=18081", "--dir=", "--temp-dir=", `token=""`} {
		if !strings.Contains(first, want) {
			t.Fatalf("first start %q lacks %q", first, want)
		}
	}
	eventually(t, "running", func() bool { st, _ := s.Status(); return st == StateRunning })

	s.Apply(tgapp.Credentials{APIID: 2, APIHash: hash2})
	eventually(t, "restart with new credentials", func() bool {
		l := lines(record)
		return len(l) == 2 && strings.HasPrefix(l[1], "2 "+hash2)
	})

	cancel()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestRestartsAfterCrash(t *testing.T) {
	s, record := newSup(t, "FAKE_EXIT_AFTER_MS=20")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx, &tgapp.Credentials{APIID: 1, APIHash: hash1})
	eventually(t, "three starts", func() bool { return len(lines(record)) >= 3 })
	if _, lastErr := s.Status(); lastErr == "" {
		eventually(t, "crash recorded", func() bool { _, e := s.Status(); return e != "" })
	}
}
