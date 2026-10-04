package store

import (
	"context"
	"database/sql"
	"errors"
)

type Media struct {
	ID            int64
	DedupeKey     string
	BotID         int64
	SourceRef     string
	Kind          string
	Mime          string
	FileName      string
	Size          int64
	Width         int
	Height        int
	Duration      int
	Waveform      []byte
	Path          string
	State         string
	Attempts      int
	NextAttemptAt int64
	Error         string
}

const mediaCols = "id, dedupe_key, bot_id, source_ref, kind, mime, file_name, size, width, height, duration, waveform, path, state, attempts, next_attempt_at, error"

func scanMedia(r scanner) (*Media, error) {
	var m Media
	err := r.Scan(&m.ID, &m.DedupeKey, &m.BotID, &m.SourceRef, &m.Kind, &m.Mime, &m.FileName, &m.Size, &m.Width, &m.Height,
		&m.Duration, &m.Waveform, &m.Path, &m.State, &m.Attempts, &m.NextAttemptAt, &m.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *Store) GetMedia(ctx context.Context, id int64) (*Media, error) {
	return scanMedia(s.db.QueryRowContext(ctx, "SELECT "+mediaCols+" FROM media WHERE id = ?", id))
}

func (s *Store) DueMedia(ctx context.Context, now int64, limit int) ([]Media, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+mediaCols+" FROM media WHERE state = 'pending' AND next_attempt_at <= ? ORDER BY id LIMIT ?", now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Media{}
	for rows.Next() {
		m, err := scanMedia(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// MarkMediaDone returns false when the row no longer exists (deleted while downloading).
func (s *Store) MarkMediaDone(ctx context.Context, id int64, path string, size int64) (bool, error) {
	err := affected(s.db.ExecContext(ctx, "UPDATE media SET state = 'done', path = ?, size = ?, error = '' WHERE id = ?", path, size, id))
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) MarkMediaRetry(ctx context.Context, id int64, attempts int, nextAt int64, errMsg string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE media SET attempts = ?, next_attempt_at = ?, error = ? WHERE id = ?", attempts, nextAt, errMsg, id)
	return err
}

func (s *Store) MarkMediaFailed(ctx context.Context, id int64, attempts int, errMsg string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE media SET state = 'failed', attempts = ?, error = ? WHERE id = ?", attempts, errMsg, id)
	return err
}

func (s *Store) MarkMediaTooLarge(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE media SET state = 'too_large', error = '' WHERE id = ?", id)
	return err
}

func (s *Store) ResetMedia(ctx context.Context, id int64) error {
	return affected(s.db.ExecContext(ctx,
		"UPDATE media SET state = 'pending', attempts = 0, next_attempt_at = 0, error = '' WHERE id = ? AND state = 'failed'", id))
}

func (s *Store) MessagesForMedia(ctx context.Context, mediaID int64) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT mm.message_id FROM message_media mm JOIN messages m ON m.id = mm.message_id
		WHERE mm.media_id = ? AND m.deleted_at = 0 ORDER BY mm.message_id`, mediaID)
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

type MediaStatus struct{ State, Error string }

type ReceiptInfo struct {
	MessageID, BotID, TgChatID, TgMessageID int64
	Source, Receipt                         string
	Main                                    []MediaStatus
}

func (s *Store) GetReceiptInfo(ctx context.Context, messageID int64) (*ReceiptInfo, error) {
	var ri ReceiptInfo
	err := s.db.QueryRowContext(ctx, `
		SELECT m.id, c.bot_id, c.sender_id, m.tg_message_id, m.source, m.receipt
		FROM messages m JOIN chats c ON c.id = m.chat_id WHERE m.id = ? AND m.deleted_at = 0`, messageID,
	).Scan(&ri.MessageID, &ri.BotID, &ri.TgChatID, &ri.TgMessageID, &ri.Source, &ri.Receipt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT md.state, md.error FROM message_media mm JOIN media md ON md.id = mm.media_id
		WHERE mm.message_id = ? AND mm.role = 'main' ORDER BY mm.position`, messageID)
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

func (s *Store) SetReceipt(ctx context.Context, messageID int64, receipt string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE messages SET receipt = ? WHERE id = ?", receipt, messageID)
	return err
}
