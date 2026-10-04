package userbot

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/gotd/td/tgmock"

	"tgarchive/internal/convert/mtproto"
	"tgarchive/internal/store"
)

func photoPost(fileRef []byte) *tg.Message {
	return &tg.Message{ID: 42, PeerID: &tg.PeerChannel{ChannelID: 500}, Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{
		ID: 9, AccessHash: 99, FileReference: fileRef, Sizes: []tg.PhotoSizeClass{&tg.PhotoSize{Type: "y", W: 10, H: 10, Size: 5}}}}}
}

func mtMedia(t *testing.T, fileRef []byte) *store.Media {
	t.Helper()
	conv, err := mtproto.Convert(photoPost(fileRef), mtproto.Channel{ID: 500, AccessHash: 5005, Username: "chan"}, mtproto.Names{})
	if err != nil {
		t.Fatal(err)
	}
	md := conv.Media[0]
	return &store.Media{ID: 1, DedupeKey: md.DedupeKey, SourceRef: md.SourceRef, Kind: md.Kind, Mime: md.Mime}
}

func mtEnv(t *testing.T, ready bool, h func(req bin.Encoder) (bin.Encoder, error)) *MTSource {
	f := newFakeTG()
	f.extra = h
	return &MTSource{API: &fakeAPI{ready: ready, client: tg.NewClient(tgmock.Invoker(f.handle))}}
}

func TestMTSourceDownloads(t *testing.T) {
	data := []byte("jpeg-bytes")
	src := mtEnv(t, true, func(req bin.Encoder) (bin.Encoder, error) {
		if r, ok := req.(*tg.UploadGetFileRequest); ok {
			loc := r.Location.(*tg.InputPhotoFileLocation)
			if loc.ID != 9 || loc.ThumbSize != "y" || !reflect.DeepEqual(loc.FileReference, []byte{1}) {
				t.Errorf("location = %+v", loc)
			}
			if r.Offset > 0 {
				return &tg.UploadFile{Type: &tg.StorageFileJpeg{}}, nil
			}
			return &tg.UploadFile{Type: &tg.StorageFileJpeg{}, Bytes: data}, nil
		}
		return nil, nil
	})
	dir := t.TempDir()
	path, size, err := src.Fetch(ctx, mtMedia(t, []byte{1}), filepath.Join(dir, "abc"))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if path != filepath.Join(dir, "abc.jpg") || size != int64(len(data)) || string(got) != string(data) {
		t.Fatalf("path=%s size=%d got=%q", path, size, got)
	}
	if _, err := os.Stat(path + ".part"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(".part left behind")
	}
}

func TestMTSourceRefreshesExpiredReference(t *testing.T) {
	data := []byte("fresh")
	refetched := false
	src := mtEnv(t, true, func(req bin.Encoder) (bin.Encoder, error) {
		switch r := req.(type) {
		case *tg.UploadGetFileRequest:
			loc := r.Location.(*tg.InputPhotoFileLocation)
			if reflect.DeepEqual(loc.FileReference, []byte{1}) {
				return nil, tgerr.New(400, "FILE_REFERENCE_EXPIRED")
			}
			if r.Offset > 0 {
				return &tg.UploadFile{Type: &tg.StorageFileJpeg{}}, nil
			}
			return &tg.UploadFile{Type: &tg.StorageFileJpeg{}, Bytes: data}, nil
		case *tg.ChannelsGetMessagesRequest:
			in := r.Channel.(*tg.InputChannel)
			if in.ChannelID != 500 || in.AccessHash != 5005 || r.ID[0].(*tg.InputMessageID).ID != 42 {
				t.Errorf("refetch request = %+v", r)
			}
			refetched = true
			return &tg.MessagesChannelMessages{Messages: []tg.MessageClass{photoPost([]byte{2})}}, nil
		}
		return nil, nil
	})
	path, _, err := src.Fetch(ctx, mtMedia(t, []byte{1}), filepath.Join(t.TempDir(), "abc"))
	if err != nil || !refetched {
		t.Fatalf("Fetch = %v, refetched=%v", err, refetched)
	}
	if got, _ := os.ReadFile(path); string(got) != "fresh" {
		t.Fatalf("content = %q", got)
	}
}

func TestMTSourceFailures(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := mtEnv(t, false, nil).Fetch(ctx, mtMedia(t, []byte{1}), filepath.Join(dir, "a")); !errors.Is(err, ErrNotReady) {
		t.Fatalf("not ready err = %v", err)
	}
	gone := mtEnv(t, true, func(req bin.Encoder) (bin.Encoder, error) {
		switch req.(type) {
		case *tg.UploadGetFileRequest:
			return nil, tgerr.New(400, "FILE_REFERENCE_EXPIRED")
		case *tg.ChannelsGetMessagesRequest:
			return &tg.MessagesChannelMessages{Messages: []tg.MessageClass{&tg.MessageEmpty{ID: 42}}}, nil
		}
		return nil, nil
	})
	if _, _, err := gone.Fetch(ctx, mtMedia(t, []byte{1}), filepath.Join(dir, "b")); !errors.Is(err, errNoMessage) {
		t.Fatalf("deleted post err = %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("files left behind: %v", entries)
	}
	bad := &store.Media{DedupeKey: "mt:doc:1", SourceRef: "{not json"}
	if _, _, err := mtEnv(t, true, nil).Fetch(ctx, bad, filepath.Join(dir, "c")); err == nil {
		t.Fatal("bad source ref accepted")
	}
}

func TestExtFor(t *testing.T) {
	cases := map[string]*store.Media{
		".jpg":  {Mime: "image/jpeg"},
		".pdf":  {FileName: "Report.PDF", Mime: "application/octet-stream"},
		".mp4":  {FileName: "", Mime: "video/mp4"},
		".tgs":  {Mime: "application/x-tgsticker"},
		".bin":  {FileName: "weird.name with space", Mime: "application/x-unknown"},
		".ogg":  {Mime: "audio/ogg"},
		".webm": {FileName: "clip.webm", Mime: "video/webm"},
	}
	for want, m := range cases {
		if got := extFor(m); got != want {
			t.Errorf("extFor(%+v) = %s, want %s", m, got, want)
		}
	}
}
