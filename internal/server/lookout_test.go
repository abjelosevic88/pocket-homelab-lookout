package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/abjelosevic88/pocket-homelab-lookout/internal/config"
	"github.com/abjelosevic88/pocket-homelab-lookout/internal/store"
)

type app struct {
	t     *testing.T
	url   string
	s     *Server
	st    *store.Store
	alice string // admin, owner of the box
	bob   string
	ids   map[string]int64
	site  *fakeSite
}

// fakeSite is a page whose content the test changes.
type fakeSite struct {
	mu    sync.Mutex
	pages map[string]string
	types map[string]string
	srv   *httptest.Server
}

func (f *fakeSite) set(path, body string) {
	f.mu.Lock()
	f.pages[path] = body
	f.mu.Unlock()
}

func (f *fakeSite) url(path string) string { return f.srv.URL + path }

func newApp(t *testing.T, tweak func(*config.Config)) *app {
	t.Helper()
	cfg := testConfig(t.TempDir())
	cfg.FetchPrivate = true // the fake site runs on localhost
	cfg.BaseURL = "http://lookout.test"
	cfg.MinInterval = 5 * 60 * 1e9
	if tweak != nil {
		tweak(cfg)
	}
	st := openTestStore(t, cfg)
	ctx := context.Background()
	a := &app{t: t, st: st, ids: map[string]int64{}}
	for _, u := range []*store.User{{Email: "alice@example.com", Name: "Alice", Role: "admin"}, {Email: "bob@example.com", Name: "Bob", Role: "member"}} {
		if err := st.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
		tok, _, _ := st.CreateAPIToken(ctx, u.ID, "t")
		a.ids[u.Name] = u.ID
		if u.Name == "Alice" {
			a.alice = tok
		} else {
			a.bob = tok
		}
	}
	site := &fakeSite{pages: map[string]string{}, types: map[string]string{}}
	site.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		site.mu.Lock()
		body, ok := site.pages[r.URL.Path]
		ct := site.types[r.URL.Path]
		site.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		if ct == "" {
			ct = "text/html; charset=utf-8"
		}
		w.Header().Set("Content-Type", ct)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(site.srv.Close)
	a.site = site
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	a.s = New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err := a.s.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(a.s.Router())
	t.Cleanup(ts.Close)
	a.url = ts.URL
	return a
}

