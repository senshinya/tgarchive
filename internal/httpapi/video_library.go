package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"tgarchive/internal/events"
	"tgarchive/internal/store"
)

func (s *Server) videoLibraryRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/video-library", s.listVideoLibrary)
	mux.HandleFunc("GET /api/video-library/{id}", s.getVideoLibraryItem)
	mux.HandleFunc("PATCH /api/video-library/{id}", s.patchVideoLibraryItem)
	mux.HandleFunc("PUT /api/video-library/media/{id}/progress", s.putVideoProgress)
}
func videoLibraryErr(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrBadVideoLibrary) {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	storeErr(w, err)
}
func (s *Server) listVideoLibrary(w http.ResponseWriter, r *http.Request) {
	chat, ok := queryInt(r, "chat_id", 0, 0, 1<<62)
	tag, ok2 := queryInt(r, "tag_id", 0, 0, 1<<62)
	limit, ok3 := queryInt(r, "limit", 24, 1, 100)
	if !ok || !ok2 || !ok3 {
		writeErr(w, http.StatusBadRequest, "bad chat_id, tag_id or limit")
		return
	}
	q := r.URL.Query()
	page, err := s.Store.ListVideoLibrary(r.Context(), store.VideoLibraryQuery{Q: q.Get("q"), ChatID: chat, TagID: tag, Kind: q.Get("kind"), Watched: q.Get("watched"), View: q.Get("view"), Sort: q.Get("sort"), Cursor: q.Get("cursor"), Limit: int(limit)})
	if err != nil {
		videoLibraryErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}
func (s *Server) getVideoLibraryItem(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad item id")
		return
	}
	item, err := s.Store.GetVideoLibraryItem(r.Context(), id)
	if err != nil {
		videoLibraryErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// These writes share the server's Host/auth/JSON Origin middleware. Limit the body and reject
// trailing JSON, so a malformed request never partially writes progress or metadata.
func readVideoLibraryJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return false
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return false
	}
	return true
}
func (s *Server) putVideoProgress(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad media id")
		return
	}
	var body *struct {
		Position  float64 `json:"position"`
		Duration  float64 `json:"duration"`
		Completed bool    `json:"completed"`
	}
	if !readVideoLibraryJSON(w, r, &body) {
		return
	}
	if body == nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	p, err := s.Store.SaveVideoProgress(r.Context(), id, store.VideoProgress{Position: body.Position, Duration: body.Duration, Completed: body.Completed}, s.Now().Unix())
	if err != nil {
		videoLibraryErr(w, err)
		return
	}
	if s.Hub != nil {
		s.Hub.Publish(events.Event{Type: "video-library.updated", Data: map[string]int64{"media_id": id}})
	}
	writeJSON(w, http.StatusOK, p)
}
func (s *Server) patchVideoLibraryItem(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad item id")
		return
	}
	var body *store.VideoLibraryUpdate
	if !readVideoLibraryJSON(w, r, &body) {
		return
	}
	if body == nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	item, err := s.Store.UpdateVideoLibraryItem(r.Context(), id, *body)
	if err != nil {
		videoLibraryErr(w, err)
		return
	}
	if s.Hub != nil {
		s.Hub.Publish(events.Event{Type: "video-library.updated", Data: map[string]int64{"message_id": item.ID}})
	}
	writeJSON(w, http.StatusOK, item)
}
