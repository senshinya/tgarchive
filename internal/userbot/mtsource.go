package userbot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	tgdown "github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"tgarchive/internal/convert/mtproto"
	"tgarchive/internal/downloader"
	"tgarchive/internal/store"
)

// MTSource downloads "mt:" media through the userbot account.
type MTSource struct {
	API API
}

func (s *MTSource) Fetch(ctx context.Context, m *store.Media, dstBase string) (string, int64, error) {
	var ref mtproto.MediaRef
	if err := json.Unmarshal([]byte(m.SourceRef), &ref); err != nil {
		return "", 0, fmt.Errorf("bad source ref for media %d: %w", m.ID, err)
	}
	dst := dstBase + extFor(m)
	tmp := dst + ".part"
	err := s.API.With(ctx, func(api *tg.Client) error {
		err := download(ctx, api, ref.Location(), tmp, m.Size)
		if !tgerr.Is(err, "FILE_REFERENCE_EXPIRED", "FILE_REFERENCE_INVALID") {
			return err
		}
		fresh, err := refreshRef(ctx, api, ref, m.DedupeKey)
		if err != nil {
			return err
		}
		return download(ctx, api, fresh.Location(), tmp, m.Size)
	})
	if err != nil {
		os.Remove(tmp)
		return "", 0, err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return "", 0, err
	}
	st, err := os.Stat(dst)
	if err != nil {
		return "", 0, err
	}
	return dst, st.Size(), nil
}

// download streams loc into path, reporting progress against size (the size Telegram announced).
func download(ctx context.Context, api *tg.Client, loc tg.InputFileLocationClass, path string, size int64) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	_, err = tgdown.NewDownloader().Download(api, loc).Stream(ctx, downloader.CountingWriter(ctx, f, size))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// refreshRef re-reads the source message to obtain a current file reference for dedupeKey.
func refreshRef(ctx context.Context, api *tg.Client, ref mtproto.MediaRef, dedupeKey string) (mtproto.MediaRef, error) {
	res, err := api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
		Channel: &tg.InputChannel{ChannelID: ref.ChannelID, AccessHash: ref.ChannelHash},
		ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: ref.MsgID}},
	})
	if err != nil {
		return mtproto.MediaRef{}, err
	}
	list, _, _, err := messagesOf(res)
	if err != nil {
		return mtproto.MediaRef{}, err
	}
	for _, mc := range list {
		m, ok := mc.(*tg.Message)
		if !ok || m.ID != ref.MsgID {
			continue
		}
		conv, err := mtproto.Convert(m, mtproto.Channel{ID: ref.ChannelID, AccessHash: ref.ChannelHash}, mtproto.Names{})
		if err != nil {
			return mtproto.MediaRef{}, err
		}
		for _, md := range conv.Media {
			if md.DedupeKey == dedupeKey {
				var fresh mtproto.MediaRef
				err := json.Unmarshal([]byte(md.SourceRef), &fresh)
				return fresh, err
			}
		}
	}
	return mtproto.MediaRef{}, errNoMessage
}

var safeExt = regexp.MustCompile(`^\.[a-z0-9]{1,10}$`)

var mimeExt = map[string]string{
	"image/jpeg": ".jpg", "image/png": ".png", "image/gif": ".gif", "image/webp": ".webp",
	"video/mp4": ".mp4", "video/webm": ".webm", "video/quicktime": ".mov",
	"audio/ogg": ".ogg", "audio/mpeg": ".mp3", "audio/mp4": ".m4a", "audio/x-m4a": ".m4a", "audio/flac": ".flac",
	"application/x-tgsticker": ".tgs", "application/pdf": ".pdf", "application/zip": ".zip",
}

func extFor(m *store.Media) string {
	if e := strings.ToLower(filepath.Ext(m.FileName)); safeExt.MatchString(e) {
		return e
	}
	if e, ok := mimeExt[m.Mime]; ok {
		return e
	}
	return ".bin"
}