func (a *app) raw(auth, method, path string, body io.Reader, ct string) (int, []byte) {
	a.t.Helper()
	req, _ := http.NewRequest(method, a.url+path, body)
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	if auth != "" {
		req.Header.Set("Authorization", "Bearer "+auth)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b
}

func (a *app) call(auth, method, path string, body any) (int, map[string]any, []any) {
	a.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	code, raw := a.raw(auth, method, path, rd, "application/json")
	var m map[string]any
	var l []any
	if json.Unmarshal(raw, &m) != nil {
		_ = json.Unmarshal(raw, &l)
	}
	return code, m, l
}

func (a *app) ok(auth, method, path string, body any) map[string]any {
	a.t.Helper()
	code, m, _ := a.call(auth, method, path, body)
	if code >= 300 {
		a.t.Fatalf("%s %s → %d %v", method, path, code, m)
	}
	return m
}

func (a *app) list(auth, path string) []any {
	a.t.Helper()
	code, m, l := a.call(auth, "GET", path, nil)
	if code >= 300 {
		a.t.Fatalf("GET %s → %d %v", path, code, m)
	}
	return l
}

func (a *app) expect(want int, auth, method, path string, body any) map[string]any {
	a.t.Helper()
	code, m, _ := a.call(auth, method, path, body)
	if code != want {
		a.t.Fatalf("%s %s → %d, want %d (%v)", method, path, code, want, m)
	}
	return m
}

func num(m map[string]any, k string) int64 { v, _ := m[k].(float64); return int64(v) }

func htmlPage(body string) string {
	return "<html><head><title>Fake shop</title><script>var x = 1</script></head><body>" + body + "</body></html>"
}

// check runs "check now" and returns the outcome.
func (a *app) check(auth string, id int64) map[string]any {
	a.t.Helper()
	m := a.ok(auth, "POST", fmt.Sprintf("/api/v1/watches/%d/check", id), nil)
	out, _ := m["outcome"].(map[string]any)
	return out
}

func TestCheckFindsChangesAndIgnoresNoise(t *testing.T) {
	a := newApp(t, nil)
	a.site.set("/p", htmlPage(`<h1>Kettle</h1><div id="price">Price: 49 €</div><p>Updated 10:42 · 1,204 views</p>`))
	w := a.ok(a.alice, "POST", "/api/v1/watches", map[string]any{"url": a.site.url("/p"), "fetch_mode": "fast"})
	id := num(w, "id")

	// the scheduler may have taken the first look already
	first := a.check(a.alice, id)
	if first["ok"] != true || first["changed"] == true {
		t.Fatalf("first check = %v", first)
	}
	got := a.ok(a.alice, "GET", fmt.Sprintf("/api/v1/watches/%d", id), nil)
	if got["page_title"] != "Fake shop" || got["status"] != "ok" {
		t.Fatalf("after first check: %v", got)
	}

	// only the time and the view counter move: noise, not a change
	a.site.set("/p", htmlPage(`<h1>Kettle</h1><div id="price">Price: 49 €</div><p>Updated 11:05 · 1,388 views</p>`))
	if out := a.check(a.alice, id); out["changed"] == true {
		t.Fatalf("noise counted as a change: %v", out)
	}

	a.site.set("/p", htmlPage(`<h1>Kettle</h1><div id="price">Price: 39 €</div><p>Updated 11:30 · 1,390 views</p>`))
	out := a.check(a.alice, id)
	if out["changed"] != true {
		t.Fatalf("price change not found: %v", out)
	}
	c := out["change"].(map[string]any)
	if num(c, "added") != 1 || num(c, "removed") != 1 || !strings.Contains(c["summary"].(string), "39 €") {
		t.Fatalf("change = %v", c)
	}
	changes := a.list(a.alice, "/api/v1/changes")
	if len(changes) != 1 {
		t.Fatalf("changes = %d", len(changes))
	}
	got = a.ok(a.alice, "GET", fmt.Sprintf("/api/v1/watches/%d", id), nil)
	if num(got, "unseen") != 1 {
		t.Fatalf("unseen = %v", got["unseen"])
	}
	a.ok(a.alice, "POST", "/api/v1/changes/seen", map[string]any{"watch_id": id})
	got = a.ok(a.alice, "GET", fmt.Sprintf("/api/v1/watches/%d", id), nil)
	if num(got, "unseen") != 0 {
		t.Fatalf("unseen after seen = %v", got["unseen"])
	}
	// with the noise filter off the clock counts
	a.ok(a.alice, "PUT", fmt.Sprintf("/api/v1/watches/%d", id), map[string]any{"noise_filter": false})
	a.site.set("/p", htmlPage(`<h1>Kettle</h1><div id="price">Price: 39 €</div><p>Updated 12:00 · 1,390 views</p>`))
	if out := a.check(a.alice, id); out["changed"] != true {
		t.Fatalf("without the noise filter the time should count: %v", out)
	}
}

func TestSelectorRulesAndDismiss(t *testing.T) {
	a := newApp(t, nil)
	a.site.set("/t", htmlPage(`<nav>Menu</nav><ul class="slots"><li>No appointments</li></ul><footer>© ad 1</footer>`))
	w := a.ok(a.alice, "POST", "/api/v1/watches", map[string]any{"url": a.site.url("/t"), "fetch_mode": "fast", "selector": "ul.slots", "trigger_text": "available", "notify_on": "added"})
	id := num(w, "id")
	a.check(a.alice, id)

	// the footer is not watched
	a.site.set("/t", htmlPage(`<nav>Menu</nav><ul class="slots"><li>No appointments</li></ul><footer>© ad 2</footer>`))
	if out := a.check(a.alice, id); out["changed"] == true {
		t.Fatalf("a change outside the selector counted: %v", out)
	}
	// changed, but no line mentions "available": recorded, does not count
	a.site.set("/t", htmlPage(`<ul class="slots"><li>No appointments yet</li></ul>`))
	out := a.check(a.alice, id)
	if out["changed"] != true || out["ignored"] != true {
		t.Fatalf("trigger rule: %v", out)
	}
	if n := len(a.list(a.alice, "/api/v1/changes")); n != 0 {
		t.Fatalf("an ignored change is listed: %d", n)
	}
	if n := len(a.list(a.alice, "/api/v1/changes?all=1")); n != 1 {
		t.Fatalf("all=1 should show it: %d", n)
	}
	a.site.set("/t", htmlPage(`<ul class="slots"><li>No appointments yet</li><li>12 Nov: 3 slots available</li></ul>`))
	out = a.check(a.alice, id)
	if out["changed"] != true || out["ignored"] == true {
		t.Fatalf("matching change: %v", out)
	}
	cid := num(out["change"].(map[string]any), "id")
	// "not important" adds its lines to the ignore list
	a.ok(a.alice, "POST", fmt.Sprintf("/api/v1/changes/%d/dismiss", cid), map[string]any{"ignore": true})
	got := a.ok(a.alice, "GET", fmt.Sprintf("/api/v1/watches/%d", id), nil)
	if !strings.Contains(got["ignore_text"].(string), "12 Nov: 3 slots available") {
		t.Fatalf("ignore_text = %q", got["ignore_text"])
	}
	// the part disappearing is an error, not an empty page
	a.site.set("/t", htmlPage(`<p>Maintenance</p>`))
	if out := a.check(a.alice, id); out["ok"] == true || !strings.Contains(out["error"].(string), "ul.slots") {
		t.Fatalf("missing selector: %v", out)
	}
}

func TestPreviewSuggestsParts(t *testing.T) {
	a := newApp(t, nil)
	a.site.set("/shop", htmlPage(`<div class="product"><h1>Lamp</h1><span itemprop="price" content="19.99">19,99 €</span><p class="stock-status">In stock</p></div>`))
	m := a.ok(a.alice, "POST", "/api/v1/preview", map[string]any{"url": a.site.url("/shop"), "fetch_mode": "fast", "find": "In stock"})
	if m["title"] != "Fake shop" || m["error"] != nil {
		t.Fatalf("preview = %v", m)
	}
	blocks := m["blocks"].([]any)
	if len(blocks) == 0 {
		t.Fatal("no parts suggested")
	}
	first := blocks[0].(map[string]any)
	if first["found"] != true || !strings.Contains(first["text"].(string), "In stock") {
		t.Fatalf("first block = %v", first)
	}
	// the suggested selector picks exactly that part
	m = a.ok(a.alice, "POST", "/api/v1/preview", map[string]any{"url": a.site.url("/shop"), "fetch_mode": "fast", "selector": first["selector"]})
	if strings.TrimSpace(m["text"].(string)) != "In stock" {
		t.Fatalf("selector %q reads %q", first["selector"], m["text"])
	}
}

func TestJSONPath(t *testing.T) {
	a := newApp(t, nil)
	a.site.set("/api", `{"latest": {"version": "6.17.4", "date": "2026-10-01"}, "other": [1,2]}`)
	a.site.types["/api"] = "application/json"
	w := a.ok(a.alice, "POST", "/api/v1/watches", map[string]any{"url": a.site.url("/api"), "fetch_mode": "fast", "selector": "$.latest.version"})
	id := num(w, "id")
	a.check(a.alice, id)
	a.site.set("/api", `{"latest": {"version": "6.18", "date": "2026-11-01"}, "other": [1,2,3]}`)
	out := a.check(a.alice, id)
	if out["changed"] != true || !strings.Contains(out["change"].(map[string]any)["summary"].(string), "6.18") {
		t.Fatalf("json path change: %v", out)
	}
}

func TestPerPersonIsolation(t *testing.T) {
	a := newApp(t, nil)
	a.site.set("/x", htmlPage("<p>one</p>"))
	w := a.ok(a.alice, "POST", "/api/v1/watches", map[string]any{"url": a.site.url("/x"), "fetch_mode": "fast", "tags": "private"})
	id := num(w, "id")
	a.check(a.alice, id)
	a.site.set("/x", htmlPage("<p>two</p>"))
	cid := num(a.check(a.alice, id)["change"].(map[string]any), "id")

	for _, p := range []string{fmt.Sprintf("/api/v1/watches/%d", id), fmt.Sprintf("/api/v1/changes/%d", cid), fmt.Sprintf("/api/v1/watches/%d/changes", id), fmt.Sprintf("/api/v1/watches/%d/snapshot", id)} {
		a.expect(404, a.bob, "GET", p, nil)
	}
	a.expect(404, a.bob, "PUT", fmt.Sprintf("/api/v1/watches/%d", id), map[string]any{"title": "mine now"})
	a.expect(404, a.bob, "POST", fmt.Sprintf("/api/v1/watches/%d/check", id), nil)
	a.expect(404, a.bob, "POST", fmt.Sprintf("/api/v1/changes/%d/dismiss", cid), nil)
	a.expect(404, a.bob, "DELETE", fmt.Sprintf("/api/v1/watches/%d", id), nil)
	if n := len(a.list(a.bob, "/api/v1/watches")); n != 0 {
		t.Fatalf("Bob sees %d watches", n)
	}
	if n := len(a.list(a.bob, "/api/v1/changes")); n != 0 {
		t.Fatalf("Bob sees %d changes", n)
	}
	if n := len(a.list(a.bob, "/api/v1/tags")); n != 0 {
		t.Fatalf("Bob sees tags")
	}
	// Bob's "mark all seen" leaves Alice's alone
	a.ok(a.bob, "POST", "/api/v1/changes/seen", nil)
	if n := len(a.list(a.alice, "/api/v1/changes?unseen=1")); n != 1 {
		t.Fatalf("Alice's unseen changes = %d", n)
	}
	// settings are personal
	a.ok(a.bob, "PUT", "/api/v1/settings", map[string]any{"default_interval": 60, "title": "Bob's box"})
	if s := a.ok(a.alice, "GET", "/api/v1/settings", nil); num(s, "default_interval") != 360 || s["title"] != "Pocket Lookout" {
		t.Fatalf("Alice's settings changed: %v", s)
	}
	// even an admin does not see a member's watches
	b := a.ok(a.bob, "POST", "/api/v1/watches", map[string]any{"url": "https://example.com/bob"})
	a.expect(404, a.alice, "GET", fmt.Sprintf("/api/v1/watches/%d", num(b, "id")), nil)
}

func TestFeed(t *testing.T) {
	a := newApp(t, nil)
	a.site.set("/f", htmlPage("<p>v1</p>"))
	w := a.ok(a.alice, "POST", "/api/v1/watches", map[string]any{"url": a.site.url("/f"), "fetch_mode": "fast", "title": "Release page"})
	id := num(w, "id")
	a.check(a.alice, id)
	a.site.set("/f", htmlPage("<p>v2</p>"))
	a.check(a.alice, id)
	feed := a.ok(a.alice, "GET", "/api/v1/settings", nil)["feed_url"].(string)
	path := strings.TrimPrefix(feed, "http://lookout.test")
	code, body := a.raw("", "GET", path, nil, "")
	if code != 200 || !strings.Contains(string(body), "Release page") || !strings.Contains(string(body), "<rss") {
		t.Fatalf("feed %d: %s", code, body)
	}
	code, body = a.raw("", "GET", strings.TrimSuffix(path, ".xml")+fmt.Sprintf("/%d.xml", id), nil, "")
	if code != 200 || !strings.Contains(string(body), "v2") {
		t.Fatalf("watch feed %d: %s", code, body)
	}
	if code, _ := a.raw("", "GET", "/feed/not-a-real-secret-at-all.xml", nil, ""); code != 404 {
		t.Fatalf("wrong secret → %d", code)
	}
	// Bob's secret does not open Alice's watch
	bobFeed := strings.TrimPrefix(a.ok(a.bob, "GET", "/api/v1/settings", nil)["feed_url"].(string), "http://lookout.test")
	if code, _ := a.raw("", "GET", strings.TrimSuffix(bobFeed, ".xml")+fmt.Sprintf("/%d.xml", id), nil, ""); code != 404 {
		t.Fatalf("Bob's feed opened Alice's watch: %d", code)
	}
	// a new secret stops the old address
	a.ok(a.alice, "POST", "/api/v1/feed/rotate", nil)
	if code, _ := a.raw("", "GET", path, nil, ""); code != 404 {
		t.Fatalf("old feed still works: %d", code)
	}
}

func TestImport(t *testing.T) {
	a := newApp(t, nil)
	list := "https://example.com/a news, tech\nexample.org/b\n# comment\nnot a url at all\n"
	code, body := a.raw(a.alice, "POST", "/api/v1/import?tags=imported", strings.NewReader(list), "text/plain")
	var res map[string]any
	_ = json.Unmarshal(body, &res)
	if code != 200 || num(res, "added") != 2 {
		t.Fatalf("url list %d: %s", code, body)
	}
	// again: nothing new
	_, body = a.raw(a.alice, "POST", "/api/v1/import", strings.NewReader(list), "text/plain")
	_ = json.Unmarshal(body, &res)
	if num(res, "added") != 0 {
		t.Fatalf("second import added %v", res["added"])
	}
	cd := `{"settings":{"application":{"tags":{"t1":{"title":"Shipping"}}}},"watching":{"u1":{"url":"https://shop.example.com/x","title":"Box","tags":["t1"],
		"include_filters":["#price","//div[@id='x']"],"ignore_text":["Ad"],"trigger_text":[],"paused":true,"fetch_backend":"html_webdriver",
		"time_between_check":{"weeks":0,"days":0,"hours":3,"minutes":0,"seconds":0},"processor":"text_json_diff"},
		"u2":{"url":"https://shop.example.com/y","processor":"restock_diff","tags":[]}}}`
	_, body = a.raw(a.alice, "POST", "/api/v1/import", strings.NewReader(cd), "application/json")
	_ = json.Unmarshal(body, &res)
	if res["from"] != "changedetection.io" || num(res, "added") != 2 {
		t.Fatalf("changedetection import: %s", body)
	}
	ws := a.list(a.alice, "/api/v1/watches?q=shop.example.com")
	var box map[string]any
	for _, x := range ws {
		if m := x.(map[string]any); m["title"] == "Box" {
			box = m
		}
	}
	if box == nil || box["selector"] != "#price" || box["tags"] != "shipping" || box["fetch_mode"] != "browser" || num(box, "interval_minutes") != 180 || box["paused"] != true || box["ignore_text"] != "Ad" {
		t.Fatalf("imported watch = %v", box)
	}
	// export → import into Bob: same watches, his own
	code, exp := a.raw(a.alice, "GET", "/api/v1/export.json", nil, "")
	if code != 200 {
		t.Fatal(code)
	}
	_, body = a.raw(a.bob, "POST", "/api/v1/import", bytes.NewReader(exp), "application/json")
	_ = json.Unmarshal(body, &res)
	if res["from"] != "Pocket Lookout" || num(res, "added") != 4 {
		t.Fatalf("lookout import: %s", body)
	}
}

func TestLinkScopes(t *testing.T) {
	req := func(method, path string) *http.Request { r := httptest.NewRequest(method, path, nil); return r }
	cases := []struct {
		scopes []string
		r      *http.Request
		want   bool
	}{
		{nil, req("GET", "/api/v1/auth/me"), true},
		{[]string{"watches:read"}, req("GET", "/api/v1/watches"), true},
		{[]string{"watches:read"}, req("GET", "/api/v1/changes/3"), true},
		{[]string{"watches:read"}, req("POST", "/api/v1/watches"), false},
		{[]string{"watches:write"}, req("POST", "/api/v1/watches"), true},
		{[]string{"watches:write"}, req("POST", "/api/v1/watches/4/check"), true},
		{[]string{"watches:read", "watches:write"}, req("GET", "/api/v1/settings"), false},
		{[]string{"watches:read", "watches:write"}, req("GET", "/api/v1/export.json"), false},
		{[]string{"watches:read", "watches:write"}, req("POST", "/api/v1/changes/3/dismiss"), false},
		{[]string{"watches:read", "watches:write"}, req("GET", "/api/v1/tokens"), false},
	}
	for _, c := range cases {
		if got := linkAllows(c.scopes, c.r); got != c.want {
			t.Errorf("%v %s %s = %v, want %v", c.scopes, c.r.Method, c.r.URL.Path, got, c.want)
		}
	}
	// a linked app changes only what it added
	w := &store.Watch{Source: "flows"}
	ctx := context.WithValue(context.Background(), linkAppKey, "flows")
	if !mayChange(ctx, w) || mayChange(context.WithValue(context.Background(), linkAppKey, "day"), w) || !mayChange(context.Background(), w) {
		t.Fatal("mayChange")
	}
}

func TestDemoData(t *testing.T) {
	a := newApp(t, func(c *config.Config) { c.DemoData = true })
	if err := a.s.seedDemo(context.Background(), a.ids["Alice"]); err != nil {
		t.Fatal(err)
	}
	ws := a.list(a.alice, "/api/v1/watches")
	if len(ws) < 15 {
		t.Fatalf("demo watches = %d", len(ws))
	}
	if cs := a.list(a.alice, "/api/v1/changes"); len(cs) < 20 {
		t.Fatalf("demo changes = %d", len(cs))
	}
	d := a.ok(a.alice, "GET", "/api/v1/dashboard", nil)
	if num(d, "failing") != 1 || num(d, "unseen") == 0 {
		t.Fatalf("dashboard = %v", d)
	}
	// seeding twice adds nothing
	_ = a.s.seedDemo(context.Background(), a.ids["Alice"])
	if n := len(a.list(a.alice, "/api/v1/watches")); n != len(ws) {
		t.Fatalf("second seed: %d → %d", len(ws), n)
	}
	if n := len(a.list(a.bob, "/api/v1/watches")); n != 0 {
		t.Fatalf("demo data leaked to Bob: %d", n)
	}
}
