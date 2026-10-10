package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"mime"
	"net/url"
	"path/filepath"

	"tgarchive/internal/model"
)

// RoleArticle links a media row to the link message whose Telegraph snapshot references it.
// Such media never show as the message's own media (bubble, shared media, chat media).
const RoleArticle = "article"

type ArticleMediaInput struct {
	DedupeKey string // "web:" + hex(sha256(URL))
	URL       string // absolute source URL; stored as media.source_ref
	Kind      string // photo / video
}

type SaveArticleInput struct {
	JobID       int64
	MessageID   int64
	Path        string
	URL         string
	Title       string
	Description string
	AuthorName  string
	AuthorURL   string
	ImageURL    string // cover (og image); "" for none, otherwise also listed in Media
	Views       int64
	Media       []ArticleMediaInput // in content order, deduplicated by URL
	// Render returns the stored content JSON once every Media URL has its media id.
	Render func(ids map[string]int64) (string, error)
	Now    int64
}

// SaveArticle stores a fetched article in one transaction: one media row per URL (deduped by
// dedupe_key, linked to the message with role 'article'), the articles row (replacing an earlier
// one) and the job moved to 'fetched'. ErrNotFound when the message was deleted meanwhile.
func (s *Store) SaveArticle(ctx context.Context, in SaveArticleInput) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var one int
		err := tx.QueryRowContext(ctx, "SELECT 1 FROM messages WHERE id = ? AND deleted_at = 0", in.MessageID).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		ids := map[string]int64{}
		for i, m := range in.Media {
			id, err := upsertMedia(ctx, tx, 0, model.Media{DedupeKey: m.DedupeKey, SourceRef: m.URL, Kind: m.Kind})
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO message_media (message_id, media_id, role, position) VALUES (?, ?, ?, ?)",
				in.MessageID, id, RoleArticle, i); err != nil {
				return err
			}
			ids[m.URL] = id
		}
		content, err := in.Render(ids)
		if err != nil {
			return err
		}
		var image any // NULL when there is no cover
		if id, ok := ids[in.ImageURL]; ok && in.ImageURL != "" {
			image = id
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO articles (message_id, path, url, title, description, author_name, author_url, image_media_id, views, content, fetched_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (message_id) DO UPDATE SET path = excluded.path, url = excluded.url, title = excluded.title,
				description = excluded.description, author_name = excluded.author_name, author_url = excluded.author_url,
				image_media_id = excluded.image_media_id, views = excluded.views, content = excluded.content, fetched_at = excluded.fetched_at`,
			in.MessageID, in.Path, in.URL, in.Title, in.Description, in.AuthorName, in.AuthorURL, image, in.Views, content, in.Now); err != nil {
			return err
		}
		return affected(tx.ExecContext(ctx, "UPDATE telegraph_jobs SET state = 'fetched', error = '', updated_at = ? WHERE id = ?", in.Now, in.JobID))
	})
}

type ArticleMediaView struct {
	ID       int64  `json:"id"`
	Kind     string `json:"kind"`
	State    string `json:"state"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Duration int    `json:"duration"`
	Mime     string `json:"mime"`
}

type ArticleView struct {
	URL         string             `json:"url"`
	Title       string             `json:"title"`
	Description string             `json:"description"`
	AuthorName  string             `json:"author_name"`
	AuthorURL   string             `json:"author_url"`
	Views       int64              `json:"views"`
	FetchedAt   int64              `json:"fetched_at"`
	Content     json.RawMessage    `json:"content"`
	Media       []ArticleMediaView `json:"media"`
}

// GetArticle returns the archived article of a non-deleted message (ErrNotFound when none).
func (s *Store) GetArticle(ctx context.Context, messageID int64) (*ArticleView, error) {
	s = s.reader()
	var v ArticleView
	var content string
	err := s.db.QueryRowContext(ctx, `
		SELECT a.url, a.title, a.description, a.author_name, a.author_url, a.views, a.fetched_at, a.content
		FROM articles a JOIN messages m ON m.id = a.message_id AND m.deleted_at = 0 WHERE a.message_id = ?`, messageID,
	).Scan(&v.URL, &v.Title, &v.Description, &v.AuthorName, &v.AuthorURL, &v.Views, &v.FetchedAt, &content)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	v.Content = json.RawMessage(content)
	rows, err := s.db.QueryContext(ctx, `
		SELECT md.id, md.kind, md.state, md.width, md.height, md.duration, md.mime, md.path
		FROM message_media mm JOIN media md ON md.id = mm.media_id
		WHERE mm.message_id = ? AND mm.role = 'article' ORDER BY mm.position`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	v.Media = []ArticleMediaView{}
	for rows.Next() {
		var m ArticleMediaView
		var path string
		if err := rows.Scan(&m.ID, &m.Kind, &m.State, &m.Width, &m.Height, &m.Duration, &m.Mime, &path); err != nil {
			return nil, err
		}
		if m.Mime == "" && path != "" {
			m.Mime = mime.TypeByExtension(filepath.Ext(path))
		}
		v.Media = append(v.Media, m)
	}
	return &v, rows.Err()
}

// ArticleSummary is the article card data carried by MessageView (present when a job exists).
type ArticleSummary struct {
	State        string `json:"state"`
	Error        string `json:"error"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	AuthorName   string `json:"author_name"`
	ImageMediaID int64  `json:"image_media_id,omitempty"` // set only once the cover is downloaded
	URL          string `json:"url"`
}

// articleURL is the canonical address of a Telegraph path, used until the article is fetched.
func articleURL(path string) string { return "https://telegra.ph/" + url.PathEscape(path) }
