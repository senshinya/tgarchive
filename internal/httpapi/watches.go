package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"tgarchive/internal/downloader"
	"tgarchive/internal/events"
	"tgarchive/internal/store"
	"tgarchive/internal/userbot"
	"tgarchive/internal/watchcond"
)

// WatchService is the channel watcher (userbot.Watcher).
type WatchService interface {
	Channels(ctx context.Context, refresh bool) (userbot.ChannelList, error)
	Search(ctx context.Context, q string) ([]userbot.ChannelInfo, error)
	Resolve(ctx context.Context, input string) (*userbot.ChannelInfo, error)
	Known(ctx context.Context, id int64) (*store.Channel, error)
	Test(ctx context.Context, id int64, cond *watchcond.Node) (*userbot.TestResult, error)
	InitialLastSeen(ctx context.Context, id int64) (int64, error)
	ChannelPhoto(ctx context.Context, id int64) (string, error)
	Backfill(ctx context.Context, watchID int64, hours int) (*userbot.BackfillState, error)
	RefreshPosts(ctx context.Context, chatID int64, ids []int64) error
	RefreshComments(ctx context.Context, chatID, rootID int64) error
	CommenterPhoto(ctx context.Context, kind string, id int64) (string, error)
	BackfillState(watchID int64) *userbot.BackfillState
	Wake()
}

const (
	// Long enough for a dialogs scan that waits out Telegram's short FLOOD_WAITs.
	watchCallTimeout = 90 * time.Second
	maxWindow        = 1440
	maxRefreshPosts  = 100
	// A dialogs rescan pages through the whole account; asking again sooner gets the cached list.
	channelsRefreshEvery = 5 * time.Minute
)

func (s *Server) watchRoutes(mux *http.ServeMux) {
	if s.Watcher == nil {
		return
	}
	mux.HandleFunc("GET /api/admin/channels", s.listChannels)
	mux.HandleFunc("POST /api/admin/channels/refresh", s.refreshChannels)
	mux.HandleFunc("GET /api/admin/channels/search", s.searchChannels)
	mux.HandleFunc("POST /api/admin/channels/resolve", s.resolveChannel)
	mux.HandleFunc("POST /api/admin/watches/test", s.testWatch)
	mux.HandleFunc("GET /api/admin/watches", s.listWatches)
	mux.HandleFunc("POST /api/admin/watches", s.createWatch)
	mux.HandleFunc("GET /api/admin/watches/{id}", s.getWatch)
	mux.HandleFunc("PUT /api/admin/watches/{id}", s.updateWatch)
	mux.HandleFunc("DELETE /api/admin/watches/{id}", s.deleteWatch)
	mux.HandleFunc("POST /api/admin/watches/{id}/backfill", s.backfillWatch)
	mux.HandleFunc("GET /api/admin/watch-settings", s.getWatchSettings)
	mux.HandleFunc("PUT /api/admin/watch-settings", s.putWatchSettings)
	mux.HandleFunc("POST /api/chats/{id}/refresh-stats", s.refreshPostStats)
	mux.HandleFunc("POST /api/chats/{id}/posts/{mid}/refresh-comments", s.refreshComments)
}

// refreshComments asks the watcher to re-read an archived post and its new comments (its comments
// are being opened). It answers at once; what changed arrives as message.updated and comments.updated.
func (s *Server) refreshComments(w http.ResponseWriter, r *http.Request) {
	chatID, ok := pathID(r, "id")
	rootID, ok2 := pathID(r, "mid")
	if !ok || !ok2 {
		writeErr(w, http.StatusBadRequest, "bad chat id or message id")
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), watchCallTimeout)
		defer cancel()
		err := s.Watcher.RefreshComments(ctx, chatID, rootID)
		if err != nil && !errors.Is(err, store.ErrNotFound) && !errors.Is(err, userbot.ErrNotReady) {
			log.Printf("refresh comments of message %d: %v", rootID, err)
		}
	}()
	w.WriteHeader(http.StatusAccepted)
}

// refreshPostStats asks the watcher to re-read the counters of the archived posts a conversation
// shows. It answers at once; the new numbers arrive as message.updated events.
func (s *Server) refreshPostStats(w http.ResponseWriter, r *http.Request) {
	chatID, ok := pathID(r, "id")
	var req struct {
		MessageIDs []int64 `json:"message_ids"`
	}
	if !ok || json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&req) != nil ||
		len(req.MessageIDs) == 0 || len(req.MessageIDs) > maxRefreshPosts {
		writeErr(w, http.StatusBadRequest, "bad chat id or message_ids")
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), watchCallTimeout)
		defer cancel()
		err := s.Watcher.RefreshPosts(ctx, chatID, req.MessageIDs)
		if err != nil && !errors.Is(err, store.ErrNotFound) && !errors.Is(err, userbot.ErrNotReady) {
			log.Printf("refresh post stats of chat %d: %v", chatID, err)
		}
	}()
	w.WriteHeader(http.StatusAccepted)
}

