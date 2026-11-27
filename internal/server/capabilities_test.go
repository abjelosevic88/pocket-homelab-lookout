package server

import (
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
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-lookout/internal/config"
	"github.com/abjelosevic88/pocket-homelab-lookout/internal/store"
)

// fakeCapCore is a Pocket Core that vouches for link tokens
// pcx_<alice|bob>_<search|day|widget|capture|read|write> from Pocket Hub
// and remembers the manifest Lookout registers with.
type fakeCapCore struct {
	*httptest.Server
	mu       sync.Mutex
	manifest map[string]any
}

func newCapApp(t *testing.T) (*app, *fakeCapCore) {
	t.Helper()
	fc := &fakeCapCore{}
	scopes := map[string]string{"search": core.ScopeSearch, "day": core.ScopeDay, "widget": core.ScopeWidget, "capture": core.ScopeCapture,
		"read": "watches:read", "write": "watches:write"}
	fc.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/register":
			var m map[string]any
			_ = json.NewDecoder(r.Body).Decode(&m)
			fc.mu.Lock()
			fc.manifest = m
			fc.mu.Unlock()
			io.WriteString(w, `{}`)
		case "/api/v1/auth/introspect":
			var in struct{ Token string }
			_ = json.NewDecoder(r.Body).Decode(&in)
			parts := strings.Split(in.Token, "_")
			if len(parts) != 3 || parts[0] != "pcx" || scopes[parts[2]] == "" {
				io.WriteString(w, `{"active":false}`)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"active": true, "kind": "delegated", "audience": "lookout", "scope": scopes[parts[2]],
				"user": map[string]any{"id": 1, "sub": parts[1] + "@example.com", "email": parts[1] + "@example.com", "name": parts[1], "role": "member"},
				"app":  map[string]any{"id": 9, "slug": "hub", "name": "Pocket Hub"}})
		default:
			io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(fc.Close)
	return newApp(t, func(c *config.Config) { c.CoreURL, c.CoreRegistrationKey = fc.URL, "key" }), fc
}

// items are the rows of a capability answer.
func items(t *testing.T, m map[string]any) []map[string]any {
	t.Helper()
	l, ok := m["items"].([]any)
	if !ok {
		t.Fatalf("no items list in %v", m)
	}
	out := []map[string]any{}
	for _, v := range l {
		out = append(out, v.(map[string]any))
	}
	return out
}

func titles(t *testing.T, m map[string]any) string {
	t.Helper()
	var out []string
	for _, it := range items(t, m) {
		out = append(out, it["title"].(string))
	}
	return strings.Join(out, " | ")
}

func sid(id int64) string { return fmt.Sprint(id) }

// watchPage starts a paused watch on a page of the fake site (so only the
// test checks it) and reads the page once: the next change counts.
func (a *app) watchPage(auth, path, title, body string, more map[string]any) int64 {
	a.t.Helper()
	a.site.set(path, htmlPage(body))
	in := map[string]any{"url": a.site.url(path), "title": title, "fetch_mode": "fast", "paused": true}
	for k, v := range more {
		in[k] = v
	}
	id := num(a.ok(auth, "POST", "/api/v1/watches", in), "id")
	a.check(auth, id)
	return id
}

// changePage puts new words on a watched page, checks it and returns the change.
func (a *app) changePage(auth string, watch int64, path, body string) map[string]any {
	a.t.Helper()
	a.site.set(path, htmlPage(body))
	out := a.check(auth, watch)
	c, _ := out["change"].(map[string]any)
	if out["changed"] != true || c == nil {
		a.t.Fatalf("%s did not change: %v", path, out)
	}
	return c
}

// changeAt moves a change to another moment (nothing in the API can).
func (a *app) changeAt(change map[string]any, at string) {
	a.t.Helper()
	if _, err := a.st.DB.Exec(`UPDATE changes SET at = ? WHERE id = ?`, at, num(change, "id")); err != nil {
		a.t.Fatal(err)
	}
}

