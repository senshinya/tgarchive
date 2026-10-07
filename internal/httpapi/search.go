package httpapi

import (
	"errors"
	"net/http"

	"tgarchive/internal/store"
)

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	conv, ok1 := queryInt(r, "chat", 0, -(1 << 62), 1<<62)
	before, ok2 := queryInt(r, "before", 0, 0, 1<<62)
	limit, ok3 := queryInt(r, "limit", 30, 1, 100)
	if !ok1 || !ok2 || !ok3 {
		writeErr(w, http.StatusBadRequest, "bad chat, before or limit")
		return
	}
	hits, err := s.Store.Search(r.Context(), q, conv, before, int(limit))
	if errors.Is(err, store.ErrBadQuery) {
		writeErr(w, http.StatusBadRequest, "empty query")
		return
	}
	if err != nil {
		storeErr(w, err)
		return
	}
	var next int64
	if len(hits) == int(limit) {
		next = hits[len(hits)-1].Message.ID
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": hits, "next": next})
}
