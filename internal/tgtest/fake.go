// Package tgtest provides an in-process fake of the Telegram Bot API for tests.
package tgtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type Call struct {
	Token  string
	Method string
	Params map[string]any
}

type FakeTG struct {
	Server    *httptest.Server
	RemoteDir string

	mu         sync.Mutex
	me         map[string]any
	updates    []json.RawMessage // update_id = index + 1
	calls      []Call
	files      map[string][]byte
	avatars    map[int64]string
	lastOffset int64
	updErr     *apiErr
	badTokens  map[string]bool
	holdFiles  bool
}

type apiErr struct {
	code int
	desc string
}

func New(t testing.TB) *FakeTG {
	f := &FakeTG{
		RemoteDir: t.TempDir(),
		me:        map[string]any{"id": 777, "is_bot": true, "first_name": "Archive", "username": "archive_bot"},
		files:     map[string][]byte{},
		avatars:   map[int64]string{},
		badTokens: map[string]bool{},
	}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Server.Close)
	return f
}

func (f *FakeTG) URL() string { return f.Server.URL }

func (f *FakeTG) SetMe(id int64, username string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.me = map[string]any{"id": id, "is_bot": true, "first_name": "Archive", "username": username}
}

func (f *FakeTG) AddFile(fileID string, content []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[fileID] = content
}

func (f *FakeTG) SetAvatar(userID int64, fileID string, content []byte) {
	f.AddFile(fileID, content)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.avatars[userID] = fileID
}

func (f *FakeTG) PushMessage(msg string) int64 { return f.push("message", msg) }
func (f *FakeTG) PushEdited(msg string) int64  { return f.push("edited_message", msg) }

func (f *FakeTG) push(field, msg string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := int64(len(f.updates) + 1)
	f.updates = append(f.updates, json.RawMessage(fmt.Sprintf(`{"update_id":%d,%q:%s}`, id, field, msg)))
	return id
}

func (f *FakeTG) FailUpdates(code int, desc string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updErr = &apiErr{code, desc}
}

func (f *FakeTG) RejectToken(token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.badTokens[token] = true
}

// HoldFiles makes getFile block until released or the request is cancelled,
// simulating a download in progress.
func (f *FakeTG) HoldFiles(on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.holdFiles = on
}

func (f *FakeTG) Calls(method string) []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Call
	for _, c := range f.calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func (f *FakeTG) LastOffset() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastOffset
}

func (f *FakeTG) serve(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/bot")
	i := strings.LastIndex(rest, "/")
	if i < 0 {
		http.NotFound(w, r)
		return
	}
	token, method := rest[:i], rest[i+1:]
	params := map[string]any{}
	_ = json.NewDecoder(r.Body).Decode(&params)

	f.mu.Lock()
	f.calls = append(f.calls, Call{Token: token, Method: method, Params: params})
	bad := f.badTokens[token]
	me := f.me
	f.mu.Unlock()
	if bad {
		fail(w, 401, "Unauthorized")
		return
	}

	switch method {
	case "getMe":
		ok(w, me)
	case "logOut", "setMessageReaction":
		ok(w, true)
	case "sendMessage":
		ok(w, map[string]any{"message_id": 1, "date": 0, "chat": map[string]any{"id": params["chat_id"], "type": "private"}})
	case "getUpdates":
		f.getUpdates(w, r, params)
	case "getFile":
		f.getFile(w, r, token, params)
	case "getUserProfilePhotos":
		f.mu.Lock()
		fid, has := f.avatars[int64(num(params["user_id"]))]
		f.mu.Unlock()
		if !has {
			ok(w, map[string]any{"total_count": 0, "photos": []any{}})
			return
		}
		ok(w, map[string]any{"total_count": 1, "photos": [][]map[string]any{{
			{"file_id": fid, "file_unique_id": "u" + fid, "width": 160, "height": 160},
			{"file_id": fid, "file_unique_id": "u" + fid, "width": 640, "height": 640},
		}}})
	default:
		fail(w, 404, "Not Found: method not found")
	}
}

func (f *FakeTG) getUpdates(w http.ResponseWriter, r *http.Request, params map[string]any) {
	offset := int64(num(params["offset"]))
	deadline := time.Now().Add(200 * time.Millisecond)
	for {
		f.mu.Lock()
		f.lastOffset = offset
		if f.updErr != nil {
			e := *f.updErr
			f.mu.Unlock()
			fail(w, e.code, e.desc)
			return
		}
		out := []json.RawMessage{}
		for i, u := range f.updates {
			if int64(i+1) >= offset {
				out = append(out, u)
			}
		}
		f.mu.Unlock()
		if len(out) > 0 || time.Now().After(deadline) {
			ok(w, out)
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (f *FakeTG) getFile(w http.ResponseWriter, r *http.Request, token string, params map[string]any) {
	for {
		f.mu.Lock()
		hold := f.holdFiles
		f.mu.Unlock()
		if !hold {
			break
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(20 * time.Millisecond):
		}
	}
	fileID, _ := params["file_id"].(string)
	f.mu.Lock()
	content, has := f.files[fileID]
	f.mu.Unlock()
	if !has {
		fail(w, 400, "Bad Request: invalid file_id")
		return
	}
	dir := filepath.Join(f.RemoteDir, token, "documents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail(w, 500, err.Error())
		return
	}
	p := filepath.Join(dir, fileID+".jpg")
	if err := os.WriteFile(p, content, 0o644); err != nil {
		fail(w, 500, err.Error())
		return
	}
	ok(w, map[string]any{"file_id": fileID, "file_unique_id": "u" + fileID, "file_size": len(content), "file_path": p})
}

func ok(w http.ResponseWriter, result any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
}

func fail(w http.ResponseWriter, code int, desc string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": code, "description": desc})
}

func num(v any) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return 0
}

// TextMsg builds a private-chat text message from user `from`.
func TextMsg(id, from int64, text string) string {
	return fmt.Sprintf(`{"message_id":%d,"from":{"id":%d,"is_bot":false,"first_name":"User%d"},"chat":{"id":%d,"type":"private"},"date":%d,"text":%q}`,
		id, from, from, from, 1759500000+id, text)
}

// PhotoMsg builds a private-chat photo message. Callers must AddFile(fileID) and AddFile(fileID+"-s").
func PhotoMsg(id, from int64, fileID string) string {
	return fmt.Sprintf(`{"message_id":%d,"from":{"id":%d,"is_bot":false,"first_name":"User%d"},"chat":{"id":%d,"type":"private"},"date":%d,`+
		`"photo":[{"file_id":"%s-s","file_unique_id":"%s-s","width":90,"height":60,"file_size":2},{"file_id":"%s","file_unique_id":"%s","width":1280,"height":853,"file_size":4}]}`,
		id, from, from, from, 1759500000+id, fileID, fileID, fileID, fileID)
}
