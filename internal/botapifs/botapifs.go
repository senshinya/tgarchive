// Package botapifs handles the directory shared with the local Bot API server.
package botapifs

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Mapper translates paths returned by getFile (inside the Bot API container)
// into paths inside this container.
type Mapper struct {
	Remote string
	Local  string
}

func (m Mapper) Map(remote string) (string, error) {
	if !filepath.IsAbs(remote) {
		return "", fmt.Errorf("bot api file path %q is not absolute", remote)
	}
	rel, err := filepath.Rel(m.Remote, filepath.Clean(remote))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("bot api file path %q is outside %s", remote, m.Remote)
	}
	return filepath.Join(m.Local, rel), nil
}

// CleanOlderThan removes downloaded files older than age. Only files at depth >= 3
// (<bot>/<category>/<file>) are touched; the server's own state such as <bot>/td.binlog is kept.
func (m Mapper) CleanOlderThan(age time.Duration, now time.Time) (int, error) {
	if _, err := os.Stat(m.Local); errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	n := 0
	err := filepath.WalkDir(m.Local, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(m.Local, p)
		if err != nil || len(strings.Split(rel, string(filepath.Separator))) < 3 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if now.Sub(info.ModTime()) > age && os.Remove(p) == nil {
			n++
		}
		return nil
	})
	return n, err
}

// LinkOrCopy places src at dst, replacing dst. A hard link is tried first (same filesystem),
// falling back to copy-then-rename so dst is never observed half-written.
func LinkOrCopy(src, dst string) error {
	if err := os.Remove(dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
