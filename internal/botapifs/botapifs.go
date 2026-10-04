// Package botapifs handles the directory shared with the local Bot API server.
package botapifs

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// tokenSegment matches a bot token ("<bot_id>:<secret>"), which the local Bot API server
// uses as a directory name in every file path it returns.
var tokenSegment = regexp.MustCompile(`\d+:[A-Za-z0-9_-]{30,}`)

// RedactPath replaces every bot token in s with "<bot>".
func RedactPath(s string) string {
	return tokenSegment.ReplaceAllString(s, "<bot>")
}

type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.err }

// redactErr keeps errors.Is/As working while its message never carries a bot token.
func redactErr(err error) error {
	if err == nil {
		return nil
	}
	return &redactedError{msg: RedactPath(err.Error()), err: err}
}

// Mapper translates paths returned by getFile (inside the Bot API container)
// into paths inside this container.
type Mapper struct {
	Remote string
	Local  string
}

func (m Mapper) Map(remote string) (string, error) {
	if !filepath.IsAbs(remote) {
		return "", fmt.Errorf("bot api file path %q is not absolute", RedactPath(remote))
	}
	rel, err := filepath.Rel(m.Remote, filepath.Clean(remote))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("bot api file path %q is outside %s", RedactPath(remote), RedactPath(m.Remote))
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
	var rmErrs []error
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
		if now.Sub(info.ModTime()) > age {
			if rmErr := os.Remove(p); rmErr != nil {
				if !errors.Is(rmErr, fs.ErrNotExist) {
					log.Printf("botapifs: remove %s: %v", RedactPath(p), RedactPath(rmErr.Error()))
					rmErrs = append(rmErrs, rmErr)
				}
			} else {
				n++
			}
		}
		return nil
	})
	if err != nil {
		return n, redactErr(err)
	}
	if len(rmErrs) > 0 {
		return n, redactErr(errors.Join(rmErrs...))
	}
	return n, nil
}

// archiveMode is the mode of every archived file. The local Bot API server creates downloads 0600
// and a hard link shares that inode, so it is widened here: the NAS pull reads media/ through
// OpenList, which runs as a different UID.
const archiveMode fs.FileMode = 0o644

// LinkOrCopy places src at dst, replacing dst. A hard link is tried first (same filesystem),
// falling back to copy-then-rename so dst is never observed half-written. dst ends up archiveMode.
func LinkOrCopy(src, dst string) error {
	return redactErr(linkOrCopy(src, dst))
}

func linkOrCopy(src, dst string) error {
	if err := os.Remove(dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Link(src, dst); err == nil {
		return os.Chmod(dst, archiveMode)
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".part"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, archiveMode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Chmod(archiveMode); err != nil { // OpenFile's mode is filtered by the umask
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
