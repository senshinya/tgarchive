package tgbot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type APIError struct {
	Code        int
	Description string
	RetryAfter  int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram api error %d: %s", e.Code, e.Description)
}

type Client struct {
	baseURL string
	token   string
	hc      *http.Client
}

func New(baseURL, token string, hc *http.Client) *Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), token: token, hc: hc}
}

func (c *Client) call(ctx context.Context, method string, params any, out any) error {
	if params == nil {
		params = struct{}{}
	}
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/bot"+c.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return c.redact(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return c.redact(err) // *url.Error embeds the URL, which contains the token
	}
	defer resp.Body.Close()
	var env struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		ErrorCode   int             `json:"error_code"`
		Description string          `json:"description"`
		Parameters  *struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&env); err != nil {
		return fmt.Errorf("telegram %s: http %d: decode response: %w", method, resp.StatusCode, err)
	}
	if !env.OK {
		ae := &APIError{Code: env.ErrorCode, Description: env.Description}
		if ae.Code == 0 {
			ae.Code = resp.StatusCode
		}
		if env.Parameters != nil {
			ae.RetryAfter = env.Parameters.RetryAfter
		}
		return ae
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(env.Result, out)
}

// Redact returns err with every occurrence of this client's token replaced. A nil err stays nil.
func (c *Client) Redact(err error) error {
	if err == nil {
		return nil
	}
	return c.redact(err)
}

func (c *Client) redact(err error) error {
	if c.token == "" {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), c.token, "<redacted>"))
}

func (c *Client) GetMe(ctx context.Context) (*User, error) {
	var u User
	if err := c.call(ctx, "getMe", nil, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

func (c *Client) LogOut(ctx context.Context) error {
	return c.call(ctx, "logOut", nil, nil)
}

func (c *Client) GetUpdates(ctx context.Context, offset int64, timeoutSec int) ([]Update, error) {
	var ups []Update
	err := c.call(ctx, "getUpdates", map[string]any{
		"offset":          offset,
		"timeout":         timeoutSec,
		"allowed_updates": []string{"message", "edited_message"},
	}, &ups)
	return ups, err
}

func (c *Client) GetFile(ctx context.Context, fileID string) (*File, error) {
	var f File
	if err := c.call(ctx, "getFile", map[string]any{"file_id": fileID}, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

func (c *Client) SetMessageReaction(ctx context.Context, chatID, messageID int64, emoji string) error {
	return c.call(ctx, "setMessageReaction", map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
		"reaction":   []map[string]string{{"type": "emoji", "emoji": emoji}},
	}, nil)
}

func (c *Client) SendMessage(ctx context.Context, chatID int64, text string, replyTo int64) error {
	params := map[string]any{"chat_id": chatID, "text": text}
	if replyTo != 0 {
		params["reply_parameters"] = map[string]any{"message_id": replyTo, "allow_sending_without_reply": true}
	}
	return c.call(ctx, "sendMessage", params, nil)
}

func (c *Client) GetUserProfilePhotos(ctx context.Context, userID int64, limit int) (*UserProfilePhotos, error) {
	var p UserProfilePhotos
	if err := c.call(ctx, "getUserProfilePhotos", map[string]any{"user_id": userID, "limit": limit}, &p); err != nil {
		return nil, err
	}
	return &p, nil
}