func TestPocketSearch(t *testing.T) {
	a := newApp(t, nil)
	kettle := a.watchPage(a.alice, "/kettle", "Kettle", `<p>Price: 49 EUR</p>`, map[string]any{"tags": "kitchen", "notes": "for the birthday"})
	visa := a.watchPage(a.alice, "/visa-appointments", "", `<p>No free slots</p>`, nil)
	news := a.watchPage(a.alice, "/news", "Town news", `<p>Nothing yet</p>`, nil)
	a.watchPage(a.bob, "/bob-kettle", "Bob's kettle", `<p>Price: 10 EUR</p>`, nil)
	a.changePage(a.alice, news, "/news", `<p>The kettle factory opens in May</p>`)

	// a name with the word comes before a page that only changed about it
	res := a.ok(a.alice, "GET", "/api/v1/pocket/search?q=kettle", nil)
	got := items(t, res)
	if len(got) != 2 || got[0]["title"] != "Kettle" || got[1]["title"] != "Town news" {
		t.Fatalf("kettle: %v", got)
	}
	k := got[0]
	if k["id"] != sid(kettle) || k["kind"] != "watched page" || k["url"] != "/watches/"+sid(kettle) || k["link"] != a.site.url("/kettle") ||
		k["snippet"] != a.site.url("/kettle") || k["date"] != nil {
		t.Fatalf("a watch that never changed: %v", k)
	}
	n := got[1]
	if n["id"] != sid(news) || !strings.Contains(n["snippet"].(string), "kettle factory") || n["url"] != "/watches/"+sid(news) {
		t.Fatalf("a watch found by its last change: %v", n)
	}
	if _, err := time.Parse(time.RFC3339, n["date"].(string)); err != nil {
		t.Fatalf("date %v is not when it last changed", n["date"])
	}
	// by page title (the fake site's), address, tag and notes; every word must be there
	for q, want := range map[string]string{
		"fake+shop+visa":    "Fake shop",
		"VISA-appointments": "Fake shop",
		"kitchen":           "Kettle",
		"birthday+kettle":   "Kettle",
		"kettle+visa":       "",
		"nothing+like+it":   "",
		"??":                "",
		"*":                 "",
		"%25":               "",
		"_":                 "",
	} {
		if got := titles(t, a.ok(a.alice, "GET", "/api/v1/pocket/search?q="+q, nil)); got != want {
			t.Errorf("q=%s finds %q, want %q", q, got, want)
		}
	}
	if v := items(t, a.ok(a.alice, "GET", "/api/v1/pocket/search?q=visa", nil)); len(v) != 1 || v[0]["id"] != sid(visa) {
		t.Fatalf("visa: %v", v)
	}
	if got := items(t, a.ok(a.alice, "GET", "/api/v1/pocket/search?q=kettle&limit=1", nil)); len(got) != 1 || got[0]["title"] != "Kettle" {
		t.Fatalf("limit 1: %v", got)
	}
	if got := items(t, a.ok(a.alice, "GET", "/api/v1/pocket/search?q=", nil)); len(got) != 0 {
		t.Fatalf("no words: %v", got)
	}
	// another person's watches never appear
	if got := titles(t, a.ok(a.bob, "GET", "/api/v1/pocket/search?q=kettle", nil)); got != "Bob's kettle" {
		t.Fatalf("bob finds %q", got)
	}
	if got := items(t, a.ok(a.bob, "GET", "/api/v1/pocket/search?q=news", nil)); len(got) != 0 {
		t.Fatalf("bob finds alice's watch: %v", got)
	}
	a.expect(401, "", "GET", "/api/v1/pocket/search?q=kettle", nil)
}

