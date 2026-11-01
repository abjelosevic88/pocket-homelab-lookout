package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/abjelosevic88/pocket-homelab-lookout/internal/page"
	"github.com/abjelosevic88/pocket-homelab-lookout/internal/store"
)

// ---- export / import ----

// exportDoc is the JSON export of one person's watches and their changes
// (no snapshots, pictures, users or tokens).
type exportDoc struct {
	App      string         `json:"app"`
	Version  string         `json:"version"`
	Exported string         `json:"exported_at"`
	Watches  []store.Watch  `json:"watches"`
	Changes  []store.Change `json:"changes"`
}

func (s *Server) handleExportJSON(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	doc := exportDoc{App: "pocket-lookout", Version: Version, Exported: store.Now()}
	doc.Watches, _ = s.store.ListWatches(ctx, uid, store.WatchFilter{})
	doc.Changes = []store.Change{}
	var before int64
	for {
		cs, err := s.store.ListChanges(ctx, uid, store.ChangeFilter{All: true, Before: before, Limit: 500})
		if err != nil || len(cs) == 0 {
			break
		}
		doc.Changes = append(doc.Changes, cs...)
		before = cs[len(cs)-1].ID
	}
	w.Header().Set("Content-Disposition", `attachment; filename="pocket-lookout-export.json"`)
	writeJSON(w, http.StatusOK, doc)
}

// existingKeys are the person's watches as url+selector, so imports skip them.
func (s *Server) existingKeys(r *http.Request) map[string]bool {
	ws, _ := s.store.ListWatches(r.Context(), actorID(r.Context()), store.WatchFilter{})
	have := map[string]bool{}
	for _, w := range ws {
		have[watchKey(w.URL, w.Selector)] = true
	}
	return have
}

func watchKey(url, sel string) string {
	return strings.TrimRight(strings.TrimSpace(url), "/") + "\x00" + strings.TrimSpace(sel)
}

// handleImport takes a Pocket Lookout export, a changedetection.io backup
// (its .zip or the url-watches.json / changedetection.json inside) or a plain
// list of addresses. Watches already there (same address and part) are skipped.
func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	limit := s.cfg.MaxBodyBytes
	if limit <= 0 {
		limit = 64 << 20
	}
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, limit))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "the file is too big")
		return
	}
	tags := r.URL.Query().Get("tags")
	if bytes.HasPrefix(body, []byte("PK")) {
		body, err = jsonFromZip(body)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	st, _ := s.store.GetSettings(ctx, uid)
	have := s.existingKeys(r)
	res := importResult{Notes: []string{}}
	trimmed := bytes.TrimSpace(body)
	switch {
	case len(trimmed) > 0 && trimmed[0] == '{':
		var probe map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &probe); err != nil {
			writeErr(w, http.StatusBadRequest, "the file is not valid JSON")
			return
		}
		switch {
		case probe["watching"] != nil:
			res.From = "changedetection.io"
			s.importChangedetection(r, trimmed, st, have, tags, &res)
		case probe["watches"] != nil:
			res.From = "Pocket Lookout"
			s.importLookout(r, trimmed, st, have, tags, &res)
		default:
			writeErr(w, http.StatusBadRequest, "this JSON is neither a Pocket Lookout export nor a changedetection.io backup")
			return
		}
	default:
		res.From = "list of addresses"
		s.importURLs(r, string(body), st, have, tags, &res)
	}
	if res.Added > 0 {
		s.nudge()
	}
	writeJSON(w, http.StatusOK, res)
}

type importResult struct {
	From    string   `json:"from"`
	Added   int      `json:"added"`
	Skipped int      `json:"skipped"`
	Notes   []string `json:"notes"`
}

func (s *Server) importOne(r *http.Request, wa *store.Watch, have map[string]bool, res *importResult) {
	wa.Normalize(s.minInterval())
	if msg := validateWatch(wa); msg != "" {
		res.Skipped++
		if len(res.Notes) < 20 {
			res.Notes = append(res.Notes, wa.URL+": "+msg)
		}
		return
	}
	k := watchKey(wa.URL, wa.Selector)
	if have[k] {
		res.Skipped++
		return
	}
	if err := s.store.CreateWatch(r.Context(), actorID(r.Context()), wa); err != nil {
		res.Skipped++
		return
	}
	have[k] = true
	res.Added++
}

func addTags(cur, extra string) string {
	if extra == "" {
		return cur
	}
	return store.JoinTags(cur + "," + extra)
}

// importURLs reads one address per line, optionally followed by tags
// ("https://… news, tech"), like changedetection.io's "import a list".
func (s *Server) importURLs(r *http.Request, text string, st store.Settings, have map[string]bool, tags string, res *importResult) {
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		u, rest, _ := strings.Cut(l, " ")
		if !strings.Contains(u, "://") {
			u = "https://" + u
		}
		if pu, err := page.CheckURL(u); err != nil || (!strings.Contains(pu.Hostname(), ".") && pu.Hostname() != "localhost" && !strings.Contains(pu.Hostname(), ":")) {
			res.Skipped++
			continue
		}
		wa := s.newWatch(st)
		wa.URL, wa.Tags = u, addTags(rest, tags)
		s.importOne(r, wa, have, res)
	}
}

