package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"tgarchive/internal/userbot"
)

type UserbotService interface {
	Status(ctx context.Context) userbot.Info
	SendCode(ctx context.Context, phone string) error
	SignIn(ctx context.Context, code string) (string, error)
	Password(ctx context.Context, password string) error
	Logout(ctx context.Context) error
	Reload()
}

var (
	phoneRe = regexp.MustCompile(`^\+?[0-9]{5,20}$`)
	codeRe  = regexp.MustCompile(`^[0-9]{3,8}$`)
)

const loginTimeout = 30 * time.Second

func (s *Server) userbotRoutes(mux *http.ServeMux) {
	if s.Userbot == nil {
		return
	}
	mux.HandleFunc("GET /api/admin/userbot", s.userbotStatus)
	mux.HandleFunc("POST /api/admin/userbot/phone", s.userbotPhone)
	mux.HandleFunc("POST /api/admin/userbot/code", s.userbotCode)
	mux.HandleFunc("POST /api/admin/userbot/password", s.userbotPassword)
	mux.HandleFunc("POST /api/admin/userbot/logout", s.userbotLogout)
}

func (s *Server) userbotStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Userbot.Status(r.Context()))
}

func (s *Server) userbotPhone(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Phone string `json:"phone"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	phone := strings.Map(func(c rune) rune {
		switch c {
		case ' ', '-', '(', ')':
			return -1
		}
		return c
	}, req.Phone)
	if !phoneRe.MatchString(phone) {
		writeErr(w, http.StatusBadRequest, "手机号格式不正确")
		return
	}
	if !strings.HasPrefix(phone, "+") {
		phone = "+" + phone
	}
	ctx, cancel := context.WithTimeout(r.Context(), loginTimeout)
	defer cancel()
	if err := s.Userbot.SendCode(ctx, phone); err != nil {
		userbotErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.Userbot.Status(r.Context()))
}

func (s *Server) userbotCode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	code := strings.TrimSpace(req.Code)
	if !codeRe.MatchString(code) {
		writeErr(w, http.StatusBadRequest, "验证码格式不正确")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), loginTimeout)
	defer cancel()
	if _, err := s.Userbot.SignIn(ctx, code); err != nil {
		userbotErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.Userbot.Status(r.Context()))
}

func (s *Server) userbotPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	if req.Password == "" || len(req.Password) > 256 {
		writeErr(w, http.StatusBadRequest, "请输入二步验证密码")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), loginTimeout)
	defer cancel()
	if err := s.Userbot.Password(ctx, req.Password); err != nil {
		userbotErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.Userbot.Status(r.Context()))
}

func (s *Server) userbotLogout(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), loginTimeout)
	defer cancel()
	if err := s.Userbot.Logout(ctx); err != nil {
		userbotErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeSmall(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return false
	}
	return true
}

func userbotErr(w http.ResponseWriter, err error) {
	var ie *userbot.InputError
	switch {
	case errors.As(err, &ie):
		writeErr(w, http.StatusBadRequest, ie.Msg)
	case errors.Is(err, userbot.ErrNotConfigured), errors.Is(err, userbot.ErrBadState), errors.Is(err, userbot.ErrAlreadyLoggedIn):
		writeErr(w, http.StatusConflict, err.Error())
	case errors.Is(err, userbot.ErrNotConnected):
		writeErr(w, http.StatusServiceUnavailable, err.Error())
	default:
		writeErr(w, http.StatusBadGateway, err.Error())
	}
}