func TestPocketDay(t *testing.T) {
	a := newApp(t, nil)
	kettle := a.watchPage(a.alice, "/kettle", "Kettle", `<p>Price: 49 EUR</p>`, nil)
	news := a.watchPage(a.alice, "/news", "Town news", `<p>Nothing yet</p>`, nil)
	quiet := a.watchPage(a.alice, "/quiet", "Quiet page", `<p>Stock: 5</p>`, map[string]any{"notify_on": "added", "trigger_text": "never there"})
	bobs := a.watchPage(a.bob, "/bob", "Bob's page", `<p>one</p>`, nil)

	c1 := a.changePage(a.alice, kettle, "/kettle", `<p>Price: 45 EUR</p>`)
	c2 := a.changePage(a.alice, kettle, "/kettle", `<p>Price: 42 EUR</p>`)
	c3 := a.changePage(a.alice, news, "/news", `<p>The bridge opens on Friday</p>`)
	late := a.changePage(a.alice, news, "/news", `<p>The bridge opens on Saturday</p>`)
	skipped := a.changePage(a.alice, quiet, "/quiet", `<p>Stock: 4</p>`)
	theirs := a.changePage(a.bob, bobs, "/bob", `<p>two</p>`)
	if skipped["matched"] != false {
		t.Fatalf("a change outside the watch's rules counted: %v", skipped)
	}
	a.changeAt(c1, "2026-03-10T08:05:00Z")
	a.changeAt(c2, "2026-03-10T14:30:00.25Z")
	a.changeAt(c3, "2026-03-10T00:00:00.5Z")
	a.changeAt(late, "2026-03-10T23:30:00Z") // already the 11th in Sarajevo
	a.changeAt(skipped, "2026-03-10T10:00:00Z")
	a.changeAt(theirs, "2026-03-10T09:00:00Z")

	day := a.ok(a.alice, "GET", "/api/v1/pocket/day/2026-03-10?tz=UTC", nil)
	got := items(t, day)
	if day["date"] != "2026-03-10" || day["summary"] != "4 changes on 2 pages" || len(got) != 4 {
		t.Fatalf("the day: %v", day)
	}
	by := map[string]map[string]any{}
	for _, it := range got {
		by[it["id"].(string)] = it
		if it["kind"] != "changed" {
			t.Errorf("kind of %v", it)
		}
	}
	first := by[sid(num(c1, "id"))]
	if first["title"] != "Kettle" || first["time"] != "08:05" || first["sub"] != c1["summary"] || first["sub"] == "" || first["url"] != "/changes/"+sid(num(c1, "id")) {
		t.Fatalf("a change: %v (summary %q)", first, c1["summary"])
	}
	if by[sid(num(c3, "id"))]["time"] != "00:00" || by[sid(num(late, "id"))]["time"] != "23:30" || by[sid(num(c2, "id"))]["time"] != "14:30" {
		t.Fatalf("times: %v", got)
	}
	// the person's time zone moves the day's edges and the clock
	sar := a.ok(a.alice, "GET", "/api/v1/pocket/day/2026-03-10?tz=Europe/Sarajevo", nil)
	if got := items(t, sar); len(got) != 3 || sar["summary"] != "3 changes on 2 pages" {
		t.Fatalf("the 10th in Sarajevo: %v", sar)
	}
	next := a.ok(a.alice, "GET", "/api/v1/pocket/day/2026-03-11?tz=Europe/Sarajevo", nil)
	if got := items(t, next); len(got) != 1 || got[0]["time"] != "00:30" || got[0]["title"] != "Town news" || next["summary"] != "1 page changed" {
		t.Fatalf("the 11th in Sarajevo: %v", next)
	}
	// no zone said: the box's own, as GET /day/{date}
	when, _ := time.Parse(time.RFC3339, "2026-03-10T08:05:00Z")
	local := a.ok(a.alice, "GET", "/api/v1/pocket/day/"+when.Local().Format("2006-01-02"), nil)
	found := false
	for _, it := range items(t, local) {
		found = found || it["id"] == sid(num(c1, "id")) && it["time"] == when.Local().Format("15:04")
	}
	if !found {
		t.Fatalf("the box's day: %v", local)
	}
	// a day with nothing says nothing
	empty := a.ok(a.alice, "GET", "/api/v1/pocket/day/2026-03-09?tz=UTC", nil)
	if len(items(t, empty)) != 0 || empty["summary"] != nil || empty["date"] != "2026-03-09" {
		t.Fatalf("an empty day: %v", empty)
	}
	a.expect(400, a.alice, "GET", "/api/v1/pocket/day/yesterday", nil)
	// per person
	bob := a.ok(a.bob, "GET", "/api/v1/pocket/day/2026-03-10?tz=UTC", nil)
	if got := items(t, bob); len(got) != 1 || got[0]["title"] != "Bob's page" || bob["summary"] != "1 page changed" {
		t.Fatalf("bob's day: %v", bob)
	}
	a.expect(401, "", "GET", "/api/v1/pocket/day/2026-03-10", nil)
}

