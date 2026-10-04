package store

import (
	"context"
	"database/sql"
	"errors"
)

const (
	JobQueued      = "queued"
	JobFetching    = "fetching"
	JobFetched     = "fetched"
	JobFailed      = "failed"
	JobUnsupported = "unsupported" // link not fetchable; the message was archived as text instead
)

type FetchJob struct {
	ID              int64
	BotID           int64
	SenderID        int64
	LinkTgMessageID int64
	Link            string
	State           string
	Error           string
	Receipt         string
	CreatedAt       int64
	UpdatedAt       int64
}

const jobCols = "id, bot_id, sender_id, link_tg_message_id, link, state, error, receipt, created_at, updated_at"

func scanJob(r scanner) (*FetchJob, error) {
	var j FetchJob
	err := r.Scan(&j.ID, &j.BotID, &j.SenderID, &j.LinkTgMessageID, &j.Link, &j.State, &j.Error, &j.Receipt, &j.CreatedAt, &j.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// CreateFetchJob inserts j unless a job for the same link message exists; created reports which.
func (s *Store) CreateFetchJob(ctx context.Context, j *FetchJob) (int64, bool, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO fetch_jobs (bot_id, sender_id, link_tg_message_id, link, state, error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (bot_id, sender_id, link_tg_message_id) DO NOTHING RETURNING id`,
		j.BotID, j.SenderID, j.LinkTgMessageID, j.Link, j.State, j.Error, j.CreatedAt, j.UpdatedAt).Scan(&id)
	if err == nil {
		return id, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	err = s.db.QueryRowContext(ctx, "SELECT id FROM fetch_jobs WHERE bot_id = ? AND sender_id = ? AND link_tg_message_id = ?",
		j.BotID, j.SenderID, j.LinkTgMessageID).Scan(&id)
	return id, false, err
}

func (s *Store) GetFetchJob(ctx context.Context, id int64) (*FetchJob, error) {
	return scanJob(s.db.QueryRowContext(ctx, "SELECT "+jobCols+" FROM fetch_jobs WHERE id = ?", id))
}

func (s *Store) ClaimNextFetchJob(ctx context.Context, now int64) (*FetchJob, error) {
	var job *FetchJob
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		j, err := scanJob(tx.QueryRowContext(ctx, "SELECT "+jobCols+" FROM fetch_jobs WHERE state = 'queued' ORDER BY id LIMIT 1"))
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE fetch_jobs SET state = 'fetching', updated_at = ? WHERE id = ?", now, j.ID); err != nil {
			return err
		}
		j.State, j.UpdatedAt = JobFetching, now
		job = j
		return nil
	})
	return job, err
}

func (s *Store) FinishFetchJob(ctx context.Context, id int64, state, errMsg string, now int64) error {
	return affected(s.db.ExecContext(ctx, "UPDATE fetch_jobs SET state = ?, error = ?, updated_at = ? WHERE id = ?", state, errMsg, now, id))
}

// RequeueFetchingJobs puts jobs interrupted by a restart back into the queue.
func (s *Store) RequeueFetchingJobs(ctx context.Context, now int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, "UPDATE fetch_jobs SET state = 'queued', updated_at = ? WHERE state = 'fetching'", now)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) LinkFetchJobMessage(ctx context.Context, jobID, messageID int64) error {
	_, err := s.db.ExecContext(ctx, "INSERT OR IGNORE INTO fetch_job_messages (job_id, message_id) VALUES (?, ?)", jobID, messageID)
	return err
}

func (s *Store) FetchJobsForMessage(ctx context.Context, messageID int64) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT job_id FROM fetch_job_messages WHERE message_id = ? ORDER BY job_id", messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) SetFetchJobReceipt(ctx context.Context, id int64, receipt string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE fetch_jobs SET receipt = ? WHERE id = ?", receipt, id)
	return err
}

type JobReceiptInfo struct {
	JobID, BotID, TgChatID, TgMessageID int64
	State, Error, Receipt               string
	Main                                []MediaStatus
}

func (s *Store) GetJobReceiptInfo(ctx context.Context, jobID int64) (*JobReceiptInfo, error) {
	ri := JobReceiptInfo{JobID: jobID}
	err := s.db.QueryRowContext(ctx, "SELECT bot_id, sender_id, link_tg_message_id, state, error, receipt FROM fetch_jobs WHERE id = ?", jobID).
		Scan(&ri.BotID, &ri.TgChatID, &ri.TgMessageID, &ri.State, &ri.Error, &ri.Receipt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT md.state, md.error FROM fetch_job_messages fj
		JOIN messages m ON m.id = fj.message_id AND m.deleted_at = 0
		JOIN message_media mm ON mm.message_id = m.id AND mm.role = 'main'
		JOIN media md ON md.id = mm.media_id
		WHERE fj.job_id = ? ORDER BY m.id, mm.position`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var ms MediaStatus
		if err := rows.Scan(&ms.State, &ms.Error); err != nil {
			return nil, err
		}
		ri.Main = append(ri.Main, ms)
	}
	return &ri, rows.Err()
}
