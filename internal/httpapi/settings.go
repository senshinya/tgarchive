package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"tgarchive/internal/store"
	"tgarchive/internal/tgapp"
)

func (s *Server) settingsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/telegram-app", s.getTelegramApp)
	mux.HandleFunc("PUT /api/admin/telegram-app", s.putTelegramApp)
}

func (s *Server) getTelegramApp(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"configured": false, "api_id": 0}
	c, err := s.TgApp.Load(r.Context())
	switch {
	case err == nil:
		out["configured"], out["api_id"] = true, c.APIID
	case !errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	server := map[string]any{"managed": s.BotAPI != nil, "state": "", "error": ""}
	if s.BotAPI != nil {
		server["state"], server["error"] = s.BotAPI.Status()
	}
	out["server"] = server
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) putTelegramApp(w http.ResponseWriter, r *http.Request) {
	var c tgapp.Credentials
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := tgapp.Validate(c); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.TgApp.Save(r.Context(), c, s.Now().Unix()); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s.BotAPI != nil {
		s.BotAPI.Apply(c)
	}
	w.WriteHeader(http.StatusNoContent)
}
