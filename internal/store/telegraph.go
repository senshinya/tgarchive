package store

import (
	"context"
	"database/sql"
	"errors"
)

const (
	TelegraphQueued   = "queued"
	TelegraphFetching = "fetching"
	TelegraphFetched  = "fetched"
	TelegraphFailed   = "failed"
)

// TelegraphJob archives the Telegraph article a link message points to (one job per message).
type TelegraphJob struct {
	ID        int64
	MessageID int64
	ChatID    int64
	Path      string
	State     string
	Attempts  int
	Error     string
	CreatedAt int64
	UpdatedAt int64
}

const telegraphJobCols = "j.id, j.message_id, m.chat_id, j.path, j.state, j.attempts, j.error, j.created_at, j.updated_at"

func scanTelegraphJob(r scanner) (*TelegraphJob, error) {
	var j TelegraphJob
	err := r.Scan(&j.ID, &j.MessageID, &j.ChatID, &j.Path, &j.State, &j.Attempts, &j.Error, &j.CreatedAt, &j.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// GetTelegraphJob returns the job of a non-deleted link message.
func (s *Store) GetTelegraphJob(ctx context.Context, messageID int64) (*TelegraphJob, error) {
	return scanTelegraphJob(s.db.QueryRowContext(ctx, `SELECT `+telegraphJobCols+`
		FROM telegraph_jobs j JOIN messages m ON m.id = j.message_id AND m.deleted_at = 0 WHERE j.message_id = ?`, messageID))
}

// ClaimNextTelegraphJob moves the oldest queued job to 'fetching' and returns it (ErrNotFound when idle).
func (s *Store) ClaimNextTelegraphJob(ctx context.Context, now int64) (*TelegraphJob, error) {
	var job *TelegraphJob
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		j, err := scanTelegraphJob(tx.QueryRowContext(ctx, `SELECT `+telegraphJobCols+`
			FROM telegraph_jobs j JOIN messages m ON m.id = j.message_id AND m.deleted_at = 0
			WHERE j.state = 'queued' ORDER BY j.id LIMIT 1`))
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE telegraph_jobs SET state = 'fetching', updated_at = ? WHERE id = ?", now, j.ID); err != nil {
			return err
		}
		j.State, j.UpdatedAt = TelegraphFetching, now
		job = j
		return nil
	})
	return job, err
}

// RequeueFetchingTelegraphJobs puts jobs interrupted by a restart back into the queue.
func (s *Store) RequeueFetchingTelegraphJobs(ctx context.Context, now int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, "UPDATE telegraph_jobs SET state = 'queued', updated_at = ? WHERE state = 'fetching'", now)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SetTelegraphJobAttempts records a transient failure; the job stays 'fetching'.
func (s *Store) SetTelegraphJobAttempts(ctx context.Context, id int64, attempts int, errMsg string, now int64) error {
	return affected(s.db.ExecContext(ctx, "UPDATE telegraph_jobs SET attempts = ?, error = ?, updated_at = ? WHERE id = ?", attempts, errMsg, now, id))
}

// FinishTelegraphJob sets a final state; ErrNotFound when the job is gone (message deleted).
func (s *Store) FinishTelegraphJob(ctx context.Context, id int64, state, errMsg string, now int64) error {
	return affected(s.db.ExecContext(ctx, "UPDATE telegraph_jobs SET state = ?, error = ?, updated_at = ? WHERE id = ?", state, errMsg, now, id))
}
