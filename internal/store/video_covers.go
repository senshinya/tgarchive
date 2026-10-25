package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
)

// VideoCoverJob names the original by all three identity fields. Media IDs can be reused
// after deletion, and a download can replace its path while ffmpeg is running.
type VideoCoverJob struct {
	ID        int64
	DedupeKey string
	Path      string
	Duration  int
	// CoverPath is an already generated thumbnail that another reference can reuse.
	CoverPath string
}
type VideoCover struct {
	Path          string
	Size          int64
	Width, Height int
}

func VideoCoverRel(key string) string {
	return fmt.Sprintf("covers/%x.jpg", sha256.Sum256([]byte(key)))
}

// videoCoverMessages selects live ordinary main-media references that still need a thumbnail.
// A native thumb that finishes while ffmpeg runs wins over the generated fallback.
const videoCoverMessages = `SELECT DISTINCT m.id FROM message_media main
 JOIN messages m ON m.id=main.message_id
 WHERE main.media_id=? AND main.role='main' AND m.deleted_at=0 AND m.thread_root_id=0
 AND NOT EXISTS (SELECT 1 FROM message_media thumb JOIN media tm ON tm.id=thumb.media_id
 WHERE thumb.message_id=m.id AND thumb.role='thumb' AND tm.state='done')`

// NextVideoCoverJob uses an ascending media cursor so a failed original never blocks later
// candidates. The worker resets the cursor after reaching the end of a sweep.
func (s *Store) NextVideoCoverJob(ctx context.Context, afterID int64) (*VideoCoverJob, error) {
	s = s.reader()
	var j VideoCoverJob
	err := s.db.QueryRowContext(ctx, `SELECT md.id,md.dedupe_key,md.path,md.duration,
 COALESCE(generated.path,'') FROM media md
 LEFT JOIN media generated ON generated.dedupe_key='cover:'||md.dedupe_key AND generated.state='done'
 WHERE md.id>? AND md.state='done' AND md.path!=''
 AND (md.kind='video' OR (md.kind='document' AND md.mime LIKE 'video/%'))
 AND EXISTS (SELECT 1 FROM message_media main JOIN messages m ON m.id=main.message_id
 WHERE main.media_id=md.id AND main.role='main' AND m.deleted_at=0 AND m.thread_root_id=0
 AND NOT EXISTS (SELECT 1 FROM message_media thumb JOIN media tm ON tm.id=thumb.media_id
 WHERE thumb.message_id=m.id AND thumb.role='thumb' AND tm.state='done'))
 ORDER BY md.id LIMIT 1`, afterID).Scan(&j.ID, &j.DedupeKey, &j.Path, &j.Duration, &j.CoverPath)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// InstallVideoCover atomically rechecks the original's identity and binds one generated row
// to every still-eligible reference. false means no binding was made: discard a new output.
func (s *Store) InstallVideoCover(ctx context.Context, job VideoCoverJob, cover VideoCover) (int64, bool, error) {
	if cover.Path != VideoCoverRel(job.DedupeKey) || cover.Size <= 0 || cover.Width < 1 || cover.Height < 1 || cover.Width > 640 || cover.Height > 360 {
		return 0, false, errors.New("invalid generated video cover")
	}
	var coverID int64
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var original int64
		err := tx.QueryRowContext(ctx, `SELECT id FROM media WHERE id=? AND dedupe_key=? AND path=? AND state='done'
  AND (kind='video' OR (kind='document' AND mime LIKE 'video/%'))`, job.ID, job.DedupeKey, job.Path).Scan(&original)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		messages, err := queryIDs(ctx, tx, videoCoverMessages, job.ID)
		if err != nil || len(messages) == 0 {
			return err
		}
		err = tx.QueryRowContext(ctx, `INSERT INTO media(dedupe_key,kind,mime,file_name,size,width,height,path,state)
  VALUES(?,'photo','image/jpeg',?,?,?,?,?,'done')
  ON CONFLICT(dedupe_key) DO UPDATE SET kind='photo',mime='image/jpeg',file_name=excluded.file_name,
  size=excluded.size,width=excluded.width,height=excluded.height,path=excluded.path,state='done',error=''
  RETURNING id`, "cover:"+job.DedupeKey, filepath.Base(cover.Path), cover.Size, cover.Width, cover.Height, cover.Path).Scan(&coverID)
		if err != nil {
			return err
		}
		// Put fallback thumbs after native attachments, so a native thumb that finishes
		// later becomes the first ready thumb without replacing either archived file.
		for _, messageID := range messages {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO message_media(message_id,media_id,role,position) VALUES(?,?,'thumb',2147483647)`, messageID, coverID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, false, err
	}
	return coverID, coverID != 0, nil
}
