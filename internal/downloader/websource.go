package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"syscall"
	"time"

	"tgarchive/internal/store"
)

// ErrAddrNotAllowed is the failure reason for URLs that are not http(s) or that resolve to a
// loopback, private, link-local, multicast, unspecified or CGNAT (100.64.0.0/10) address.
var ErrAddrNotAllowed = errors.New("地址不允许")

const (
	webUserAgent    = "tgarchive/1.0"
	webMaxRedirects = 5
	webTimeout      = 5 * time.Minute
)

var cgnat = mustCIDR("100.64.0.0/10")

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

// PublicIP is the default dial check of WebSource: nil for publicly routable unicast addresses.
func PublicIP(ip net.IP) error {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || cgnat.Contains(ip) {
		return ErrAddrNotAllowed
	}
	return nil
}

// WebSource downloads "web:" media (Telegraph article images and videos) over plain HTTP(S).
// The address check runs on every connection after DNS resolution, so redirects and DNS
// rebinding cannot reach an internal address.
type WebSource struct {
	MaxBytes int64         // 0 = unlimited; larger bodies fail with ErrTooLarge
	Timeout  time.Duration // whole request, default 5 min
	client   *http.Client
}

// NewWebSource builds the source. allow checks each dialed IP; nil means PublicIP. Only tests
// pass anything else (to reach an httptest server on 127.0.0.1).
func NewWebSource(maxBytes int64, allow func(net.IP) error) *WebSource {
	if allow == nil {
		allow = PublicIP
	}
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return ErrAddrNotAllowed
			}
			return allow(ip)
		},
	}
	tr := &http.Transport{
		Proxy:                 nil, // a proxy would make the dial check see the proxy's address instead
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: time.Minute,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       90 * time.Second,
	}
	client := &http.Client{
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > webMaxRedirects {
				return fmt.Errorf("超过 %d 次重定向", webMaxRedirects)
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return ErrAddrNotAllowed
			}
			return nil
		},
	}
	return &WebSource{MaxBytes: maxBytes, Timeout: webTimeout, client: client}
}

func (w *WebSource) Fetch(ctx context.Context, m *store.Media, dstBase string) (string, int64, error) {
	u, err := url.Parse(m.SourceRef)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", 0, Permanent(ErrAddrNotAllowed)
	}
	ctx, cancel := context.WithTimeout(ctx, w.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", 0, Permanent(err)
	}
	req.Header.Set("User-Agent", webUserAgent)
	resp, err := w.client.Do(req)
	if err != nil {
		if errors.Is(err, ErrAddrNotAllowed) {
			return "", 0, Permanent(ErrAddrNotAllowed)
		}
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("HTTP %d", resp.StatusCode)
		if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusRequestTimeout && resp.StatusCode != http.StatusTooManyRequests {
			return "", 0, Permanent(err)
		}
		return "", 0, err
	}
	if w.MaxBytes > 0 && resp.ContentLength > w.MaxBytes {
		return "", 0, ErrTooLarge
	}
	dst := dstBase + webExt(resp.Header.Get("Content-Type"), u.Path)
	tmp := dst + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", 0, err
	}
	var body io.Reader = resp.Body
	if w.MaxBytes > 0 {
		body = io.LimitReader(resp.Body, w.MaxBytes+1)
	}
	n, err := io.Copy(f, body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && w.MaxBytes > 0 && n > w.MaxBytes {
		err = ErrTooLarge
	}
	if err == nil {
		err = os.Chmod(tmp, 0o644) // archived files are world-readable (NAS pull), whatever the umask
	}
	if err == nil {
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		os.Remove(tmp)
		return "", 0, err
	}
	return dst, n, nil
}

var webMimeExt = map[string]string{
	"image/jpeg": ".jpg", "image/png": ".png", "image/gif": ".gif", "image/webp": ".webp",
	"video/mp4": ".mp4", "video/webm": ".webm", "video/quicktime": ".mov",
}

var webPathExt = map[string]string{
	".jpg": ".jpg", ".jpeg": ".jpg", ".png": ".png", ".gif": ".gif", ".webp": ".webp",
	".mp4": ".mp4", ".webm": ".webm", ".mov": ".mov",
}

// webExt picks the stored extension from the response type, then the URL path; anything else
// is ".bin", which /media serves as an opaque download.
func webExt(contentType, urlPath string) string {
	if mt, _, err := mime.ParseMediaType(contentType); err == nil {
		if e, ok := webMimeExt[strings.ToLower(mt)]; ok {
			return e
		}
	}
	if e, ok := webPathExt[strings.ToLower(path.Ext(urlPath))]; ok {
		return e
	}
	return ".bin"
}
