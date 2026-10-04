package downloader

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"tgarchive/internal/botapifs"
	"tgarchive/internal/store"
	"tgarchive/internal/tgbot"
)

// BotSource fetches "bot:" media through the local Bot API server (--local mode),
// which writes the file into the shared directory and returns its absolute path.
type BotSource struct {
	Clients func(ctx context.Context, botID int64) (*tgbot.Client, error)
	Mapper  botapifs.Mapper
}

func (b *BotSource) Fetch(ctx context.Context, m *store.Media, dstBase string) (string, int64, error) {
	cl, err := b.Clients(ctx, m.BotID)
	if err != nil {
		return "", 0, err
	}
	f, err := cl.GetFile(ctx, m.SourceRef)
	if err != nil {
		return "", 0, err
	}
	local, err := b.Mapper.Map(f.FilePath)
	if err != nil {
		return "", 0, err
	}
	dst := dstBase + strings.ToLower(filepath.Ext(local))
	if err := botapifs.LinkOrCopy(local, dst); err != nil {
		return "", 0, err
	}
	st, err := os.Stat(dst)
	if err != nil {
		return "", 0, err
	}
	_ = os.Remove(local)
	return dst, st.Size(), nil
}