// watchErr maps watcher errors: the account not being usable is a conflict, everything else the
// watcher returns is already a user-facing reason.
func watchErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, userbot.ErrNotReady):
		writeErr(w, http.StatusConflict, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		writeErr(w, http.StatusGatewayTimeout, "Telegram 响应超时，请稍后重试")
	default:
		writeErr(w, http.StatusBadRequest, err.Error())
	}
}

type channelItem struct {
	userbot.ChannelInfo
	Watched bool `json:"watched"`
}

func (s *Server) withWatched(ctx context.Context, list []userbot.ChannelInfo) ([]channelItem, error) {
	watches, err := s.Store.ListWatches(ctx)
	if err != nil {
		return nil, err
	}
	watched := map[int64]bool{}
	for _, wv := range watches {
		watched[wv.ChannelID] = true
	}
	out := make([]channelItem, 0, len(list))
	for _, c := range list {
		out = append(out, channelItem{ChannelInfo: c, Watched: watched[c.ChannelID]})
	}
	return out, nil
}

// listChannels answers with the cached channel list. It never forces a rescan: a GET can be fired
// by any page (an <img>), and a rescan costs Telegram requests. The watcher still fills an empty or
// expired cache on its own schedule.
func (s *Server) listChannels(w http.ResponseWriter, r *http.Request) {
	s.writeChannels(w, r, false)
}

// refreshChannels asks for a rescan of the account's dialogs, at most every channelsRefreshEvery;
// sooner, it answers like listChannels. The scan runs in the background (loading is true meanwhile).
func (s *Server) refreshChannels(w http.ResponseWriter, r *http.Request) {
	s.channelsMu.Lock()
	refresh := s.Now().Sub(s.channelsRefreshed) >= channelsRefreshEvery
	s.channelsMu.Unlock()
	if s.writeChannels(w, r, refresh) && refresh {
		s.channelsMu.Lock()
		s.channelsRefreshed = s.Now()
		s.channelsMu.Unlock()
	}
}

