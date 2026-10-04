package tgbot_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tgarchive/internal/tgbot"
	"tgarchive/internal/tgtest"
)

const token = "777:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

var ctx = context.Background()

func TestGetMeAndMethods(t *testing.T) {
	f := tgtest.New(t)
	c := tgbot.New(f.URL(), token, nil)
	me, err := c.GetMe(ctx)
	if err != nil || me.ID != 777 || me.Username != "archive_bot" {
		t.Fatalf("GetMe = %+v, %v", me, err)
	}
	if err := c.SetMessageReaction(ctx, 42, 10, "👀"); err != nil {
		t.Fatal(err)
	}
	call := f.Calls("setMessageReaction")[0]
	if call.Token != token || call.Params["chat_id"].(float64) != 42 || call.Params["message_id"].(float64) != 10 {
		t.Fatalf("reaction call = %+v", call)
	}
	r := call.Params["reaction"].([]any)[0].(map[string]any)
	if r["type"] != "emoji" || r["emoji"] != "👀" {
		t.Fatalf("reaction payload = %+v", r)
	}
	if err := c.SendMessage(ctx, 42, "hi", 10); err != nil {
		t.Fatal(err)
	}
	rp := f.Calls("sendMessage")[0].Params["reply_parameters"].(map[string]any)
	if rp["message_id"].(float64) != 10 || rp["allow_sending_without_reply"] != true {
		t.Fatalf("reply_parameters = %+v", rp)
	}
}

func TestGetUpdatesSendsOffsetAndFilter(t *testing.T) {
	f := tgtest.New(t)
	f.PushMessage(tgtest.TextMsg(1, 42, "a"))
	f.PushMessage(tgtest.TextMsg(2, 42, "b"))
	c := tgbot.New(f.URL(), token, nil)
	ups, err := c.GetUpdates(ctx, 2, 0)
	if err != nil || len(ups) != 1 || ups[0].UpdateID != 2 || len(ups[0].Message) == 0 {
		t.Fatalf("GetUpdates = %+v, %v", ups, err)
	}
	p := f.Calls("getUpdates")[0].Params
	allowed := p["allowed_updates"].([]any)
	if len(allowed) != 2 || allowed[0] != "message" || allowed[1] != "edited_message" {
		t.Fatalf("allowed_updates = %v", allowed)
	}
}

func TestAPIErrorParsed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 7","parameters":{"retry_after":7}}`))
	}))
	defer srv.Close()
	_, err := tgbot.New(srv.URL, token, nil).GetMe(ctx)
	var ae *tgbot.APIError
	if !errors.As(err, &ae) || ae.Code != 429 || ae.RetryAfter != 7 {
		t.Fatalf("err = %#v", err)
	}
}

func TestNetworkErrorRedactsToken(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	_, err := tgbot.New(url, token, nil).GetMe(ctx)
	if err == nil || strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "AAAAAAAA") {
		t.Fatalf("token leaked or no error: %v", err)
	}
}