func TestPocketWidget(t *testing.T) {
	a := newApp(t, nil)
	// nobody watches anything: nothing to show
	if w := a.ok(a.alice, "GET", "/api/v1/pocket/widget", nil); len(items(t, w)) != 0 || w["line"] != nil || w["metrics"] != nil {
		t.Fatalf("an empty widget: %v", w)
	}
	kettle := a.watchPage(a.alice, "/kettle", "Kettle", `<p>Price: 49 EUR</p>`, nil)
	news := a.watchPage(a.alice, "/news", "Town news", `<p>Nothing yet</p>`, nil)
	ads := a.watchPage(a.alice, "/ads", "Ads page", `<p>Ad one</p>`, nil)
	metrics := func(w map[string]any) string {
		var out []string
		for _, m := range w["metrics"].([]any) {
			out = append(out, fmt.Sprint(m.(map[string]any)["label"], "=", m.(map[string]any)["value"]))
		}
		return strings.Join(out, " ")
	}
	// watching, nothing changed yet: the numbers, no line
	w := a.ok(a.alice, "GET", "/api/v1/pocket/widget", nil)
	if len(items(t, w)) != 0 || w["line"] != nil || metrics(w) != "Watching=3 New changes=0" || w["url"] != "/changes" {
		t.Fatalf("before any change: %v", w)
	}

	seen := a.changePage(a.alice, news, "/news", `<p>The bridge opens on Friday</p>`)
	a.ok(a.alice, "POST", "/api/v1/changes/seen", nil)
	a.changePage(a.alice, kettle, "/kettle", `<p>Price: 45 EUR</p>`)
	w = a.ok(a.alice, "GET", "/api/v1/pocket/widget", nil)
	if w["line"] != "1 new change" || titles(t, w) != "Kettle | Town news" {
		t.Fatalf("one new change: %v", w)
	}
	last := a.changePage(a.alice, kettle, "/kettle", `<p>Price: 42 EUR</p>`)
	unimportant := a.changePage(a.alice, ads, "/ads", `<p>Ad two</p>`)
	a.ok(a.alice, "POST", "/api/v1/changes/"+sid(num(unimportant, "id"))+"/dismiss", nil)
	w = a.ok(a.alice, "GET", "/api/v1/pocket/widget", nil)
	got := items(t, w)
	// one row per page with its latest change: new ones first, then what was looked at; a change set aside is left out
	if w["line"] != "2 new changes on 1 page" || metrics(w) != "Watching=3 New changes=2" || len(got) != 2 ||
		got[0]["title"] != "Kettle" || got[0]["id"] != sid(num(last, "id")) || got[0]["detail"] != last["summary"] || got[0]["url"] != "/changes/"+sid(num(last, "id")) ||
		got[1]["title"] != "Town news" || got[1]["id"] != sid(num(seen, "id")) {
		t.Fatalf("the widget: %v", w)
	}
	// a new change on each of two pages needs no "on 2 pages"
	a.changePage(a.alice, news, "/news", `<p>The bridge opens on Saturday</p>`)
	a.ok(a.alice, "POST", "/api/v1/changes/"+sid(num(last, "id"))+"/seen", nil)
	if w = a.ok(a.alice, "GET", "/api/v1/pocket/widget", nil); w["line"] != "2 new changes" || titles(t, w) != "Town news | Kettle" {
		t.Fatalf("two pages with one new change each: %v", w)
	}
	// a page that stopped answering is said too
	a.site.mu.Lock()
	delete(a.site.pages, "/news")
	a.site.mu.Unlock()
	a.check(a.alice, news)
	// a paused watch is not failing; resumed here without its check, so only the test checks it
	if _, err := a.st.DB.Exec(`UPDATE watches SET paused = 0, next_check_at = '9999-01-01T00:00:00Z' WHERE id = ?`, news); err != nil {
		t.Fatal(err)
	}
	w = a.ok(a.alice, "GET", "/api/v1/pocket/widget", nil)
	if w["line"] != "2 new changes, 1 page can't be read" || metrics(w) != "Watching=3 New changes=2 Failing=1" {
		t.Fatalf("with a failing page: %v", w)
	}
	// reading the widget checks no page
	var checks int
	_ = a.st.DB.QueryRow(`SELECT COUNT(1) FROM checks`).Scan(&checks)
	for range 3 {
		a.ok(a.alice, "GET", "/api/v1/pocket/widget", nil)
	}
	var after int
	_ = a.st.DB.QueryRow(`SELECT COUNT(1) FROM checks`).Scan(&after)
	if after != checks {
		t.Fatalf("the widget checked pages: %d → %d checks", checks, after)
	}
	// per person
	if w := a.ok(a.bob, "GET", "/api/v1/pocket/widget", nil); len(items(t, w)) != 0 || w["line"] != nil || w["metrics"] != nil {
		t.Fatalf("bob's widget: %v", w)
	}
	a.expect(401, "", "GET", "/api/v1/pocket/widget", nil)
}

func capture(a *app, tok, requestID, text string, more map[string]any) (int, map[string]any) {
	in := map[string]any{"request_id": requestID, "kind": "watch", "text": text}
	for k, v := range more {
		in[k] = v
	}
	code, m, _ := a.call(tok, "POST", "/api/v1/pocket/capture", in)
	return code, m
}