// writeChannels answers with the channel list and reports whether the watcher took the request.
func (s *Server) writeChannels(w http.ResponseWriter, r *http.Request, refresh bool) bool {
	ctx, cancel := context.WithTimeout(r.Context(), watchCallTimeout)
	defer cancel()
	list, err := s.Watcher.Channels(ctx, refresh)
	if err != nil {
		watchErr(w, err)
		return false
	}
	items, err := s.withWatched(r.Context(), list.Channels)
	if err != nil {
		storeErr(w, err)
		return false
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": items, "loading": list.Loading, "updated_at": list.UpdatedAt, "error": list.Error})
	return true
}

func (s *Server) searchChannels(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if n := utf8.RuneCountInString(q); n < 2 || n > 64 {
		writeErr(w, http.StatusBadRequest, "搜索词须为 2–64 个字符")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), watchCallTimeout)
	defer cancel()
	list, err := s.Watcher.Search(ctx, q)
	if err != nil {
		watchErr(w, err)
		return
	}
	out, err := s.withWatched(r.Context(), list)
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) resolveChannel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Input string `json:"input"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	if req.Input == "" || len(req.Input) > 200 {
		writeErr(w, http.StatusBadRequest, "请输入 @用户名 或 t.me 链接")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), watchCallTimeout)
	defer cancel()
	c, err := s.Watcher.Resolve(ctx, req.Input)
	if err != nil {
		watchErr(w, err)
		return
	}
	out, err := s.withWatched(r.Context(), []userbot.ChannelInfo{*c})
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out[0])
}

// decodeCond reads a condition tree; raw null or absent yields nil when allowNil.
func decodeCond(w http.ResponseWriter, raw json.RawMessage, allowNil bool) (*watchcond.Node, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		if allowNil {
			return nil, true
		}
		writeErr(w, http.StatusBadRequest, "请设置条件")
		return nil, false
	}
	n, err := watchcond.Parse(raw)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	return n, true
}

func decodeWatchBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return false
	}
	return true
}

func (s *Server) testWatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChannelID int64           `json:"channel_id"`
		Cond      json.RawMessage `json:"cond"`
	}
	if !decodeWatchBody(w, r, &req) {
		return
	}
	if req.ChannelID <= 0 {
		writeErr(w, http.StatusBadRequest, "bad channel id")
		return
	}
	cond, ok := decodeCond(w, req.Cond, true)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), watchCallTimeout)
	defer cancel()
	res, err := s.Watcher.Test(ctx, req.ChannelID, cond)
	if err != nil {
		watchErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

type watchJSON struct {
	ID            int64             `json:"id"`
	Channel       store.ChannelView `json:"channel"`
	ChatID        int64             `json:"chat_id"`
	WindowMinutes int               `json:"window_minutes"`
	Cond          json.RawMessage   `json:"cond"`
	Enabled       bool              `json:"enabled"`
	Status        string            `json:"status"`
	Error         string            `json:"error"`
	Pending       int64             `json:"pending"`
	Hits          int64             `json:"hits"`
	CreatedAt     int64             `json:"created_at"`
	UpdatedAt     int64             `json:"updated_at"`
	LastPolledAt  int64             `json:"last_polled_at"`
	Hits24h       int64             `json:"hits_24h"`
	Hits7d        int64             `json:"hits_7d"`
	LastHitAt     int64             `json:"last_hit_at"`
	// PollSeconds is the current poll interval, against which the WebUI judges a stalled watch.
	PollSeconds int `json:"poll_seconds"`
	// Backfill is the latest manual backfill; null when none ran since the server started.
	Backfill *userbot.BackfillState `json:"backfill"`
}

// watchInfo is what every watch's JSON shares: recent hits by watch and the poll interval.
type watchInfo struct {
	activity map[int64]store.WatchActivity
	poll     int
}

func (s *Server) loadWatchInfo(ctx context.Context) (watchInfo, error) {
	act, err := s.Store.WatchActivity(ctx, s.Now().Unix())
	if err != nil {
		return watchInfo{}, err
	}
	poll, err := s.pollSeconds(ctx)
	return watchInfo{act, poll}, err
}

func (s *Server) watchJSON(v *store.WatchView, info watchInfo) watchJSON {
	j := toWatchJSON(v)
	a := info.activity[v.ID]
	j.LastPolledAt, j.Hits24h, j.Hits7d, j.LastHitAt, j.PollSeconds = v.LastPolledAt, a.Hits24h, a.Hits7d, a.LastHitAt, info.poll
	j.Backfill = s.Watcher.BackfillState(v.ID)
	return j
}

// writeWatch answers with one watch.
func (s *Server) writeWatch(w http.ResponseWriter, r *http.Request, code int, v *store.WatchView) {
	info, err := s.loadWatchInfo(r.Context())
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, code, s.watchJSON(v, info))
}

func toWatchJSON(v *store.WatchView) watchJSON {
	cond := json.RawMessage(v.Cond)
	if !json.Valid(cond) {
		cond = json.RawMessage("null")
	}
	return watchJSON{
		ID: v.ID, ChatID: v.ChatID, WindowMinutes: v.WindowMinutes, Cond: cond, Enabled: v.Enabled, Status: v.Status,
		Error: v.LastError, Pending: v.Pending, Hits: v.Hits, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt,
		Channel: store.ChannelView{ChannelID: v.ChannelID, Title: v.Channel.Title, Username: v.Channel.Username, HasAvatar: v.Channel.AvatarPath != ""},
	}
}

func (s *Server) listWatches(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListWatches(r.Context())
	if err != nil {
		storeErr(w, err)
		return
	}
	info, err := s.loadWatchInfo(r.Context())
	if err != nil {
		storeErr(w, err)
		return
	}
	out := make([]watchJSON, 0, len(list))
	for i := range list {
		out = append(out, s.watchJSON(&list[i], info))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getWatch(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad watch id")
		return
	}
	v, err := s.Store.GetWatch(r.Context(), id)
	if err != nil {
		storeErr(w, err)
		return
	}
	s.writeWatch(w, r, http.StatusOK, v)
}

func (s *Server) backfillWatch(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad watch id")
		return
	}
	var req struct {
		Hours int `json:"hours"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	if req.Hours < 1 || req.Hours > userbot.MaxBackfillHours {
		writeErr(w, http.StatusBadRequest, "回溯时长须为 1–720 小时")
		return
	}
	st, err := s.Watcher.Backfill(r.Context(), id, req.Hours)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not found")
	case errors.Is(err, userbot.ErrNotReady):
		writeErr(w, http.StatusConflict, err.Error())
	case err != nil:
		writeErr(w, http.StatusConflict, err.Error())
	default:
		writeJSON(w, http.StatusAccepted, st)
	}
}

type watchBody struct {
	ChannelID     int64           `json:"channel_id"`
	WindowMinutes int             `json:"window_minutes"`
	Cond          json.RawMessage `json:"cond"`
	Enabled       bool            `json:"enabled"`
}

func (s *Server) checkWatchBody(w http.ResponseWriter, b *watchBody) bool {
	if b.WindowMinutes < 1 || b.WindowMinutes > maxWindow {
		writeErr(w, http.StatusBadRequest, "观察窗口须为 1–1440 分钟")
		return false
	}
	_, ok := decodeCond(w, b.Cond, false)
	return ok
}

