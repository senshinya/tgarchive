package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"tgarchive/internal/model"
)

type IngestInput struct {
	BotID  int64
	Sender model.Sender
	Msg    *model.Message
	Offset int64 // when > 0, bots.update_offset advances to it in the same transaction
	Now    int64
	// TelegraphPath, when set, queues a Telegraph job for the message in the same transaction,
	// but only if the message is newly created (an edit never queues a second snapshot).
	TelegraphPath string
}

type IngestResult struct {
	MessageID       int64
	ChatID          int64
	ChatCreated     bool
	Created         bool     // false when an existing message was updated (edit)
	OrphanPaths     []string // media files no longer referenced; caller deletes them
	TelegraphQueued bool     // a Telegraph job was created for this message
}

func (s *Store) Ingest(ctx context.Context, in IngestInput) (*IngestResult, error) {
	res := &IngestResult{}
	m := in.Msg
	ents := m.Entities
	if ents == nil {
		ents = []model.Entity{}
	}
	entJSON, err := json.Marshal(ents)
	if err != nil {
		return nil, err
	}
	fwd := ""
	if m.ForwardOrigin != nil {
		b, err := json.Marshal(m.ForwardOrigin)
		if err != nil {
			return nil, err
		}
		fwd = string(b)
	}

	err = s.withTx(ctx, func(tx *sql.Tx) error {
		snd := in.Sender
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO senders (tg_user_id, first_name, last_name, username, updated_at) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (tg_user_id) DO UPDATE SET first_name = excluded.first_name, last_name = excluded.last_name,
				username = excluded.username, updated_at = excluded.updated_at`,
			snd.TgUserID, snd.FirstName, snd.LastName, snd.Username, in.Now); err != nil {
			return err
		}

		err := tx.QueryRowContext(ctx, "SELECT id FROM chats WHERE bot_id = ? AND sender_id = ?", in.BotID, snd.TgUserID).Scan(&res.ChatID)
		if errors.Is(err, sql.ErrNoRows) {
			if err := tx.QueryRowContext(ctx, "INSERT INTO chats (bot_id, sender_id) VALUES (?, ?) RETURNING id", in.BotID, snd.TgUserID).Scan(&res.ChatID); err != nil {
				return err
			}
			res.ChatCreated = true
		} else if err != nil {
			return err
		}

		var existing, existingDeletedAt int64
		err = tx.QueryRowContext(ctx, "SELECT id, deleted_at FROM messages WHERE chat_id = ? AND source = ? AND origin_chat_id = ? AND tg_message_id = ?",
			res.ChatID, m.Source, m.OriginChatID, m.TgMessageID).Scan(&existing, &existingDeletedAt)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if err := tx.QueryRowContext(ctx, `
				INSERT INTO messages (chat_id, tg_message_id, source, media_group_id, date, edit_date, kind, text, entities_json,
					forward_origin_json, reply_to_tg_message_id, origin_chat_id, origin_chat_title, origin_link, extra_json, raw_format, raw_json)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
				res.ChatID, m.TgMessageID, m.Source, m.MediaGroupID, m.Date, m.EditDate, string(m.Kind), m.Text, string(entJSON),
				fwd, m.ReplyToTgMessageID, m.OriginChatID, m.OriginChatTitle, m.OriginLink, string(m.Extra), m.RawFormat, string(m.Raw),
			).Scan(&res.MessageID); err != nil {
				return err
			}
			res.Created = true
			if in.TelegraphPath != "" {
				if _, err := tx.ExecContext(ctx, `INSERT INTO telegraph_jobs (message_id, path, state, created_at, updated_at)
					VALUES (?, ?, 'queued', ?, ?)`, res.MessageID, in.TelegraphPath, in.Now, in.Now); err != nil {
					return err
				}
				res.TelegraphQueued = true
			}
		case err != nil:
			return err
		case existingDeletedAt != 0:
			// Edit of a message already soft-deleted via DeleteMessage: ignore the edit entirely
			// (no text/kind/media changes, no re-link), but still advance the offset below.
			res.MessageID = existing
			if in.Offset > 0 {
				if _, err := tx.ExecContext(ctx, "UPDATE bots SET update_offset = ? WHERE id = ? AND update_offset < ?", in.Offset, in.BotID, in.Offset); err != nil {
					return err
				}
			}
			return nil
		default:
			res.MessageID = existing
			if _, err := tx.ExecContext(ctx, `
				UPDATE messages SET edit_date = ?, kind = ?, text = ?, entities_json = ?, extra_json = ?, raw_json = ? WHERE id = ?`,
				m.EditDate, string(m.Kind), m.Text, string(entJSON), string(m.Extra), string(m.Raw), existing); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "DELETE FROM message_media WHERE message_id = ?", existing); err != nil {
				return err
			}
		}

		for i, md := range m.Media {
			mediaID, err := upsertMedia(ctx, tx, in.BotID, md)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO message_media (message_id, media_id, role, position) VALUES (?, ?, ?, ?)",
				res.MessageID, mediaID, md.Role, i); err != nil {
				return err
			}
		}

		if res.Created {
			lastAt := m.Date
			if m.Source == model.SourceUserbotFetch {
				lastAt = in.Now
			}
			if _, err := tx.ExecContext(ctx, "UPDATE chats SET last_message_at = ? WHERE id = ? AND last_message_at < ?", lastAt, res.ChatID, lastAt); err != nil {
				return err
			}
		} else {
			paths, err := collectOrphans(ctx, tx)
			if err != nil {
				return err
			}
			res.OrphanPaths = paths
		}

		if in.Offset > 0 {
			if _, err := tx.ExecContext(ctx, "UPDATE bots SET update_offset = ? WHERE id = ? AND update_offset < ?", in.Offset, in.BotID, in.Offset); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// upsertMedia dedupes by dedupe_key. A failed row seen again is requeued; a not-yet-done row
// takes the newest bot_id/source_ref because Bot API file_ids are only valid for the receiving bot.
func upsertMedia(ctx context.Context, tx *sql.Tx, botID int64, md model.Media) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `
		INSERT INTO media (dedupe_key, bot_id, source_ref, kind, mime, file_name, size, width, height, duration, waveform)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (dedupe_key) DO UPDATE SET
			bot_id          = CASE WHEN media.state = 'done' THEN media.bot_id ELSE excluded.bot_id END,
			source_ref      = CASE WHEN media.state = 'done' THEN media.source_ref ELSE excluded.source_ref END,
			attempts        = CASE WHEN media.state = 'failed' THEN 0 ELSE media.attempts END,
			next_attempt_at = CASE WHEN media.state = 'failed' THEN 0 ELSE media.next_attempt_at END,
			error           = CASE WHEN media.state = 'failed' THEN '' ELSE media.error END,
			state           = CASE WHEN media.state = 'failed' THEN 'pending' ELSE media.state END
		RETURNING id`,
		md.DedupeKey, botID, md.SourceRef, md.Kind, md.Mime, md.FileName, md.Size, md.Width, md.Height, md.Duration, md.Waveform).Scan(&id)
	return id, err
}

