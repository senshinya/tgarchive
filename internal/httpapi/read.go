package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tgarchive/internal/downloader"
	"tgarchive/internal/events"
	"tgarchive/internal/store"
)

type BotView struct {
	ID        int64  `json:"id"`
	TgBotID   int64  `json:"tg_bot_id"`
	Username  string `json:"username"`
	Name      string `json:"name"`
	HasAvatar bool   `json:"has_avatar"`
	Enabled   bool   `json:"enabled"`
	Status    string `json:"status"`
	LastError string `json:"last_error"`
}

func botView(b *store.Bot) BotView {
	return BotView{ID: b.ID, TgBotID: b.TgBotID, Username: b.Username, Name: b.Name, HasAvatar: b.AvatarPath != "",
		Enabled: b.Enabled, Status: b.Status, LastError: b.LastError}
}

func (s *Server) listBots(w http.ResponseWriter, r *http.Request) {
	bots, err := s.Store.ListBots(r.Context())
	if err != nil {
		storeErr(w, err)
		return
	}
	out := make([]BotView, 0, len(bots))
	for i := range bots {
		out = append(out, botView(&bots[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) listChats(w http.ResponseWriter, r *http.Request) {
	botID, ok := queryInt(r, "bot_id", 0, 0, 1<<62)
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad bot_id")
		return
	}
	chats, err := s.Store.ListChats(r.Context(), botID)
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, chats)
}

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request) {
	chatID, ok := pathID(r, "id")
	page, ok2 := pageParams(r)
	limit, ok3 := queryInt(r, "limit", 50, 1, 100)
	if !ok || !ok2 || !ok3 {
		writeErr(w, http.StatusBadRequest, "bad chat id, before, after, around or limit")
		return
	}
	msgs, err := s.Store.ListMessagesPage(r.Context(), chatID, page, int(limit))
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, msgs)
}

// pageParams reads a conversation page: at most one of before, after and around.
func pageParams(r *http.Request) (store.Page, bool) {
	before, ok1 := queryInt(r, "before", 0, 0, 1<<62)
	after, ok2 := queryInt(r, "after", 0, 0, 1<<62)
	around, ok3 := queryInt(r, "around", 0, 0, 1<<62)
	set := 0
	for _, v := range []int64{before, after, around} {
		if v != 0 {
			set++
		}
	}
	return store.Page{Before: before, After: after, Around: around}, ok1 && ok2 && ok3 && set <= 1
}

func (s *Server) listChatMedia(w http.ResponseWriter, r *http.Request) {
	chatID, ok := pathID(r, "id")
	before, ok2 := queryInt(r, "before", 0, 0, 1<<62)
	limit, ok3 := queryInt(r, "limit", 50, 1, 100)
	if !ok || !ok2 || !ok3 {
		writeErr(w, http.StatusBadRequest, "bad chat id, before or limit")
		return
	}
	msgs, err := s.Store.ListChatMedia(r.Context(), chatID, r.URL.Query().Get("type"), before, int(limit))
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, msgs)
}

func (s *Server) listBotMessages(w http.ResponseWriter, r *http.Request) {
	botID, ok := pathID(r, "id")
	page, ok2 := pageParams(r)
	limit, ok3 := queryInt(r, "limit", 50, 1, 100)
	if !ok || !ok2 || !ok3 {
		writeErr(w, http.StatusBadRequest, "bad bot id, before, after, around or limit")
		return
	}
	msgs, err := s.Store.ListBotMessagesPage(r.Context(), botID, page, int(limit))
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, msgs)
}

func (s *Server) listBotMedia(w http.ResponseWriter, r *http.Request) {
	botID, ok := pathID(r, "id")
	before, ok2 := queryInt(r, "before", 0, 0, 1<<62)
	limit, ok3 := queryInt(r, "limit", 50, 1, 100)
	if !ok || !ok2 || !ok3 {
		writeErr(w, http.StatusBadRequest, "bad bot id, before or limit")
		return
	}
	msgs, err := s.Store.ListBotMedia(r.Context(), botID, r.URL.Query().Get("type"), before, int(limit))
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, msgs)
}

