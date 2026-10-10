package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"tgarchive/internal/model"
)

type IngestInput struct {
	BotID  int64
	Sender model.Sender
	// ChannelID, when set, files the message in that watched channel's conversation instead of a
	// bot × sender chat (BotID and Sender are then unused); Stats is its stats_json.
	ChannelID int64
	Stats     string
	// ThreadRootID files the message as a comment on that archived post of the channel: kept out
	// of the conversation's timeline, and not moving the conversation up the list.
	ThreadRootID int64
	Msg          *model.Message
	Offset       int64 // when > 0, bots.update_offset advances to it in the same transaction
	Now          int64
	// TelegraphPath, when set, queues a Telegraph job for the message in the same transaction,
	// but only if the message is newly created (an edit never queues a second snapshot).
	TelegraphPath string
	// Revive brings a message deleted in the archive back when it is archived again; without it the
	// deleted message is left alone. Only a user's own request to archive it sets it (fetching its
	// link, a backfill): edits and polling must not undo a deletion.
	Revive bool
}

type IngestResult struct {
	MessageID       int64
	ChatID          int64
	ChatCreated     bool
	Created         bool     // false when an existing message was updated (edit); true for a revived one
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
		var unlinked []int64 // media an edit unlinked, the only ones it can leave orphaned
		if in.ChannelID != 0 {
			var watched, n int
			if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM channel_watches WHERE channel_id = ?", in.ChannelID).Scan(&watched); err != nil {
				return err
			}
			if watched == 0 {
				return ErrNoWatch
			}
			if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM chats WHERE channel_id = ?", in.ChannelID).Scan(&n); err != nil {
				return err
			}
			id, err := channelChat(ctx, tx, in.ChannelID, 0)
			if err != nil {
				return err
			}
			res.ChatID, res.ChatCreated = id, n == 0
		} else if err := privateChat(ctx, tx, in, res); err != nil {
			return err
		}

		var existing, existingDeletedAt int64
		err = tx.QueryRowContext(ctx, "SELECT id, deleted_at FROM messages WHERE chat_id = ? AND source = ? AND origin_chat_id = ? AND tg_message_id = ?",
			res.ChatID, m.Source, m.OriginChatID, m.TgMessageID).Scan(&existing, &existingDeletedAt)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if err := tx.QueryRowContext(ctx, `
				INSERT INTO messages (chat_id, tg_message_id, source, media_group_id, date, edit_date, kind, text, entities_json,
					forward_origin_json, reply_to_tg_message_id, origin_chat_id, origin_chat_title, origin_link, extra_json, raw_format, raw_json,
					stats_json, thread_root_id)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
				res.ChatID, m.TgMessageID, m.Source, m.MediaGroupID, m.Date, m.EditDate, string(m.Kind), m.Text, string(entJSON),
				fwd, m.ReplyToTgMessageID, m.OriginChatID, m.OriginChatTitle, m.OriginLink, string(m.Extra), m.RawFormat, string(m.Raw),
				in.Stats, in.ThreadRootID,
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
		case existingDeletedAt != 0 && !in.Revive:
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
			// An edit, or a deleted message archived again (Revive): DeleteMessage dropped its media
			// links, Telegraph snapshot and favorite, so it comes back like a new one, re-linking (and
			// re-queuing) its media below; clearing deleted_at puts it back in the search index.
			res.MessageID, res.Created = existing, existingDeletedAt != 0
			if _, err := tx.ExecContext(ctx, `
				UPDATE messages SET edit_date = ?, kind = ?, text = ?, entities_json = ?, extra_json = ?, raw_json = ?,
					stats_json = CASE WHEN ? != '' THEN ? ELSE stats_json END, deleted_at = 0 WHERE id = ?`,
				m.EditDate, string(m.Kind), m.Text, string(entJSON), string(m.Extra), string(m.Raw), in.Stats, in.Stats, existing); err != nil {
				return err
			}
			// Article media belong to the message's Telegraph snapshot, not to its Telegram content.
			if unlinked, err = queryIDs(ctx, tx, "DELETE FROM message_media WHERE message_id = ? AND role != 'article' RETURNING media_id", existing); err != nil {
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

		// A comment leaves the conversation where it is in the list.
		if res.Created && in.ThreadRootID == 0 {
			lastAt := m.Date
			if m.Source == model.SourceUserbotFetch || m.Source == model.SourceChannelWatch {
				lastAt = in.Now
			}
			if _, err := tx.ExecContext(ctx, "UPDATE chats SET last_message_at = ? WHERE id = ? AND last_message_at < ?", lastAt, res.ChatID, lastAt); err != nil {
				return err
			}
		} else if !res.Created {
			paths, err := collectOrphans(ctx, tx, unlinked)
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

// privateChat finds or creates the bot × sender chat of in, refreshing the sender's names.
func privateChat(ctx context.Context, tx *sql.Tx, in IngestInput, res *IngestResult) error {
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
	return nil
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

// queryIDs returns the single integer column q yields (a SELECT, or a DELETE ... RETURNING).
func queryIDs(ctx context.Context, tx *sql.Tx, q string, args ...any) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// orphanBatch bounds the ids collectOrphans checks in one query (SQLite's variable limit).
const orphanBatch = 500

// collectOrphans deletes those of candidates (the media rows this transaction unlinked) that no
// message (nor custom emoji) links to any more, and returns their stored paths, by media id.
// Only the candidates are looked at: a media row nothing unlinked cannot have become an orphan.
func collectOrphans(ctx context.Context, tx *sql.Tx, candidates []int64) ([]string, error) {
	cand := slices.Compact(slices.Sorted(slices.Values(candidates)))
	var ids []int64
	paths := []string{}
	for len(cand) > 0 {
		batch := cand[:min(len(cand), orphanBatch)]
		cand = cand[len(batch):]
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		rows, err := tx.QueryContext(ctx, `SELECT id, kind, path FROM media WHERE id IN (`+
			strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")+`)
			AND NOT EXISTS (SELECT 1 FROM message_media mm WHERE mm.media_id = media.id)
			AND NOT EXISTS (SELECT 1 FROM custom_emoji ce WHERE ce.media_id = media.id) ORDER BY id`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			var kind, p string
			if err := rows.Scan(&id, &kind, &p); err != nil {
				rows.Close()
				return nil, err
			}
			ids = append(ids, id)
			if p == "" {
				continue
			}
			paths = append(paths, p)
			if kind == "video" || kind == "animation" || kind == "video_note" {
				// A browser-playable copy, or one a crash left half-written or unrecorded; removing a
				// file that does not exist is a no-op.
				paths = append(paths, CompatRel(p), CompatPartRel(p))
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
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
		// Telegraph snapshot, its job and the favorite go with the message (the FK cascade only fires on a hard delete).
		unlinked, err := queryIDs(ctx, tx, "DELETE FROM message_media WHERE message_id = ? RETURNING media_id", id)
		if err != nil {
			return err
		}
		for _, q := range []string{
			"DELETE FROM articles WHERE message_id = ?",
			"DELETE FROM telegraph_jobs WHERE message_id = ?",
			"DELETE FROM favorites WHERE message_id = ?",
		} {
			if _, err := tx.ExecContext(ctx, q, id); err != nil {
				return err
			}
		}
		orphans, err = collectOrphans(ctx, tx, unlinked)
		return err
	})
	return chatID, orphans, err
}

// PurgeBot deletes a bot with all its chats, messages and now-unreferenced media.
func (s *Store) PurgeBot(ctx context.Context, id int64) ([]string, error) {
	var orphans []string
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		// The media of the bot's messages, which the cascade below unlinks.
		unlinked, err := queryIDs(ctx, tx, `SELECT DISTINCT mm.media_id FROM chats c JOIN messages m ON m.chat_id = c.id
			JOIN message_media mm ON mm.message_id = m.id WHERE c.bot_id = ?`, id)
		if err != nil {
			return err
		}
		if err := affected(tx.ExecContext(ctx, "DELETE FROM bots WHERE id = ?", id)); err != nil {
			return err
		}
		orphans, err = collectOrphans(ctx, tx, unlinked)
		return err
	})
	return orphans, err
}