// collectOrphans deletes media rows no message links to and returns their stored paths.
func collectOrphans(ctx context.Context, tx *sql.Tx) ([]string, error) {
	rows, err := tx.QueryContext(ctx, "SELECT id, path FROM media WHERE NOT EXISTS (SELECT 1 FROM message_media mm WHERE mm.media_id = media.id)")
	if err != nil {
		return nil, err
	}
	var ids []int64
	paths := []string{}
	for rows.Next() {
		var id int64
		var p string
		if err := rows.Scan(&id, &p); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
		if p != "" {
			paths = append(paths, p)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, "DELETE FROM media WHERE id = ?", id); err != nil {
			return nil, err
		}
	}
	return paths, nil
}

// DeleteMessage soft-deletes a message and drops its media links; unreferenced media are removed.
func (s *Store) DeleteMessage(ctx context.Context, id, now int64) (int64, []string, error) {
	var chatID int64
	var orphans []string
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, "SELECT chat_id FROM messages WHERE id = ? AND deleted_at = 0", id).Scan(&chatID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE messages SET deleted_at = ? WHERE id = ?", now, id); err != nil {
			return err
		}
		// Unlinking the media (article media included) lets collectOrphans drop unshared files; the
		// Telegraph snapshot and its job go with the message (the FK cascade only fires on a hard delete).
		for _, q := range []string{
			"DELETE FROM message_media WHERE message_id = ?",
			"DELETE FROM articles WHERE message_id = ?",
			"DELETE FROM telegraph_jobs WHERE message_id = ?",
		} {
			if _, err := tx.ExecContext(ctx, q, id); err != nil {
				return err
			}
		}
		orphans, err = collectOrphans(ctx, tx)
		return err
	})
	return chatID, orphans, err
}

// PurgeBot deletes a bot with all its chats, messages and now-unreferenced media.
func (s *Store) PurgeBot(ctx context.Context, id int64) ([]string, error) {
	var orphans []string
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if err := affected(tx.ExecContext(ctx, "DELETE FROM bots WHERE id = ?", id)); err != nil {
			return err
		}
		var err error
		orphans, err = collectOrphans(ctx, tx)
		return err
	})
	return orphans, err
}
