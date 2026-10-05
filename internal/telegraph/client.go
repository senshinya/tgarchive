package telegraph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultAPIURL is the Telegraph API; TELEGRAPH_API_URL overrides it for tests only.
const DefaultAPIURL = "https://api.telegra.ph"

const maxPageBytes = 16 << 20

// Page is the part of a Telegraph getPage result tgarchive keeps.
type Page struct {
	Path        string `json:"path"`
	URL         string `json:"url"`
	Title       string `json:"title"`
	Description string `json:"description"`
	AuthorName  string `json:"author_name"`
	AuthorURL   string `json:"author_url"`
	ImageURL    string `json:"image_url"`
	Content     []Node `json:"content"`
	Views       int64  `json:"views"`
}

// APIError is Telegraph answering ok:false (e.g. PAGE_NOT_FOUND). It is final: never retried.
type APIError struct{ Code string }

func (e *APIError) Error() string { return "telegraph: " + e.Code }

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// NewClient talks to baseURL with a 30s per-request timeout.
func NewClient(baseURL string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// GetPage fetches an article with its content. Network errors, 5xx and non-JSON answers come
// back as plain errors (retryable); ok:false comes back as *APIError.
func (c *Client) GetPage(ctx context.Context, path string) (*Page, error) {
	u := c.BaseURL + "/getPage/" + url.PathEscape(path) + "?return_content=true"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "tgarchive/1.0")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPageBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("telegraph: HTTP %d", resp.StatusCode)
	}
	var r struct {
		OK     bool   `json:"ok"`
		Error  string `json:"error"`
		Result *Page  `json:"result"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("telegraph: bad response (HTTP %d): %w", resp.StatusCode, err)
	}
	if !r.OK {
		return nil, &APIError{Code: r.Error}
	}
	if r.Result == nil {
		return nil, errors.New("telegraph: ok without result")
	}
	return r.Result, nil
}