func (s *Server) createWatch(w http.ResponseWriter, r *http.Request) {
	var b watchBody
	if !decodeWatchBody(w, r, &b) {
		return
	}
	if b.ChannelID <= 0 {
		writeErr(w, http.StatusBadRequest, "请选择频道")
		return
	}
	if !s.checkWatchBody(w, &b) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), watchCallTimeout)
	defer cancel()
	ch, err := s.Watcher.Known(ctx, b.ChannelID)
	if err != nil {
		watchErr(w, err)
		return
	}
	// The channel must be readable. The poller sets the starting point itself, taking in the
	// posts published within the window (so it works the same when the account is offline now).
	if _, err := s.Watcher.InitialLastSeen(ctx, b.ChannelID); err != nil && !errors.Is(err, userbot.ErrNotReady) {
		watchErr(w, err)
		return
	}
	now := s.Now().Unix()
	if err := s.Store.UpsertChannel(r.Context(), *ch, now); err != nil {
		storeErr(w, err)
		return
	}
	id, err := s.Store.CreateWatch(r.Context(), &store.Watch{ChannelID: b.ChannelID, WindowMinutes: b.WindowMinutes, Cond: string(b.Cond),
		Enabled: b.Enabled, CreatedAt: now})
	if errors.Is(err, store.ErrExists) {
		writeErr(w, http.StatusConflict, "该频道已在监听")
		return
	}
	if err != nil {
		storeErr(w, err)
		return
	}
	channelID := b.ChannelID
	go func() {
		pctx, cancel := context.WithTimeout(context.Background(), watchCallTimeout)
		defer cancel()
		if _, err := s.Watcher.ChannelPhoto(pctx, channelID); err == nil {
			s.Hub.Publish(events.Event{Type: "watch.updated", Data: map[string]int64{"watch_id": id}})
		}
	}()
	s.Watcher.Wake()
	s.Hub.Publish(events.Event{Type: "watch.updated", Data: map[string]int64{"watch_id": id}})
	v, err := s.Store.GetWatch(r.Context(), id)
	if err != nil {
		storeErr(w, err)
		return
	}
	s.writeWatch(w, r, http.StatusCreated, v)
}

func (s *Server) updateWatch(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad watch id")
		return
	}
	var b watchBody
	if !decodeWatchBody(w, r, &b) || !s.checkWatchBody(w, &b) {
		return
	}
	if err := s.Store.UpdateWatch(r.Context(), id, b.WindowMinutes, string(b.Cond), b.Enabled, s.Now().Unix()); err != nil {
		storeErr(w, err)
		return
	}
	s.Watcher.Wake()
	s.Hub.Publish(events.Event{Type: "watch.updated", Data: map[string]int64{"watch_id": id}})
	v, err := s.Store.GetWatch(r.Context(), id)
	if err != nil {
		storeErr(w, err)
		return
	}
	s.writeWatch(w, r, http.StatusOK, v)
}

func (s *Server) deleteWatch(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad watch id")
		return
	}
	_, paths, err := s.Store.DeleteWatch(r.Context(), id, r.URL.Query().Get("purge") == "1")
	if err != nil {
		storeErr(w, err)
		return
	}
	downloader.RemoveFiles(s.MediaDir, paths)
	s.Hub.Publish(events.Event{Type: "watch.updated", Data: map[string]int64{"watch_id": id}})
	w.WriteHeader(http.StatusNoContent)
}

// pollSeconds is the stored poll interval, or the default.
func (s *Server) pollSeconds(ctx context.Context) (int, error) {
	v, err := s.Store.GetSetting(ctx, userbot.PollSettingKey)
	if errors.Is(err, store.ErrNotFound) {
		return userbot.DefaultPollSeconds, nil
	}
	if err != nil {
		return 0, err
	}
	if n, err := strconv.Atoi(string(v)); err == nil {
		return n, nil
	}
	return userbot.DefaultPollSeconds, nil
}

func (s *Server) getWatchSettings(w http.ResponseWriter, r *http.Request) {
	secs, err := s.pollSeconds(r.Context())
	if err != nil {
		storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"poll_seconds": secs})
}

func (s *Server) putWatchSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PollSeconds int `json:"poll_seconds"`
	}
	if !decodeSmall(w, r, &req) {
		return
	}
	if req.PollSeconds < userbot.MinPollSeconds || req.PollSeconds > userbot.MaxPollSeconds {
		writeErr(w, http.StatusBadRequest, "轮询间隔须为 30–600 秒")
		return
	}
	if err := s.Store.PutSetting(r.Context(), userbot.PollSettingKey, []byte(strconv.Itoa(req.PollSeconds)), s.Now().Unix()); err != nil {
		storeErr(w, err)
		return
	}
	s.Watcher.Wake()
	writeJSON(w, http.StatusOK, map[string]int{"poll_seconds": req.PollSeconds})
}
