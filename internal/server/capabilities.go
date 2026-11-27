package server

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-lookout/internal/store"
)

// The four routes behind `capabilities` (links.go): /api/v1/pocket/search,
// /day/{date}, /widget and /capture. Each runs as the request's person and
// sees only their watches and changes; the SDK's handlers check and cap what
// goes in and out. None of the three reading routes checks a page: they read
// what the scheduler already stored.

func watchPath(id int64) string  { return fmt.Sprintf("/watches/%d", id) }
func changePath(id int64) string { return fmt.Sprintf("/changes/%d", id) }

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// ---------- search ----------

// searchWords are the words of a query that can find something: lowercase,
// and only those with a letter or a digit in them ("??" finds nothing).
func searchWords(q string) []string {
	var out []string
	for _, w := range strings.Fields(strings.ToLower(q)) {
		if strings.IndexFunc(w, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0 {
			out = append(out, w)
		}
	}
	return out
}

func hasAll(text string, words []string) bool {
	for _, w := range words {
		if !strings.Contains(text, w) {
			return false
		}
	}
	return true
}

// pocketSearch finds the person's watches by name, page title, address, tags,
// notes and what changed last: every word must be there. A match in the name
// comes first; otherwise the order is the list's (new changes on top).
func (s *Server) pocketSearch(r *http.Request, q string, limit int) ([]core.SearchItem, error) {
	words := searchWords(q)
	if len(words) == 0 {
		return nil, nil
	}
	ws, err := s.store.ListWatches(r.Context(), actorID(r.Context()), store.WatchFilter{})
	if err != nil {
		return nil, err
	}
	out := []core.SearchItem{}
	for _, w := range ws {
		name := strings.ToLower(w.Name + "\n" + w.Title + "\n" + w.PageTitle)
		it := core.SearchItem{ID: strconv.FormatInt(w.ID, 10), Title: w.Name, Snippet: firstNonEmpty(w.LastSummary, w.URL), Kind: "watched page",
			Date: w.LastChangedAt, URL: watchPath(w.ID), Link: w.URL, Score: 2}
		if !hasAll(name, words) {
			if !hasAll(name+"\n"+strings.ToLower(w.URL+"\n"+w.Tags+"\n"+w.Notes+"\n"+w.LastSummary), words) {
				continue
			}
			it.Score = 1
		}
		out = append(out, it)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ---------- day ----------

// pocketDay is the person's day in Lookout: the changes that counted, as
// GET /day/{date} lists them, in the caller's time zone when it said one.
func (s *Server) pocketDay(r *http.Request, date string, tz *time.Location) (*core.Day, error) {
	ctx := r.Context()
	loc := time.Local
	if tz != nil {
		loc = tz
	}
	t, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return nil, nil
	}
	// bounds without the zone letter, so "…:00Z" and "…:00.123Z" both sort after them
	const bound = "2006-01-02T15:04:05"
	cs, err := s.store.ListChanges(ctx, actorID(ctx), store.ChangeFilter{From: t.UTC().Format(bound), To: t.AddDate(0, 0, 1).UTC().Format(bound), Limit: 500})
	if err != nil {
		return nil, err
	}
	if len(cs) == 0 {
		return nil, nil
	}
	day := &core.Day{Date: date}
	pages := map[int64]bool{}
	for _, c := range cs {
		pages[c.WatchID] = true
		at := ""
		if when, err := time.Parse(time.RFC3339Nano, c.At); err == nil {
			at = when.In(loc).Format("15:04")
		}
		day.Items = append(day.Items, core.DayItem{ID: strconv.FormatInt(c.ID, 10), Time: at, Title: c.WatchName, Sub: c.Summary, Kind: "changed", URL: changePath(c.ID)})
	}
	if len(cs) == len(pages) {
		day.Summary = plural(len(pages), "page changed", "pages changed")
	} else {
		day.Summary = plural(len(cs), "change", "changes") + " on " + plural(len(pages), "page", "pages")
	}
	return day, nil
}

// ---------- widget ----------

// pocketWidget is the dashboard in a few lines: how many pages are watched,
// what is new, and the pages that changed last with their latest change (new
// ones first; a page whose latest change was set aside as not important is
// left out). It reads the newest rows only and never checks a page.
func (s *Server) pocketWidget(r *http.Request) (*core.WidgetData, error) {
	ctx := r.Context()
	uid := actorID(ctx)
	stats := s.store.Stats(ctx, uid)
	if stats["watches"] == 0 {
		return nil, nil
	}
	out := &core.WidgetData{URL: "/changes", Metrics: []core.Metric{
		{Label: "Watching", Value: strconv.Itoa(stats["watches"])},
		{Label: "New changes", Value: strconv.Itoa(stats["unseen"])},
	}}
	var parts []string
	if n, pages := stats["unseen"], stats["changed_watches"]; n > 0 {
		line := plural(n, "new change", "new changes")
		if n != pages {
			// several on one page: say how many pages there are to look at
			line += " on " + plural(pages, "page", "pages")
		}
		parts = append(parts, line)
	}
	if n := stats["failing"]; n > 0 {
		out.Metrics = append(out.Metrics, core.Metric{Label: "Failing", Value: strconv.Itoa(n)})
		parts = append(parts, plural(n, "page can't be read", "pages can't be read"))
	}
	out.Line = strings.Join(parts, ", ")

	const rows, look = 8, 24
	shown := map[int64]bool{}
	for _, f := range []store.ChangeFilter{{Unseen: true, Limit: look}, {Limit: look}} {
		if len(out.Items) == rows {
			break
		}
		cs, err := s.store.ListChanges(ctx, uid, f)
		if err != nil {
			return nil, err
		}
		for _, c := range cs {
			if shown[c.WatchID] || len(out.Items) == rows {
				continue
			}
			shown[c.WatchID] = true
			if c.Dismissed {
				continue
			}
			out.Items = append(out.Items, core.WidgetItem{ID: strconv.FormatInt(c.ID, 10), Title: c.WatchName, Detail: c.Summary, URL: changePath(c.ID)})
		}
	}
	return out, nil
}

// ---------- capture ----------

// The first web address in a line, as /add reads shared text.
var addressRe = regexp.MustCompile(`(?i)https?://\S+`)

// cutAddress takes the first web address out of a line and returns it with
// what is left of the line.
func cutAddress(line string) (addr, rest string) {
	raw := addressRe.FindString(line)
	return strings.TrimRight(raw, ").,"), strings.TrimSpace(strings.Replace(line, raw, "", 1))
}

// pocketCapture starts watching the web address in a quick line, with what
// POST /watches gives a watch that says only its address: the person's
// defaults for how often, how to read the page and how to tell, the whole
// page. The title is the watch's name when one came (else the page's own
// title names it after the first check); other words of the line are kept as
// its notes. The same request_id again answers the same watch and adds
// nothing: the receipt is written in the transaction that adds the watch.
func (s *Server) pocketCapture(r *http.Request, in core.CaptureInput) (*core.CaptureResult, error) {
	ctx := r.Context()
	uid := actorID(ctx)
	op, err := core.NewOperation(fmt.Sprintf("lookout:user:%d:%s", uid, core.CapturePath), in.RequestID, in)
	if err != nil {
		return nil, err
	}
	var res core.CaptureResult
	replay := func() (*core.CaptureResult, error) {
		found, err := op.Replay(ctx, s.store.DB, &res)
		switch {
		case errors.Is(err, core.ErrOperationConflict):
			return nil, fmt.Errorf("%w: this was already kept with other words", core.ErrCapture)
		case err != nil || !found:
			return nil, err
		}
		return &res, nil
	}
	if kept, err := replay(); kept != nil || err != nil {
		return kept, err
	}

	title := strings.Join(strings.Fields(in.Title), " ")
	addr, notes := cutAddress(in.Text)
	if addr == "" {
		// the address may be all there is, sent as the title
		addr, title = cutAddress(title)
	}
	if addr == "" {
		return nil, fmt.Errorf("%w: write the page's address, like https://example.com/page", core.ErrCapture)
	}
	st, _ := s.store.GetSettings(ctx, uid)
	wa := s.newWatch(st)
	wa.URL, wa.Title, wa.Notes = addr, title, notes
	wa.Source = linkApp(ctx)
	wa.Normalize(s.minInterval())
	if msg := validateWatch(wa); msg != "" {
		return nil, fmt.Errorf("%w: %s", core.ErrCapture, msg)
	}
	err = s.store.CreateWatch(ctx, uid, wa, s.watchEvent(ctx, "lookout.watch_added", wa), func(tx *sql.Tx) error {
		res = core.CaptureResult{ID: strconv.FormatInt(wa.ID, 10), Title: wa.Name, URL: watchPath(wa.ID)}
		return op.Save(ctx, tx, res)
	})
	if err != nil {
		// the same line sent twice at once: the other one kept it
		if kept, rerr := replay(); kept != nil || rerr != nil {
			return kept, rerr
		}
		return nil, err
	}
	s.nudge()
	return &res, nil
}