func (s *Server) importLookout(r *http.Request, body []byte, st store.Settings, have map[string]bool, tags string, res *importResult) {
	var doc exportDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		res.Notes = append(res.Notes, "the export could not be read: "+err.Error())
		return
	}
	for _, x := range doc.Watches {
		wa := x
		wa.ID, wa.UserID, wa.LastSnapshotID, wa.NextCheckAt, wa.Unseen, wa.Status = 0, 0, nil, "", 0, ""
		wa.LastCheckedAt, wa.LastChangedAt, wa.LastError, wa.ErrorCount = "", "", "", 0
		wa.Tags = addTags(wa.Tags, tags)
		s.importOne(r, &wa, have, res)
	}
	if len(doc.Changes) > 0 {
		res.Notes = append(res.Notes, fmt.Sprintf("%d recorded changes were not imported: history starts again with the first check", len(doc.Changes)))
	}
}

// changedetection.io's datastore (url-watches.json / changedetection.json).
type cdStore struct {
	Settings struct {
		Application struct {
			Tags map[string]struct {
				Title string `json:"title"`
			} `json:"tags"`
		} `json:"application"`
	} `json:"settings"`
	Watching map[string]cdWatch `json:"watching"`
}

type cdWatch struct {
	URL              string            `json:"url"`
	Title            string            `json:"title"`
	Tag              string            `json:"tag"`  // old versions: "a, b"
	Tags             json.RawMessage   `json:"tags"` // new: tag uuids
	IncludeFilters   []string          `json:"include_filters"`
	CSSFilter        string            `json:"css_filter"` // old versions
	Subtractive      []string          `json:"subtractive_selectors"`
	IgnoreText       []string          `json:"ignore_text"`
	TriggerText      []string          `json:"trigger_text"`
	Paused           bool              `json:"paused"`
	FetchBackend     string            `json:"fetch_backend"`
	Headers          map[string]string `json:"headers"`
	Processor        string            `json:"processor"`
	NotificationMute bool              `json:"notification_muted"`
	FilterAdded      *bool             `json:"filter_text_added"`
	FilterRemoved    *bool             `json:"filter_text_removed"`
	WaitSeconds      int               `json:"webdriver_delay"`
	TimeBetween      struct {
		Weeks, Days, Hours, Minutes, Seconds int
	} `json:"time_between_check"`
}

func (s *Server) importChangedetection(r *http.Request, body []byte, st store.Settings, have map[string]bool, tags string, res *importResult) {
	var cd cdStore
	if err := json.Unmarshal(body, &cd); err != nil {
		res.Notes = append(res.Notes, "the backup could not be read: "+err.Error())
		return
	}
	xpath := 0
	for _, x := range cd.Watching {
		wa := s.newWatch(st)
		wa.URL, wa.Title = x.URL, x.Title
		var names []string
		var ids []string
		if json.Unmarshal(x.Tags, &ids) == nil {
			for _, id := range ids {
				if t, ok := cd.Settings.Application.Tags[id]; ok && t.Title != "" {
					names = append(names, t.Title)
				}
			}
		}
		wa.Tags = addTags(strings.Join(append(names, x.Tag), ","), tags)
		filters := x.IncludeFilters
		if x.CSSFilter != "" {
			filters = append(filters, x.CSSFilter)
		}
		var sels []string
		for _, f := range filters {
			f = strings.TrimSpace(f)
			if f == "" {
				continue
			}
			if strings.HasPrefix(f, "/") || strings.HasPrefix(strings.ToLower(f), "xpath") {
				xpath++
				continue
			}
			sels = append(sels, strings.TrimPrefix(f, "json:"))
		}
		if x.Processor == "restock_diff" {
			sels = []string{page.PriceSelector}
		}
		wa.Selector = strings.Join(sels, "\n")
		wa.Exclude = strings.Join(x.Subtractive, "\n")
		wa.IgnoreText = strings.Join(x.IgnoreText, "\n")
		wa.TriggerText = strings.Join(x.TriggerText, "\n")
		wa.Paused = x.Paused
		if x.NotificationMute {
			wa.NotifyMode = store.NotifyOff
		}
		switch x.FetchBackend {
		case "html_webdriver":
			wa.FetchMode = store.FetchBrowser
		case "html_requests":
			wa.FetchMode = store.FetchFast
		}
		if x.WaitSeconds > 0 {
			wa.BrowserWait = x.WaitSeconds
		}
		if x.FilterAdded != nil && x.FilterRemoved != nil {
			switch {
			case *x.FilterAdded && !*x.FilterRemoved:
				wa.NotifyOn = "added"
			case !*x.FilterAdded && *x.FilterRemoved:
				wa.NotifyOn = "removed"
			}
		}
		var hs []string
		for k, v := range x.Headers {
			hs = append(hs, k+": "+v)
		}
		wa.Headers = strings.Join(hs, "\n")
		tb := x.TimeBetween
		if mins := tb.Weeks*7*24*60 + tb.Days*24*60 + tb.Hours*60 + tb.Minutes + tb.Seconds/60; mins > 0 {
			wa.IntervalMinutes = mins
		}
		s.importOne(r, wa, have, res)
	}
	if xpath > 0 {
		res.Notes = append(res.Notes, fmt.Sprintf("%d XPath filters were left out (this app uses CSS selectors); those watches read the whole page until you pick a part", xpath))
	}
}

// jsonFromZip finds changedetection.io's datastore in its backup zip.
func jsonFromZip(b []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return nil, fmt.Errorf("the zip could not be opened")
	}
	for _, name := range []string{"url-watches.json", "changedetection.json"} {
		for _, f := range zr.File {
			if f.Name == name || strings.HasSuffix(f.Name, "/"+name) {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(io.LimitReader(rc, 64<<20))
			}
		}
	}
	return nil, fmt.Errorf("the zip has no url-watches.json or changedetection.json (is it a changedetection.io backup?)")
}
