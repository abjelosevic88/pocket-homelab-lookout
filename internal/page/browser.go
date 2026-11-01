package page

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// Browser drives a remote headless Chrome over the DevTools protocol: a
// websocket (ws://browser:3000 for sockpuppetbrowser, a browserless URL) or a
// Chrome started with --remote-debugging-port (http://chrome:9222).
type Browser struct {
	URL     string
	Timeout time.Duration
	// Pages at the same time (each one is a fresh browser context).
	sem chan struct{}
	mu  sync.Mutex
}

// NewBrowser returns nil when url is empty.
func NewBrowser(url string, timeout time.Duration, parallel int) *Browser {
	if strings.TrimSpace(url) == "" {
		return nil
	}
	if parallel < 1 {
		parallel = 1
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &Browser{URL: url, Timeout: timeout, sem: make(chan struct{}, parallel)}
}

// Configured reports whether there is a browser.
func (b *Browser) Configured() bool { return b != nil && b.URL != "" }

// MaxShotHeight caps how much of a long page goes into a picture.
const MaxShotHeight = 4000

// Get opens a page in the browser, waits for it to settle and returns its
// HTML (and a JPEG picture of it when shot is true).
func (b *Browser) Get(ctx context.Context, raw string, headers http.Header, wait time.Duration, shot bool) (*Result, error) {
	if !b.Configured() {
		return nil, errors.New("no browser is set up (BROWSER_URL)")
	}
	select {
	case b.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-b.sem }()

	ctx, cancel := context.WithTimeout(ctx, b.Timeout)
	defer cancel()
	opts := []chromedp.RemoteAllocatorOption{}
	if strings.HasPrefix(b.URL, "ws://") || strings.HasPrefix(b.URL, "wss://") {
		opts = append(opts, chromedp.NoModifyURL)
	}
	actx, acancel := chromedp.NewRemoteAllocator(ctx, b.URL, opts...)
	defer acancel()
	tctx, tcancel := chromedp.NewContext(actx)
	defer tcancel()

	status := 0
	var statusMu sync.Mutex
	chromedp.ListenTarget(tctx, func(ev any) {
		if e, ok := ev.(*network.EventResponseReceived); ok && e.Type == network.ResourceTypeDocument {
			statusMu.Lock()
			if status == 0 {
				status = int(e.Response.Status)
			}
			statusMu.Unlock()
		}
	})
	extra := network.Headers{}
	for k, vs := range headers {
		extra[k] = strings.Join(vs, ", ")
	}
	var html, final string
	var buf []byte
	actions := []chromedp.Action{
		network.Enable(),
		emulation.SetDeviceMetricsOverride(1280, 900, 1, false),
	}
	if len(extra) > 0 {
		actions = append(actions, network.SetExtraHTTPHeaders(extra))
	}
	actions = append(actions,
		chromedp.Navigate(raw),
		chromedp.Sleep(wait),
		chromedp.Location(&final),
		chromedp.OuterHTML("html", &html, chromedp.ByQuery),
	)
	if shot {
		actions = append(actions, chromedp.ActionFunc(func(ctx context.Context) error {
			_, _, content, _, _, css, err := page.GetLayoutMetrics().Do(ctx)
			if err != nil {
				return err
			}
			size := content
			if css != nil {
				size = css
			}
			h := math.Min(math.Max(size.Height, 900), MaxShotHeight)
			buf, err = page.CaptureScreenshot().WithFormat(page.CaptureScreenshotFormatJpeg).WithQuality(70).
				WithCaptureBeyondViewport(true).WithClip(&page.Viewport{X: 0, Y: 0, Width: 1280, Height: h, Scale: 1}).Do(ctx)
			return err
		}))
	}
	if err := chromedp.Run(tctx, actions...); err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("the page took too long in the browser")
		}
		msg := err.Error()
		switch {
		case strings.Contains(msg, "ERR_NAME_NOT_RESOLVED"):
			return nil, errors.New("the address does not resolve")
		case strings.Contains(msg, "ERR_CONNECTION_REFUSED"):
			return nil, errors.New("the site refused the connection")
		case strings.Contains(msg, "websocket") || strings.Contains(msg, "dial"):
			return nil, fmt.Errorf("the browser is not reachable (%s)", b.URL)
		}
		return nil, fmt.Errorf("browser: %w", err)
	}
	statusMu.Lock()
	st := status
	statusMu.Unlock()
	res := &Result{Body: []byte(html), ContentType: "text/html; charset=utf-8", FinalURL: final, Status: st, Via: "browser", Shot: buf}
	if st >= 400 {
		return res, &HTTPError{st}
	}
	return res, nil
}