func (s *Server) getMessage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad message id")
		return
	}
	v, err := s.Store.GetMessageView(r.Context(), id)
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) getArticle(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad message id")
		return
	}
	a, err := s.Store.GetArticle(r.Context(), id)
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) deleteMessage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad message id")
		return
	}
	chatID, orphans, err := s.Store.DeleteMessage(r.Context(), id, s.Now().Unix())
	if err != nil {
		storeErr(w, err)
		return
	}
	downloader.RemoveFiles(s.MediaDir, orphans)
	s.Hub.Publish(events.Event{Type: "message.deleted", Data: map[string]int64{"chat_id": chatID, "message_id": id}})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) retryMedia(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad media id")
		return
	}
	if err := s.Store.ResetMedia(r.Context(), id); err != nil {
		if err == store.ErrNotFound {
			writeErr(w, http.StatusConflict, "media is not in failed state")
			return
		}
		storeErr(w, err)
		return
	}
	s.Downloader.Wake()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	ch, cancel := s.Hub.Subscribe()
	defer cancel()
	// Heartbeats are named events, not SSE comments: the page cannot see comments, and it needs
	// to notice a stream that a frozen background tab left silently dead.
	const pingEvent = "event: ping\ndata: {}\n\n"
	fmt.Fprint(w, pingEvent)
	fl.Flush()
	ping := time.NewTicker(s.pingEvery())
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e := <-ch:
			data, err := jsonMarshal(e.Data)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, data)
			fl.Flush()
		case <-ping.C:
			fmt.Fprint(w, pingEvent)
			fl.Flush()
		}
	}
}

func (s *Server) serveMedia(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad media id")
		return
	}
	m, err := s.Store.GetMedia(r.Context(), id)
	if err != nil || m.State != store.StateDone {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	// ?compat=1 plays the browser-playable copy of a video; downloads always get the original.
	compat := r.URL.Query().Get("compat") == "1"
	if compat && m.CompatState != store.CompatDone {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	rel, etag := m.Path, fmt.Sprintf(`"m%d"`, m.ID)
	if compat {
		rel, etag = m.CompatPath, fmt.Sprintf(`"c%d"`, m.ID)
	}
	p, ok := within(s.MediaDir, rel)
	if !ok {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	f, err := os.Open(p)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	ct := m.Mime
	if compat {
		ct = "video/mp4"
	}
	if ct == "" {
		ct = mime.TypeByExtension(filepath.Ext(p))
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	inline := inlineSafe(ct)
	if !inline {
		ct = "application/octet-stream"
	}
	h := w.Header()
	h.Set("Content-Type", ct)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", mediaCSP)
	h.Set("Cache-Control", "private, max-age=31536000, immutable")
	h.Set("ETag", etag)
	if !inline || (!compat && r.URL.Query().Get("download") == "1") {
		name := m.FileName
		if name == "" {
			name = filepath.Base(p)
		}
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	}
	http.ServeContent(w, r, "", st.ModTime(), f)
}

// mediaCSP keeps archived files from running script even if a browser renders them.
const mediaCSP = "default-src 'none'; img-src 'self'; media-src 'self'; style-src 'unsafe-inline'; sandbox"

// inlineSafe reports whether a stored media type may be rendered by the browser. Anything else
// (SVG, HTML, XHTML, PDF, text, ...) could carry script and is served as an opaque download.
func inlineSafe(ct string) bool {
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return false
	}
	switch mt {
	case "image/jpeg", "image/png", "image/gif", "image/webp", "application/x-tgsticker":
		return true
	}
	return strings.HasPrefix(mt, "video/") || strings.HasPrefix(mt, "audio/")
}

func (s *Server) serveAvatar(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	id, ok := pathID(r, "id")
	if !ok || (kind != "bots" && kind != "senders" && kind != "channels") {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	p := filepath.Join(s.AvatarDir, kind, fmt.Sprintf("%d.jpg", id))
	f, err := os.Open(p)
	if err != nil && kind == "channels" && s.Watcher != nil {
		// Channel photos are fetched on first use (the picker shows channels never seen before).
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		_, ferr := s.Watcher.ChannelPhoto(ctx, id)
		cancel()
		if ferr == nil {
			f, err = os.Open(p)
		}
	}
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, "", st.ModTime(), f)
}

// within joins rel onto base and refuses results that escape base.
func within(base, rel string) (string, bool) {
	p := filepath.Join(base, rel)
	r, err := filepath.Rel(base, p)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", false
	}
	return p, true
}

func (s *Server) markRead(w http.ResponseWriter, r *http.Request) {
	chatID, ok := pathID(r, "id")
	var req struct {
		MessageID int64 `json:"message_id"`
	}
	if !ok || json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req) != nil || req.MessageID <= 0 {
		writeErr(w, http.StatusBadRequest, "bad chat id or message_id")
		return
	}
	if err := s.Store.MarkRead(r.Context(), chatID, req.MessageID); err != nil {
		storeErr(w, err)
		return
	}
	s.Hub.Publish(events.Event{Type: "chat.read", Data: map[string]int64{"chat_id": chatID}})
	w.WriteHeader(http.StatusNoContent)
}
