package botapifs

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, p string, mtime time.Time) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(p, mtime, mtime)
}

func TestMap(t *testing.T) {
	m := Mapper{Remote: "/var/lib/telegram-bot-api", Local: "/data/botapi"}
	got, err := m.Map("/var/lib/telegram-bot-api/1:tok/photos/file_0.jpg")
	if err != nil || got != "/data/botapi/1:tok/photos/file_0.jpg" {
		t.Fatalf("Map = %q, %v", got, err)
	}
	for _, bad := range []string{"/etc/passwd", "/var/lib/telegram-bot-api/../x", "/var/lib/telegram-bot-api", "relative/x"} {
		if _, err := m.Map(bad); err == nil {
			t.Fatalf("Map(%q) must fail", bad)
		}
	}
}

func TestCleanOlderThanKeepsServerState(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	old := now.Add(-48 * time.Hour)
	write(t, filepath.Join(root, "1:tok", "td.binlog"), old)
	write(t, filepath.Join(root, "1:tok", "photos", "old.jpg"), old)
	write(t, filepath.Join(root, "1:tok", "photos", "new.jpg"), now)
	n, err := Mapper{Local: root}.CleanOlderThan(24*time.Hour, now)
	if err != nil || n != 1 {
		t.Fatalf("removed %d, %v", n, err)
	}
	for p, want := range map[string]bool{"1:tok/td.binlog": true, "1:tok/photos/old.jpg": false, "1:tok/photos/new.jpg": true} {
		_, err := os.Stat(filepath.Join(root, p))
		if (err == nil) != want {
			t.Fatalf("%s exists=%v, want %v", p, err == nil, want)
		}
	}
	if n, err := (Mapper{Local: filepath.Join(root, "missing")}).CleanOlderThan(time.Hour, now); n != 0 || err != nil {
		t.Fatalf("missing dir = %d %v", n, err)
	}
}

func TestLinkOrCopy(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "a"), filepath.Join(dir, "sub", "b")
	os.WriteFile(src, []byte("data"), 0o644)
	os.MkdirAll(filepath.Dir(dst), 0o755)
	os.WriteFile(dst, []byte("stale"), 0o644)
	if err := LinkOrCopy(src, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "data" {
		t.Fatalf("dst = %q", b)
	}
}
