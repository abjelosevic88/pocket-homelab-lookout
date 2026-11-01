// Package page reads web pages for Pocket Lookout: it downloads them (plain
// HTTP, or a headless browser for pages that need JavaScript), turns them into
// lines of text (the whole page, or only the parts picked with CSS selectors,
// a JSON path or the product price), filters noise, compares two versions and
// suggests the parts of a page worth watching.
package page

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// ErrBlocked is returned for addresses the fetcher refuses (private networks).
var ErrBlocked = errors.New("that address is on a private network (an admin can allow it with FETCH_PRIVATE=true)")

// MaxBytes is the most of a page that is read.
const MaxBytes = 8 << 20

// Fetcher downloads pages.
type Fetcher struct {
	UserAgent    string
	AllowPrivate bool
	Timeout      time.Duration
	Browser      *Browser // nil = no headless browser
}

// Result is a downloaded page.
type Result struct {
	Body        []byte
	ContentType string
	FinalURL    string
	Status      int
	Via         string // fast | browser
	Shot        []byte // JPEG, browser only, when asked for
}

// HTTPError is a page that answered with an error status.
type HTTPError struct{ Status int }

func (e *HTTPError) Error() string {
	switch e.Status {
	case 401, 403:
		return fmt.Sprintf("the site refused (HTTP %d) — it may block robots; try the browser", e.Status)
	case 404, 410:
		return fmt.Sprintf("the page is gone (HTTP %d)", e.Status)
	case 429:
		return "the site says too many requests (HTTP 429); checks will slow down"
	}
	return fmt.Sprintf("the site answered HTTP %d", e.Status)
}

var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

func privateIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() || cgnat.Contains(ip)
}

// CheckURL accepts http(s) URLs only.
func CheckURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("not a web address (it needs http:// or https://)")
	}
	return u, nil
}

// Allowed reports whether the fetcher may reach the URL's host (resolving it).
func (f *Fetcher) Allowed(ctx context.Context, raw string) error {
	u, err := CheckURL(raw)
	if err != nil {
		return err
	}
	if f.AllowPrivate {
		return nil
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		if privateIP(ip) {
			return ErrBlocked
		}
		return nil
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("the address %s does not resolve", host)
	}
	for _, ip := range ips {
		if privateIP(ip.IP) {
			return ErrBlocked
		}
	}
	return nil
}

func (f *Fetcher) client() *http.Client {
	d := &net.Dialer{Timeout: 10 * time.Second}
	if !f.AllowPrivate {
		d.Control = func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip != nil && privateIP(ip) {
				return ErrBlocked
			}
			return nil
		}
	}
	t := &http.Transport{DialContext: d.DialContext, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 25 * time.Second,
		MaxIdleConns: 8, IdleConnTimeout: 30 * time.Second, ForceAttemptHTTP2: true}
	timeout := f.Timeout
	if timeout == 0 {
		timeout = 40 * time.Second
	}
	return &http.Client{Transport: t, Timeout: timeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 8 {
			return errors.New("too many redirects")
		}
		return nil
	}}
}

// ParseHeaders reads "Name: value" lines.
func ParseHeaders(s string) http.Header {
	h := http.Header{}
	for _, l := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(l, ":")
		k = strings.TrimSpace(k)
		if ok && k != "" && !strings.ContainsAny(k, " \t") {
			h.Add(k, strings.TrimSpace(v))
		}
	}
	return h
}

// Fast downloads a page with plain HTTP.
func (f *Fetcher) Fast(ctx context.Context, raw string, headers http.Header) (*Result, error) {
	u, err := CheckURL(raw)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	ua := f.UserAgent
	if ua == "" {
		ua = "Mozilla/5.0 (compatible; PocketLookout/1.0)"
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json;q=0.9,text/plain;q=0.8,*/*;q=0.5")
	req.Header.Set("Accept-Language", "en,de;q=0.8,*;q=0.5")
	for k, vs := range headers {
		req.Header.Del(k)
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	res, err := f.client().Do(req)
	if err != nil {
		if errors.Is(err, ErrBlocked) {
			return nil, ErrBlocked
		}
		return nil, friendlyNetErr(err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, MaxBytes))
	out := &Result{Body: b, ContentType: res.Header.Get("Content-Type"), FinalURL: res.Request.URL.String(), Status: res.StatusCode, Via: "fast"}
	if err != nil {
		return out, friendlyNetErr(err)
	}
	if res.StatusCode >= 400 {
		return out, &HTTPError{res.StatusCode}
	}
	return out, nil
}

func friendlyNetErr(err error) error {
	var dns *net.DNSError
	switch {
	case errors.As(err, &dns):
		return fmt.Errorf("the address %s does not resolve", dns.Name)
	case errors.Is(err, context.DeadlineExceeded), strings.Contains(err.Error(), "Client.Timeout"):
		return errors.New("the page took too long to answer")
	case strings.Contains(err.Error(), "connection refused"):
		return errors.New("the site refused the connection")
	case strings.Contains(err.Error(), "certificate"):
		return errors.New("the site's certificate is not valid")
	}
	return err
}

// Kind is what a downloaded body is: html, json, text or other.
func Kind(contentType string, body []byte) string {
	mt, _, _ := mime.ParseMediaType(contentType)
	switch {
	case strings.Contains(mt, "html") || strings.Contains(mt, "xml"):
		return "html"
	case strings.Contains(mt, "json"):
		return "json"
	case strings.HasPrefix(mt, "text/"):
		return "text"
	}
	head := strings.TrimSpace(string(body[:min(len(body), 512)]))
	switch {
	case strings.HasPrefix(head, "{") || strings.HasPrefix(head, "["):
		return "json"
	case strings.Contains(strings.ToLower(head), "<html") || strings.Contains(strings.ToLower(head), "<!doctype"):
		return "html"
	case mt == "":
		return "text"
	}
	return "other"
}

// NeedsBrowser guesses whether a page only shows its content with JavaScript
// (or blocked a plain request): little text, or a "please enable JavaScript".
func NeedsBrowser(r *Result, text string, err error) bool {
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Status == 401 || he.Status == 403 || he.Status == 429 || he.Status == 503
	}
	if err != nil || r == nil {
		return false
	}
	if Kind(r.ContentType, r.Body) != "html" {
		return false
	}
	low := strings.ToLower(text)
	if len(strings.TrimSpace(text)) < 200 {
		return true
	}
	for _, s := range []string{"enable javascript", "javascript is required", "javascript is disabled", "you need to enable javascript", "checking your browser", "just a moment"} {
		if strings.Contains(low, s) && len(text) < 3000 {
			return true
		}
	}
	return false
}