func TestPocketCapture(t *testing.T) {
	a := newApp(t, nil)
	a.site.set("/kettle", htmlPage(`<p>Price: 49 EUR</p>`))
	addr := a.site.url("/kettle")
	keep := func(tok, requestID, text string, more map[string]any) map[string]any {
		t.Helper()
		code, m := capture(a, tok, requestID, text, more)
		if code != 201 {
			t.Fatalf("capture %q → %d %v", text, code, m)
		}
		return a.ok(tok, "GET", "/api/v1/watches/"+m["id"].(string), nil)
	}
	refuse := func(tok, requestID, text, want string) {
		t.Helper()
		if code, m := capture(a, tok, requestID, text, nil); code != 400 || m["error"] != want {
			t.Fatalf("capture %q → %d %v, want 400 %q", text, code, m, want)
		}
	}
	const needsAddress = "write the page's address, like https://example.com/page"

	// the person's own defaults, as a watch added in the app with only its address
	a.ok(a.alice, "PUT", "/api/v1/settings", map[string]any{"default_interval": 120, "default_notify_mode": "digest", "default_fetch_mode": "fast", "default_noise": true})
	code, first := capture(a, a.alice, "req-0000000000000001", addr, nil)
	if code != 201 || first["url"] != "/watches/"+first["id"].(string) || first["title"] != "127.0.0.1" {
		t.Fatalf("capture: %d %v", code, first)
	}
	own := a.ok(a.alice, "POST", "/api/v1/watches", map[string]any{"url": addr})
	w := a.ok(a.alice, "GET", "/api/v1/watches/"+first["id"].(string), nil)
	for _, k := range []string{"url", "title", "tags", "notes", "selector", "exclude", "fetch_mode", "browser_wait", "headers", "screenshot", "noise_filter", "ignore_text",
		"notify_on", "trigger_text", "ai_condition", "notify_mode", "interval_minutes", "paused", "source"} {
		if fmt.Sprint(w[k]) != fmt.Sprint(own[k]) {
			t.Errorf("%s = %v, the app's own add gives %v", k, w[k], own[k])
		}
	}
	if w["url"] != addr || num(w, "interval_minutes") != 120 || w["notify_mode"] != "digest" || w["fetch_mode"] != "fast" || w["paused"] != false || w["source"] != "" {
		t.Fatalf("the watch: %v", w)
	}
	// it is read right away, like any new watch
	deadline := time.Now().Add(10 * time.Second)
	for (w["last_checked_at"] == "" || w["status"] == "checking") && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		w = a.ok(a.alice, "GET", "/api/v1/watches/"+first["id"].(string), nil)
	}
	if w["status"] != "ok" || w["page_title"] != "Fake shop" {
		t.Fatalf("the first check did not run: %v", w)
	}
	// the same request again: the same answer, nothing added
	count := func(tok string) int { return len(a.list(tok, "/api/v1/watches")) }
	if code, again := capture(a, a.alice, "req-0000000000000001", addr, nil); code != 201 || again["id"] != first["id"] || again["title"] != first["title"] ||
		again["url"] != first["url"] || count(a.alice) != 2 {
		t.Fatalf("a retry: %d %v, %d watches", code, again, count(a.alice))
	}
	// the same id with other words is refused
	refuse(a.alice, "req-0000000000000001", a.site.url("/other"), "this was already kept with other words")

	// a title names the watch; other words of the line are its notes
	if w := keep(a.alice, "req-0000000000000002", addr, map[string]any{"title": "  Kettle   price "}); w["title"] != "Kettle price" || w["name"] != "Kettle price" || w["notes"] != "" {
		t.Fatalf("with a title: %v", w)
	}
	if w := keep(a.alice, "req-0000000000000003", "Look at this "+addr+". Tell me when it is cheaper", nil); w["url"] != addr || w["title"] != "" ||
		w["notes"] != "Look at this  Tell me when it is cheaper" {
		t.Fatalf("an address inside a sentence: %v", w)
	}
	if w := keep(a.alice, "req-0000000000000004", "", map[string]any{"title": "HTTP://127.0.0.1:1/Page?a=1&b=2"}); w["url"] != "HTTP://127.0.0.1:1/Page?a=1&b=2" || w["title"] != "" {
		t.Fatalf("only an address, sent as the title: %v", w)
	}

	// what cannot be watched is refused with words the person can act on
	before := count(a.alice)
	refuse(a.alice, "req-0000000000000005", "the kettle page", needsAddress)
	refuse(a.alice, "req-0000000000000006", "www.example.com/kettle", needsAddress)
	refuse(a.alice, "req-0000000000000007", "ftp://example.com/file", needsAddress)
	refuse(a.alice, "req-0000000000000008", "https:///nowhere", "not a web address (it needs http:// or https://)")
	if count(a.alice) != before {
		t.Fatal("a refused line was kept")
	}
	// a refused line can be sent again, corrected, under a new id
	keep(a.alice, "req-0000000000000009", addr, nil)

	// what the route checks itself
	a.expect(400, a.alice, "POST", "/api/v1/pocket/capture", map[string]any{"request_id": "short", "text": addr})
	a.expect(400, a.alice, "POST", "/api/v1/pocket/capture", map[string]any{"request_id": "req-0000000000000010", "kind": "change", "text": addr})
	a.expect(400, a.alice, "POST", "/api/v1/pocket/capture", map[string]any{"request_id": "req-0000000000000010"})
	// no kind means a watch
	if code, m, _ := a.call(a.alice, "POST", "/api/v1/pocket/capture", map[string]any{"request_id": "req-0000000000000011", "text": addr}); code != 201 {
		t.Fatalf("no kind: %d %v", code, m)
	}

	// per person: a request id is one's own, and nothing lands with the other
	mine := count(a.alice)
	if w := keep(a.bob, "req-0000000000000001", a.site.url("/bob"), nil); w["url"] != a.site.url("/bob") {
		t.Fatalf("bob's watch: %v", w)
	}
	if count(a.bob) != 1 || count(a.alice) != mine {
		t.Fatalf("bob has %d watches, alice %d (was %d)", count(a.bob), count(a.alice), mine)
	}
	a.expect(401, "", "POST", "/api/v1/pocket/capture", map[string]any{"request_id": "req-0000000000000012", "text": addr})
}

