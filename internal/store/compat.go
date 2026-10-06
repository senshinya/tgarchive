package store

import (
	"context"
	"database/sql"
	"errors"
)

// Compat states of a video's browser-playable copy (media.compat_state).
const (
	CompatUnchecked = ""
	CompatNone      = "none"
	CompatDone      = "done"
	CompatFailed    = "failed"
)

// CompatJob is a downloaded video not yet checked for a browser-playable copy.
type CompatJob struct {
	ID   int64
	Path string
}

// NextCompatJob returns the newest downloaded video still in use whose codecs have not been
// checked, or ErrNotFound.
func (s *Store) NextCompatJob(ctx context.Context) (*CompatJob, error) {
	var j CompatJob
	err := s.db.QueryRowContext(ctx, `SELECT id, path FROM media
		WHERE state = ? AND compat_state = '' AND path != '' AND kind IN ('video', 'animation', 'video_note')
			AND EXISTS (SELECT 1 FROM message_media mm WHERE mm.media_id = media.id)
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

// ResetFailedCompat makes failed conversions eligible again, so a fix in a new release (or a
// transient failure such as a full disk) gets another try after a restart.
func (s *Store) ResetFailedCompat(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE media SET compat_state = '', compat_error = '' WHERE compat_state = ?`, CompatFailed)
	return err
}
