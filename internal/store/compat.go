package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"time"
)

// Compat states of a video's browser-playable copy (media.compat_state).
const (
	CompatUnchecked = ""
	CompatNone      = "none"
	CompatDone      = "done"
	CompatFailed    = "failed"
)

// CompatRel is where the browser-playable copy of the archive file at rel lives, and
// CompatPartRel the file it is written to first.
func CompatRel(rel string) string {
	return strings.TrimSuffix(rel, filepath.Ext(rel)) + ".compat.mp4"
}

func CompatPartRel(rel string) string { return CompatRel(rel) + ".part" }

// CompatJob is a downloaded video not yet checked for a browser-playable copy.
type CompatJob struct {
	ID   int64
	Path string
}

// Telegram can archive a movie as a document instead of a video message.
const compatMediaKinds = `(kind IN ('video','animation','video_note') OR (kind='document' AND mime LIKE 'video/%'))`

// NextCompatJob returns the newest downloaded video still in use whose codecs have not been
// checked, or ErrNotFound. Telegraph article videos are left alone: the reader plays originals.
func (s *Store) NextCompatJob(ctx context.Context) (*CompatJob, error) {
	var j CompatJob
	err := s.db.QueryRowContext(ctx, `SELECT id, path FROM media
		WHERE state = ? AND compat_state = '' AND path != '' AND `+compatMediaKinds+`
			AND EXISTS (SELECT 1 FROM message_media mm WHERE mm.media_id = media.id AND mm.role != 'article')
		ORDER BY id DESC LIMIT 1`, StateDone).Scan(&j.ID, &j.Path)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// SetCompat records the outcome of checking (and maybe converting) a video. It reports false
// when the media row is gone or no longer downloaded, so the caller can drop a file it made.
func (s *Store) SetCompat(ctx context.Context, id int64, state, codec, path, errMsg string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE media SET compat_state = ?, compat_codec = ?, compat_path = ?, compat_error = ?
		WHERE id = ? AND state = ?`, state, codec, path, errMsg, id, StateDone)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// PrepareCompatJobs retries failures and rechecks H.264 videos skipped by the old
// codec-only probe once. The marker and resets commit together; completed copies stay ready.
func (s *Store) PrepareCompatJobs(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE media SET compat_state = '', compat_error = '' WHERE compat_state = ?`, CompatFailed); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO settings (key,value,updated_at) VALUES ('compat_timing_v1',?,?) ON CONFLICT(key) DO NOTHING`, []byte("done"), time.Now().Unix())
	if err != nil {
		return err
	}
	inserted, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if inserted > 0 {
		if _, err = tx.ExecContext(ctx, `UPDATE media SET compat_state = '', compat_error = ''
   WHERE state = ? AND compat_state = ? AND compat_codec = 'h264'
    AND `+compatMediaKinds+` AND path != ''
    AND EXISTS (SELECT 1 FROM message_media mm WHERE mm.media_id=media.id AND mm.role != 'article')`, StateDone, CompatNone); err != nil {
			return err
		}
	}
	return tx.Commit()
}