// A kept line tells the Core bus once, a retry not again.
func TestPocketCaptureEvent(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.CoreURL = "http://core.invalid"
	st := openTestStore(t, cfg)
	s := New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	ctx := context.Background()
	if err := s.core.Init(ctx); err != nil {
		t.Fatal(err)
	}
	u := &store.User{Email: "ana@example.com", Name: "Ana", Role: "admin"}
	if err := st.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	tok, _, _ := st.CreateAPIToken(ctx, u.ID, "t")
	ts := httptest.NewServer(s.Router())
	t.Cleanup(ts.Close)
	a := &app{t: t, url: ts.URL, s: s, st: st}
	var id string
	for range 2 {
		code, m := capture(a, tok, "req-0000000000000001", "https://shop.example.com/kettle", map[string]any{"title": "Kettle"})
		if code != 201 {
			t.Fatalf("capture → %d %v", code, m)
		}
		id = m["id"].(string)
	}
	rows, err := st.DB.QueryContext(ctx, `SELECT body FROM pocket_outbox ORDER BY created_at, rowid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	added := 0
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			t.Fatal(err)
		}
		var ev struct {
			Topic   string
			Subject string
			Payload map[string]any
		}
		if err := json.Unmarshal(b, &ev); err != nil {
			t.Fatal(err)
		}
		if ev.Topic != "lookout.watch_added" {
			continue
		}
		added++
		if ev.Subject != "watch:"+id || ev.Payload["url"] != "https://shop.example.com/kettle" || ev.Payload["source"] != "" {
			t.Errorf("event = %q %v", ev.Subject, ev.Payload)
		}
	}
	if added != 1 {
		t.Fatalf("watch_added events = %d, want 1", added)
	}
}

// A link token with one pocket:* scope reaches its one route and nothing else.
func TestPocketLinkTokens(t *testing.T) {
	a, _ := newCapApp(t)
	kettle := a.watchPage(a.alice, "/kettle", "Kettle", `<p>Price: 49 EUR</p>`, nil)
	change := a.changePage(a.alice, kettle, "/kettle", `<p>Price: 45 EUR</p>`)
	a.changeAt(change, "2026-03-10T08:05:00Z")
	const day = "2026-03-10?tz=UTC"
	line := func(requestID string) map[string]any {
		return map[string]any{"request_id": requestID, "kind": "watch", "text": a.site.url("/linked"), "title": "Linked"}
	}
	for _, c := range []struct {
		tok, method, path string
		body              any
		want              int
	}{
		{"pcx_alice_search", "GET", "/api/v1/pocket/search?q=kettle", nil, 200},
		{"pcx_alice_day", "GET", "/api/v1/pocket/day/" + day, nil, 200},
		{"pcx_alice_widget", "GET", "/api/v1/pocket/widget", nil, 200},
		{"pcx_alice_capture", "POST", "/api/v1/pocket/capture", line("req-0000000000000001"), 201},
		{"pcx_alice_widget", "GET", "/api/v1/auth/me", nil, 200},
		// another capability's route
		{"pcx_alice_search", "GET", "/api/v1/pocket/day/" + day, nil, 403},
		{"pcx_alice_search", "GET", "/api/v1/pocket/widget", nil, 403},
		{"pcx_alice_search", "POST", "/api/v1/pocket/capture", line("req-0000000000000002"), 403},
		{"pcx_alice_day", "GET", "/api/v1/pocket/search?q=kettle", nil, 403},
		{"pcx_alice_day", "GET", "/api/v1/pocket/widget", nil, 403},
		{"pcx_alice_widget", "GET", "/api/v1/pocket/search?q=kettle", nil, 403},
		{"pcx_alice_widget", "GET", "/api/v1/pocket/day/" + day, nil, 403},
		{"pcx_alice_widget", "POST", "/api/v1/pocket/capture", line("req-0000000000000003"), 403},
		{"pcx_alice_capture", "GET", "/api/v1/pocket/widget", nil, 403},
		{"pcx_alice_capture", "GET", "/api/v1/pocket/search?q=kettle", nil, 403},
		// the rest of the app
		{"pcx_alice_widget", "GET", "/api/v1/dashboard", nil, 403},
		{"pcx_alice_widget", "GET", "/api/v1/changes", nil, 403},
		{"pcx_alice_search", "GET", "/api/v1/watches?q=kettle", nil, 403},
		{"pcx_alice_search", "GET", "/api/v1/watches/" + sid(kettle), nil, 403},
		{"pcx_alice_day", "GET", "/api/v1/day/2026-03-10", nil, 403},
		{"pcx_alice_day", "GET", "/api/v1/changes/" + sid(num(change, "id")), nil, 403},
		{"pcx_alice_capture", "POST", "/api/v1/watches", map[string]any{"url": a.site.url("/x")}, 403},
		{"pcx_alice_capture", "POST", "/api/v1/preview", map[string]any{"url": a.site.url("/x")}, 403},
		{"pcx_alice_capture", "PUT", "/api/v1/watches/" + sid(kettle), map[string]any{"title": "x"}, 403},
		{"pcx_alice_capture", "POST", "/api/v1/watches/" + sid(kettle) + "/check", nil, 403},
		{"pcx_alice_capture", "DELETE", "/api/v1/watches/" + sid(kettle), nil, 403},
		{"pcx_alice_capture", "GET", "/api/v1/settings", nil, 403},
		// the app's own scopes do not open the new routes
		{"pcx_alice_read", "GET", "/api/v1/pocket/search?q=kettle", nil, 403},
		{"pcx_alice_read", "GET", "/api/v1/pocket/day/" + day, nil, 403},
		{"pcx_alice_read", "GET", "/api/v1/pocket/widget", nil, 403},
		{"pcx_alice_write", "POST", "/api/v1/pocket/capture", line("req-0000000000000004"), 403},
		// and still open what they always did
		{"pcx_alice_read", "GET", "/api/v1/watches?q=kettle", nil, 200},
		{"pcx_alice_read", "GET", "/api/v1/day/2026-03-10", nil, 200},
		{"pcx_nobody", "GET", "/api/v1/pocket/widget", nil, 401},
	} {
		if code, m, _ := a.call(c.tok, c.method, c.path, c.body); code != c.want {
			t.Errorf("%s %s %s → %d, want %d (%v)", c.tok, c.method, c.path, code, c.want, m)
		}
	}
	// the token acts for its person only
	if got := titles(t, a.ok("pcx_alice_search", "GET", "/api/v1/pocket/search?q=kettle", nil)); got != "Kettle" {
		t.Fatalf("alice through the link finds %q", got)
	}
	if got := items(t, a.ok("pcx_bob_search", "GET", "/api/v1/pocket/search?q=kettle", nil)); len(got) != 0 {
		t.Fatalf("bob through the link finds alice's watch: %v", got)
	}
	if w := a.ok("pcx_bob_widget", "GET", "/api/v1/pocket/widget", nil); len(items(t, w)) != 0 {
		t.Fatalf("bob's widget through the link: %v", w)
	}
	if d := a.ok("pcx_alice_day", "GET", "/api/v1/pocket/day/"+day, nil); len(items(t, d)) != 1 {
		t.Fatalf("alice's day through the link: %v", d)
	}
	if d := a.ok("pcx_bob_day", "GET", "/api/v1/pocket/day/"+day, nil); len(items(t, d)) != 0 {
		t.Fatalf("bob's day through the link: %v", d)
	}
	// only the one allowed capture landed, as the person's own watch (not one the sending app reads for them); a retry adds nothing
	code, again, _ := a.call("pcx_alice_capture", "POST", "/api/v1/pocket/capture", line("req-0000000000000001"))
	if code != 201 {
		t.Fatalf("retry through the link: %d %v", code, again)
	}
	if n := len(a.list(a.alice, "/api/v1/watches?source=hub")); n != 0 {
		t.Fatalf("a captured watch must not be marked as started by an app: %d", n)
	}
	var ws []any
	for _, w := range a.list(a.alice, "/api/v1/watches") {
		if w.(map[string]any)["title"] == "Linked" {
			ws = append(ws, w)
		}
	}
	if len(ws) != 1 || len(a.list(a.alice, "/api/v1/watches")) != 2 || len(a.list(a.bob, "/api/v1/watches")) != 0 {
		t.Fatalf("watches the link added: %v", ws)
	}
	if w := ws[0].(map[string]any); w["title"] != "Linked" || w["url"] != a.site.url("/linked") || sid(num(w, "id")) != again["id"] {
		t.Fatalf("the watch: %v, the answer: %v", w, again)
	}
}

func TestPocketLinkScopes(t *testing.T) {
	req := func(m, p string) *http.Request { return httptest.NewRequest(m, p, nil) }
	for _, c := range []struct {
		scope string
		r     *http.Request
		want  bool
	}{
		{core.ScopeSearch, req("GET", "/api/v1/pocket/search?q=x"), true},
		{core.ScopeDay, req("GET", "/api/v1/pocket/day/2026-01-02"), true},
		{core.ScopeWidget, req("GET", "/api/v1/pocket/widget"), true},
		{core.ScopeCapture, req("POST", "/api/v1/pocket/capture"), true},
		{core.ScopeWidget, req("GET", "/api/v1/auth/me"), true},
		{core.ScopeSearch, req("POST", "/api/v1/pocket/search"), false},
		{core.ScopeSearch, req("GET", "/api/v1/watches"), false},
		{core.ScopeDay, req("GET", "/api/v1/pocket/day/today"), false},
		{core.ScopeDay, req("GET", "/api/v1/day/2026-01-02"), false},
		{core.ScopeWidget, req("GET", "/api/v1/dashboard"), false},
		{core.ScopeCapture, req("GET", "/api/v1/pocket/capture"), false},
		{core.ScopeCapture, req("POST", "/api/v1/watches"), false},
		{"watches:read", req("GET", "/api/v1/pocket/search"), false},
		{"watches:read", req("GET", "/api/v1/pocket/day/2026-01-02"), false},
		{"watches:read", req("GET", "/api/v1/pocket/widget"), false},
		{"watches:write", req("POST", "/api/v1/pocket/capture"), false},
	} {
		if got := linkAllows([]string{c.scope}, c.r); got != c.want {
			t.Errorf("%s %s %s = %v, want %v", c.scope, c.r.Method, c.r.URL.Path, got, c.want)
		}
	}
}

// Lookout tells Core what it can do, so Search, Day and Hub find it on their own.
func TestManifestCapabilities(t *testing.T) {
	a, fc := newCapApp(t)
	if err := a.s.registerWithCore(context.Background()); err != nil {
		t.Fatal(err)
	}
	fc.mu.Lock()
	m := fc.manifest
	fc.mu.Unlock()
	if m["slug"] != "lookout" || m["category"] != "Tools" {
		t.Fatalf("manifest: %v %v", m["slug"], m["category"])
	}
	search, _ := m["search"].(map[string]any)
	day, _ := m["day"].(map[string]any)
	widget, _ := m["widget"].(map[string]any)
	cap, _ := m["capture"].(map[string]any)
	if search == nil || fmt.Sprint(search["bangs"]) != "[lookout watch]" || fmt.Sprint(search["kinds"]) != "[watched page]" ||
		day == nil || day["label"] != "Lookout" || day["one"] != "change" || day["many"] != "changes" || widget == nil || widget["title"] != "Page changes" || cap == nil {
		t.Fatalf("capabilities: %v %v %v %v", search, day, widget, cap)
	}
	if k := cap["kinds"].([]any)[0].(map[string]any); k["kind"] != "watch" || fmt.Sprint(k["fields"]) != "[title]" {
		t.Fatalf("capture kinds: %v", cap)
	}
	offered := map[string]bool{}
	for _, o := range m["offers"].([]any) {
		offered[o.(map[string]any)["scope"].(string)] = true
	}
	for _, sc := range []string{"watches:read", "watches:write", core.ScopeMoveExport, core.ScopeMoveImport, core.ScopeSearch, core.ScopeDay, core.ScopeWidget, core.ScopeCapture} {
		if !offered[sc] {
			t.Errorf("%s is not offered", sc)
		}
	}
}
