package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"tgarchive/internal/events"
	"tgarchive/internal/store"
)

func (s *Server) favoriteRoutes(mux *http.ServeMux) {
	mux.HandleFunc("PUT /api/messages/{id}/favorite", s.putFavorite)
	mux.HandleFunc("DELETE /api/messages/{id}/favorite", s.deleteFavorite)
	mux.HandleFunc("PUT /api/messages/{id}/tags", s.putTags)
	mux.HandleFunc("GET /api/favorites", s.listFavorites)
	mux.HandleFunc("GET /api/tags", s.listTags)
	mux.HandleFunc("DELETE /api/tags/{id}", s.deleteTag)
}

type tagsBody struct {
	Tags []string `json:"tags"`
}

// readTags reads an optional {"tags": [...]} body; an empty body gives nil tags.
func readTags(w http.ResponseWriter, r *http.Request) ([]string, bool) {
	var b tagsBody
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&b)
	if err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return nil, false
	}
	return b.Tags, true
}

func favoriteErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrBadTag):
		writeErr(w, http.StatusBadRequest, "标签须为 1–32 个字符、不含换行，每条消息最多 10 个")
	case errors.Is(err, store.ErrNotFavorite):
		writeErr(w, http.StatusConflict, "message is not a favorite")
	default:
		storeErr(w, err)
	}
}

// favoriteChanged tells the WebUI a message's favorite state changed.
func (s *Server) favoriteChanged(chatID, msgID int64) {
	s.Hub.Publish(events.Event{Type: "message.updated", Data: map[string]int64{"chat_id": chatID, "message_id": msgID}})
	s.Hub.Publish(events.Event{Type: "favorites.updated", Data: nil})
}

func (s *Server) putFavorite(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad message id")
		return
	}
	tags, ok := readTags(w, r)
	if !ok {
		return
	}
	info, chat, err := s.Store.Favorite(r.Context(), id, s.Now().Unix(), tags)
	if err != nil {
		favoriteErr(w, err)
		return
	}
	s.favoriteChanged(chat, id)
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) deleteFavorite(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad message id")
		return
	}
	chat, err := s.Store.Unfavorite(r.Context(), id)
	if err != nil {
		favoriteErr(w, err)
		return
	}
	s.favoriteChanged(chat, id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) putTags(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad message id")
		return
	}
	tags, ok := readTags(w, r)
	if !ok {
		return
	}
	refs, chat, err := s.Store.SetTags(r.Context(), id, tags)
	if err != nil {
		favoriteErr(w, err)
		return
	}
	s.favoriteChanged(chat, id)
	writeJSON(w, http.StatusOK, map[string]any{"tags": refs})
}

func (s *Server) listFavorites(w http.ResponseWriter, r *http.Request) {
	tag, ok1 := queryInt(r, "tag", 0, 0, 1<<62)
	before, ok2 := queryInt(r, "before", 0, 0, 1<<62)
	limit, ok3 := queryInt(r, "limit", 30, 1, 100)
	if !ok1 || !ok2 || !ok3 {
		writeErr(w, http.StatusBadRequest, "bad tag, before or limit")
		return
	}
	items, err := s.Store.ListFavorites(r.Context(), tag, before, int(limit))
	if err != nil {
		storeErr(w, err)
		return
	}
	var next int64
	if len(items) == int(limit) {
		next = items[len(items)-1].FavID
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next": next})
}

func (s *Server) listTags(w http.ResponseWriter, r *http.Request) {
	tags, err := s.Store.ListTags(r.Context())
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tags)
}

func (s *Server) deleteTag(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad tag id")
		return
	}
	if err := s.Store.DeleteTag(r.Context(), id); err != nil {
		storeErr(w, err)
		return
	}
	s.Hub.Publish(events.Event{Type: "favorites.updated", Data: nil})
	w.WriteHeader(http.StatusNoContent)
}
