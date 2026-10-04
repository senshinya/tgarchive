package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"tgarchive/internal/downloader"
	"tgarchive/internal/store"
	"tgarchive/internal/tgbot"
)

var tokenRe = regexp.MustCompile(`^\d+:[A-Za-z0-9_-]{30,}$`)

type step struct {
	Step   string `json:"step"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

func (s *Server) adminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/admin/bots", s.addBot)
	mux.HandleFunc("PATCH /api/admin/bots/{id}", s.patchBot)
	mux.HandleFunc("DELETE /api/admin/bots/{id}", s.deleteBot)
	mux.HandleFunc("GET /api/admin/bots/{id}/whitelist", s.listWhitelist)
	mux.HandleFunc("PUT /api/admin/bots/{id}/whitelist/{uid}", s.putWhitelist)
	mux.HandleFunc("DELETE /api/admin/bots/{id}/whitelist/{uid}", s.deleteWhitelist)
	mux.HandleFunc("GET /api/admin/bots/{id}/rejected", s.listRejected)
}

func (s *Server) addBot(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	token := strings.TrimSpace(req.Token)
	if !tokenRe.MatchString(token) {
		writeErr(w, http.StatusBadRequest, "token 格式不正确")
		return
	}
	if s.BotAPI != nil {
		if _, err := s.TgApp.Load(r.Context()); errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusConflict, "请先在设置中填写 api_id / api_hash")
			return
		} else if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	steps := []step{}

	me, err := tgbot.New(s.Cfg.BotAPIURL, token, s.HTTP).GetMe(ctx)
	if err != nil {
		steps = append(steps, step{"getMe", false, err.Error()})
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "token 校验失败", "steps": steps})
		return
	}
	steps = append(steps, step{"getMe", true, "@" + me.Username})

	if existing, err := s.Store.GetBotByTgID(ctx, me.ID); err == nil && existing.Status != store.StatusRemoved {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "机器人已存在", "bot_id": existing.ID, "steps": steps})
		return
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		storeErr(w, err)
		return
	}

	if err := tgbot.New(s.Cfg.CloudAPIURL, token, s.HTTP).LogOut(ctx); err != nil {
		steps = append(steps, step{"logOut", true, "云端未登出（可能已登出）：" + err.Error()})
	} else {
		steps = append(steps, step{"logOut", true, "已从云端登出"})
	}

	id, err := s.Store.UpsertBot(ctx, &store.Bot{
		TgBotID: me.ID, Username: me.Username, Name: me.FirstName,
		TokenEnc: s.Box.Seal([]byte(token)), CreatedAt: s.Now().Unix(),
	})
	if err != nil {
		storeErr(w, err)
		return
	}
	s.Clients.Forget(id)
	s.Manager.Start(id)
	steps = append(steps, step{"start", true, ""})
	if s.Avatars != nil {
		go s.Avatars.RefreshBot(context.WithoutCancel(r.Context()), id)
	}
	writeJSON(w, http.StatusOK, map[string]any{"bot_id": id, "steps": steps})
}

func (s *Server) patchBot(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if !ok || json.NewDecoder(r.Body).Decode(&req) != nil || req.Enabled == nil {
		writeErr(w, http.StatusBadRequest, `body must be {"enabled": bool}`)
		return
	}
	ctx := r.Context()
	if err := s.Store.SetBotEnabled(ctx, id, *req.Enabled); err != nil {
		storeErr(w, err)
		return
	}
	s.Manager.Stop(id) // also restarts cleanly when re-enabling a bot in error state
	if err := s.Store.SetBotStatus(ctx, id, store.StatusStopped, ""); err != nil {
		storeErr(w, err)
		return
	}
	if *req.Enabled {
		s.Clients.Forget(id)
		s.Manager.Start(id)
	}
	b, err := s.Store.GetBot(ctx, id)
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, botView(b))
}

func (s *Server) deleteBot(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad bot id")
		return
	}
	s.Manager.Stop(id)
	s.Clients.Forget(id)
	if r.URL.Query().Get("purge") == "1" {
		paths, err := s.Store.PurgeBot(r.Context(), id)
		if err != nil {
			storeErr(w, err)
			return
		}
		downloader.RemoveFiles(s.MediaDir, paths)
	} else if err := s.Store.RemoveBot(r.Context(), id); err != nil {
		storeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) requireBot(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad bot id")
		return 0, false
	}
	if _, err := s.Store.GetBot(r.Context(), id); err != nil {
		storeErr(w, err)
		return 0, false
	}
	return id, true
}

func (s *Server) listWhitelist(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireBot(w, r)
	if !ok {
		return
	}
	entries, err := s.Store.ListWhitelist(r.Context(), id)
	if err != nil {
		storeErr(w, err)
		return
	}
	type view struct {
		TgUserID int64  `json:"tg_user_id"`
		Note     string `json:"note"`
		CanFetch bool   `json:"can_fetch"`
	}
	out := make([]view, 0, len(entries))
	for _, e := range entries {
		out = append(out, view{e.TgUserID, e.Note, e.CanFetch})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) putWhitelist(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireBot(w, r)
	if !ok {
		return
	}
	uid, err := strconv.ParseInt(r.PathValue("uid"), 10, 64)
	var req struct {
		Note     string `json:"note"`
		CanFetch bool   `json:"can_fetch"`
	}
	if err != nil || uid <= 0 || json.NewDecoder(r.Body).Decode(&req) != nil {
		writeErr(w, http.StatusBadRequest, "bad user id or body")
		return
	}
	if err := s.Store.PutWhitelist(r.Context(), store.WhitelistEntry{BotID: id, TgUserID: uid, Note: req.Note, CanFetch: req.CanFetch}); err != nil {
		storeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteWhitelist(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireBot(w, r)
	if !ok {
		return
	}
	uid, err := strconv.ParseInt(r.PathValue("uid"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad user id")
		return
	}
	if err := s.Store.DeleteWhitelist(r.Context(), id, uid); err != nil {
		storeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listRejected(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireBot(w, r)
	if !ok {
		return
	}
	rej, err := s.Store.ListRejected(r.Context(), id)
	if err != nil {
		storeErr(w, err)
		return
	}
	type view struct {
		TgUserID   int64  `json:"tg_user_id"`
		FirstName  string `json:"first_name"`
		Username   string `json:"username"`
		LastSeenAt int64  `json:"last_seen_at"`
		Count      int64  `json:"count"`
	}
	out := make([]view, 0, len(rej))
	for _, x := range rej {
		out = append(out, view{x.TgUserID, x.FirstName, x.Username, x.LastSeenAt, x.Count})
	}
	writeJSON(w, http.StatusOK, out)
}
