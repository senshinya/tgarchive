// Package httpapi exposes the archive and bot administration over HTTP.
package httpapi

import (
	"encoding/json"
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"tgarchive/internal/avatars"
	"tgarchive/internal/botapiserver"
	"tgarchive/internal/botclients"
	"tgarchive/internal/collector"
	"tgarchive/internal/config"
	"tgarchive/internal/downloader"
	"tgarchive/internal/events"
	"tgarchive/internal/seal"
	"tgarchive/internal/store"
	"tgarchive/internal/tgapp"
)

type Server struct {
	Cfg        *config.Config
	Store      *store.Store
	Box        *seal.Box
	Clients    *botclients.Registry
	Manager    *collector.Manager
	Downloader *downloader.Downloader
	Hub        *events.Hub
	Avatars    *avatars.Refresher
	TgApp      *tgapp.Store
	BotAPI     *botapiserver.Supervisor // nil when the Bot API server is not managed by this process
	Userbot    UserbotService           // userbot login; nil disables /api/admin/userbot
	Web        fs.FS
	MediaDir   string
	AvatarDir  string
	HTTP       *http.Client
	Now        func() time.Time
	PingEvery  time.Duration // SSE heartbeat interval; 0 means defaultPingEvery
}

const defaultPingEvery = 25 * time.Second

func (s *Server) pingEvery() time.Duration {
	if s.PingEvery > 0 {
		return s.PingEvery
	}
	return defaultPingEvery
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /api/bots", s.listBots)
	mux.HandleFunc("GET /api/chats", s.listChats)
	mux.HandleFunc("GET /api/chats/{id}/messages", s.listMessages)
	mux.HandleFunc("GET /api/chats/{id}/media", s.listChatMedia)
	mux.HandleFunc("GET /api/bots/{id}/messages", s.listBotMessages)
	mux.HandleFunc("GET /api/bots/{id}/media", s.listBotMedia)
	mux.HandleFunc("GET /api/messages/{id}", s.getMessage)
	mux.HandleFunc("GET /api/messages/{id}/article", s.getArticle)
	mux.HandleFunc("DELETE /api/messages/{id}", s.deleteMessage)
	mux.HandleFunc("POST /api/media/{id}/retry", s.retryMedia)
	mux.HandleFunc("GET /api/events", s.events)
	mux.HandleFunc("GET /media/{id}", s.serveMedia)
	mux.HandleFunc("GET /avatars/{kind}/{id}", s.serveAvatar)
	s.adminRoutes(mux)
	s.settingsRoutes(mux)
	s.userbotRoutes(mux)
	mux.HandleFunc("GET /api/", func(w http.ResponseWriter, r *http.Request) { writeErr(w, http.StatusNotFound, "not found") })
	mux.Handle("GET /", s.spa())
	return s.auth(mux)
}

// auth is the in-app backstop behind Caddy forward_auth (which strips client-sent Remote-* headers).
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Cfg.RequireForwardAuth && r.URL.Path != "/healthz" && r.Header.Get("Remote-User") == "" {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if crossSite(r) {
			writeErr(w, http.StatusForbidden, "cross-site request rejected")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// crossSite is a CSRF backstop for state-changing requests: browsers label cross-origin requests
// with Sec-Fetch-Site, and a form or no-cors fetch cannot send an application/json body.
func crossSite(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		return true
	}
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		if r.ContentLength != 0 {
			mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			return err != nil || mt != "application/json"
		}
	}
	return false
}

func (s *Server) spa() http.Handler {
	files := http.FileServerFS(s.Web)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" && p != "index.html" {
			if st, err := fs.Stat(s.Web, p); err == nil && !st.IsDir() {
				files.ServeHTTP(w, r)
				return
			}
		}
		index, err := fs.ReadFile(s.Web, "index.html")
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "ui not built")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(index)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// storeErr maps store errors to HTTP codes.
func storeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not found")
	case errors.Is(err, store.ErrBadMediaType):
		writeErr(w, http.StatusBadRequest, err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, err.Error())
	}
}

func pathID(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	return id, err == nil && id > 0
}

func queryInt(r *http.Request, name string, def, lo, hi int64) (int64, bool) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def, true
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < lo || n > hi {
		return 0, false
	}
	return n, true
}

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }
