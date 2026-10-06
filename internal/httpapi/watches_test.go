package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"tgarchive/internal/config"
	"tgarchive/internal/events"
	"tgarchive/internal/seal"
	"tgarchive/internal/store"
	"tgarchive/internal/userbot"
	"tgarchive/internal/watchcond"
)

type fakeWatcher struct {
	mu        sync.Mutex
	ready     bool
	wakes     int
	tested    *watchcond.Node
	photos    int
	avatarDir string
}

var newsInfo = userbot.ChannelInfo{ChannelID: 500, Title: "News", Username: "news", Participants: 10}

func (f *fakeWatcher) err() error {
	if !f.ready {
		return userbot.ErrNotReady
	}
	return nil
}

func (f *fakeWatcher) Channels(context.Context, bool) ([]userbot.ChannelInfo, error) {
	return []userbot.ChannelInfo{newsInfo}, f.err()
}

func (f *fakeWatcher) Search(_ context.Context, q string) ([]userbot.ChannelInfo, error) {
	return []userbot.ChannelInfo{{ChannelID: 600, Title: q}}, f.err()
}

func (f *fakeWatcher) Resolve(_ context.Context, in string) (*userbot.ChannelInfo, error) {
	if in == "bad" {
		return nil, errors.New("无法识别，请输入 @用户名 或 t.me 链接")
	}
	c := newsInfo
	return &c, f.err()
}

func (f *fakeWatcher) Known(_ context.Context, id int64) (*store.Channel, error) {
	if id != 500 {
		return nil, errors.New("频道信息已过期，请重新搜索选择")
	}
	return &store.Channel{ChannelID: 500, Title: "News", Username: "news"}, nil
}

func (f *fakeWatcher) Test(_ context.Context, _ int64, cond *watchcond.Node) (*userbot.TestResult, error) {
	f.mu.Lock()
	f.tested = cond
	f.mu.Unlock()
	return &userbot.TestResult{Posts: []userbot.TestPost{{TgMessageID: 1}}}, f.err()
}

func (f *fakeWatcher) InitialLastSeen(context.Context, int64) (int64, error) {
	if !f.ready {
		return 0, userbot.ErrNotReady
	}
	return 42, nil
}

func (f *fakeWatcher) ChannelPhoto(_ context.Context, id int64) (string, error) {
	f.mu.Lock()
	f.photos++
	f.mu.Unlock()
	rel := filepath.Join("channels", "500.jpg")
	if id != 500 {
		return "", store.ErrNotFound
	}
	os.MkdirAll(filepath.Join(f.avatarDir, "channels"), 0o755)
	return rel, os.WriteFile(filepath.Join(f.avatarDir, rel), []byte("jpg"), 0o644)
}

func (f *fakeWatcher) Wake() {
	f.mu.Lock()
	f.wakes++
	f.mu.Unlock()
}

type watchEnv struct {
	h  http.Handler
	st *store.Store
	fw *fakeWatcher
}

func newWatchEnv(t *testing.T) *watchEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _ := seal.New(bytes.Repeat([]byte{1}, 32))
	dir := t.TempDir()
	fw := &fakeWatcher{ready: true, avatarDir: dir}
	srv := &Server{Cfg: &config.Config{RequireForwardAuth: true}, Store: st, Box: box, Watcher: fw, Hub: events.NewHub(),
		AvatarDir: dir, MediaDir: t.TempDir(), Now: func() time.Time { return time.Unix(1000, 0) }}
	return &watchEnv{h: srv.Handler(), st: st, fw: fw}
}

const condOK = `{"op":"and","items":[{"metric":"views","cmp":"gte","value":10}]}`

func watchReq(channel int64, window int, cond string) map[string]any {
	return map[string]any{"channel_id": channel, "window_minutes": window, "cond": json.RawMessage(cond), "enabled": true}
}

func TestWatchCRUD(t *testing.T) {
	e := newWatchEnv(t)
	w := call(e.h, "POST", "/api/admin/watches", watchReq(500, 30, condOK))
	if w.Code != 201 {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	var got watchJSON
	json.Unmarshal(w.Body.Bytes(), &got)
	if got.ID == 0 || got.Channel.Title != "News" || got.ChatID == 0 || got.WindowMinutes != 30 || string(got.Cond) != condOK {
		t.Fatalf("created = %+v", got)
	}
	if v, _ := e.st.GetWatch(context.Background(), got.ID); v.LastSeenID != 42 {
		t.Fatalf("last seen = %d", v.LastSeenID)
	}
	if w := call(e.h, "POST", "/api/admin/watches", watchReq(500, 30, condOK)); w.Code != 409 || !strings.Contains(w.Body.String(), "已在监听") {
		t.Fatalf("duplicate = %d %s", w.Code, w.Body)
	}
	for _, c := range []struct {
		body map[string]any
		want string
	}{
		{watchReq(500, 0, condOK), "观察窗口"},
		{watchReq(500, 1441, condOK), "观察窗口"},
		{watchReq(500, 30, `{"op":"and","items":[]}`), "条件组不能为空"},
		{watchReq(500, 30, `null`), "请设置条件"},
		{watchReq(0, 30, condOK), "请选择频道"},
		{watchReq(501, 30, condOK), "重新搜索"},
	} {
		if w := call(e.h, "POST", "/api/admin/watches", c.body); w.Code != 400 || !strings.Contains(w.Body.String(), c.want) {
			t.Errorf("%v = %d %s", c.body, w.Code, w.Body)
		}
	}
	path := "/api/admin/watches/" + strconv.FormatInt(got.ID, 10)
	if w := call(e.h, "PUT", path, watchReq(0, 60, condOK)); w.Code != 200 || !strings.Contains(w.Body.String(), `"window_minutes":60`) {
		t.Fatalf("update = %d %s", w.Code, w.Body)
	}
	if w := call(e.h, "GET", "/api/admin/watches", nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"title":"News"`) {
		t.Fatalf("list = %d %s", w.Code, w.Body)
	}
	if w := call(e.h, "GET", "/api/chats", nil); !strings.Contains(w.Body.String(), `"kind":"channel"`) {
		t.Fatalf("chats = %s", w.Body)
	}
	if w := call(e.h, "DELETE", path, nil); w.Code != 204 {
		t.Fatalf("delete = %d", w.Code)
	}
	if w := call(e.h, "GET", path, nil); w.Code != 404 {
		t.Fatalf("after delete = %d", w.Code)
	}
	// The conversation stays without purge.
	if w := call(e.h, "GET", "/api/chats", nil); !strings.Contains(w.Body.String(), `"kind":"channel"`) || !strings.Contains(w.Body.String(), `"watch":null`) {
		t.Fatalf("chats = %s", w.Body)
	}
	if e.fw.wakes < 2 {
		t.Fatalf("wakes = %d", e.fw.wakes)
	}
}

func TestWatchCreateWhileAccountOffline(t *testing.T) {
	e := newWatchEnv(t)
	e.fw.ready = false
	w := call(e.h, "POST", "/api/admin/watches", watchReq(500, 30, condOK))
	if w.Code != 201 {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	if w := call(e.h, "GET", "/api/admin/channels", nil); w.Code != 409 || !strings.Contains(w.Body.String(), "未登录") {
		t.Fatalf("channels offline = %d %s", w.Code, w.Body)
	}
}

func TestWatchPurge(t *testing.T) {
	e := newWatchEnv(t)
	w := call(e.h, "POST", "/api/admin/watches", watchReq(500, 30, condOK))
	var got watchJSON
	json.Unmarshal(w.Body.Bytes(), &got)
	if w := call(e.h, "DELETE", "/api/admin/watches/"+strconv.FormatInt(got.ID, 10)+"?purge=1", nil); w.Code != 204 {
		t.Fatalf("purge = %d", w.Code)
	}
	if w := call(e.h, "GET", "/api/chats", nil); strings.Contains(w.Body.String(), `"kind":"channel"`) {
		t.Fatalf("chats = %s", w.Body)
	}
}

func TestChannelPickerEndpoints(t *testing.T) {
	e := newWatchEnv(t)
	call(e.h, "POST", "/api/admin/watches", watchReq(500, 30, condOK))
	if w := call(e.h, "GET", "/api/admin/channels?refresh=1", nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"watched":true`) {
		t.Fatalf("channels = %d %s", w.Code, w.Body)
	}
	if w := call(e.h, "GET", "/api/admin/channels/search?q=a", nil); w.Code != 400 {
		t.Fatalf("short search = %d", w.Code)
	}
	if w := call(e.h, "GET", "/api/admin/channels/search?q=abc", nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"watched":false`) {
		t.Fatalf("search = %d %s", w.Code, w.Body)
	}
	if w := call(e.h, "POST", "/api/admin/channels/resolve", map[string]string{"input": "@news"}); w.Code != 200 || !strings.Contains(w.Body.String(), `"channel_id":500`) {
		t.Fatalf("resolve = %d %s", w.Code, w.Body)
	}
	if w := call(e.h, "POST", "/api/admin/channels/resolve", map[string]string{"input": "bad"}); w.Code != 400 || !strings.Contains(w.Body.String(), "无法识别") {
		t.Fatalf("resolve bad = %d %s", w.Code, w.Body)
	}
	if w := call(e.h, "POST", "/api/admin/watches/test", map[string]any{"channel_id": 500, "cond": nil}); w.Code != 200 || e.fw.tested != nil {
		t.Fatalf("test nil = %d %s", w.Code, w.Body)
	}
	if w := call(e.h, "POST", "/api/admin/watches/test", map[string]any{"channel_id": 500, "cond": json.RawMessage(condOK)}); w.Code != 200 || e.fw.tested == nil {
		t.Fatalf("test = %d %s", w.Code, w.Body)
	}
	if w := call(e.h, "POST", "/api/admin/watches/test", map[string]any{"channel_id": 500, "cond": json.RawMessage(`{"op":"x"}`)}); w.Code != 400 {
		t.Fatalf("test bad = %d", w.Code)
	}
}

func TestWatchSettings(t *testing.T) {
	e := newWatchEnv(t)
	if w := call(e.h, "GET", "/api/admin/watch-settings", nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"poll_seconds":60`) {
		t.Fatalf("get = %d %s", w.Code, w.Body)
	}
	for _, v := range []int{29, 601} {
		if w := call(e.h, "PUT", "/api/admin/watch-settings", map[string]int{"poll_seconds": v}); w.Code != 400 {
			t.Fatalf("put %d = %d", v, w.Code)
		}
	}
	if w := call(e.h, "PUT", "/api/admin/watch-settings", map[string]int{"poll_seconds": 120}); w.Code != 200 {
		t.Fatalf("put = %d", w.Code)
	}
	if w := call(e.h, "GET", "/api/admin/watch-settings", nil); !strings.Contains(w.Body.String(), `"poll_seconds":120`) {
		t.Fatalf("get = %s", w.Body)
	}
}

func TestChannelAvatarFetchedOnDemand(t *testing.T) {
	e := newWatchEnv(t)
	if w := do(e.h, "GET", "/avatars/channels/500", nil); w.Code != 200 || w.Body.String() != "jpg" || e.fw.photos != 1 {
		t.Fatalf("avatar = %d %q photos %d", w.Code, w.Body, e.fw.photos)
	}
	if w := do(e.h, "GET", "/avatars/channels/500", nil); w.Code != 200 || e.fw.photos != 1 {
		t.Fatal("a stored avatar should be served from disk")
	}
	if w := do(e.h, "GET", "/avatars/channels/7", nil); w.Code != 404 {
		t.Fatalf("missing = %d", w.Code)
	}
}
