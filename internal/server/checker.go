package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-lookout/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-lookout/internal/page"
	"github.com/abjelosevic88/pocket-homelab-lookout/internal/product"
	"github.com/abjelosevic88/pocket-homelab-lookout/internal/store"
)

var (
	checksRun     atomic.Int64
	checksFailed  atomic.Int64
	changesFound  atomic.Int64
	browserChecks atomic.Int64
)

// ---- scheduling ----
//
// Every 30 seconds (and right after "check now" / "check all") the scheduler
// takes the watches whose next check has come and hands them to CHECK_WORKERS
// workers. A watch is never checked twice at the same time, and two requests
// to the same site are at least two seconds apart.

func (s *Server) scheduler(ctx context.Context) {
	if s.cfg.DemoData {
		s.log.Info("demo data: watches are not checked on their own (Check now still works)")
		return
	}
	first := time.NewTimer(5 * time.Second)
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	sem := make(chan struct{}, max(1, s.cfg.CheckWorkers))
	for {
		select {
		case <-ctx.Done():
			return
		case <-first.C:
		case <-t.C:
		case <-s.wake:
		}
		s.dispatch(ctx, sem)
	}
}

// nudge makes the scheduler look for due watches now.
func (s *Server) nudge() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Server) claim(id int64) bool {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	if s.running[id] {
		return false
	}
	s.running[id] = true
	return true
}

func (s *Server) release(id int64) {
	s.runMu.Lock()
	delete(s.running, id)
	s.runMu.Unlock()
}

func (s *Server) isRunning(id int64) bool {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	return s.running[id]
}

func (s *Server) dispatch(ctx context.Context, sem chan struct{}) {
	due, err := s.store.DueWatches(ctx, 200)
	if err != nil {
		s.log.Warn("due watches", "err", err)
		return
	}
	for _, w := range due {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		if !s.claim(w.ID) {
			<-sem
			continue
		}
		go func(id, uid int64) {
			defer func() { <-sem; s.release(id) }()
			// read it again: a "check now" may have run since the list was made
			fresh, err := s.store.GetWatch(ctx, uid, id)
			if err != nil || fresh.Paused || fresh.NextCheckAt == "" || fresh.NextCheckAt > store.Now() {
				return
			}
			s.checkWatch(ctx, fresh)
		}(w.ID, w.UserID)
	}
}

// politeWait keeps two requests to the same site two seconds apart.
func (s *Server) politeWait(ctx context.Context, host string) {
	if host == "localhost" || strings.HasPrefix(host, "127.") || host == "[::1]" || host == "::1" {
		return
	}
	s.hostMu.Lock()
	last := s.hostAt[host]
	next := last.Add(2 * time.Second)
	now := time.Now()
	if next.Before(now) {
		next = now
	}
	s.hostAt[host] = next
	if len(s.hostAt) > 2000 {
		s.hostAt = map[string]time.Time{host: next}
	}
	s.hostMu.Unlock()
	if d := time.Until(next); d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
		}
	}
}

// ---- reading a page ----

type readResult struct {
	res          *page.Result
	ext          *page.Extracted
	via          string
	needsBrowser bool // auto switched to the browser and it worked
	ms           int
}

// read downloads and extracts a page the way a watch says: plain HTTP, the
// browser, or plain first and the browser when the page needs JavaScript.
func (s *Server) read(ctx context.Context, url, selector, exclude, headers, mode string, needsBrowser bool, wait int, shot bool) (*readResult, error) {
	start := time.Now()
	if err := s.fetch.Allowed(ctx, url); err != nil {
		return nil, err
	}
	hdr := page.ParseHeaders(headers)
	browser := s.fetch.Browser.Configured()
	useBrowser := mode == store.FetchBrowser || (mode == store.FetchAuto && needsBrowser && browser) || (shot && browser && mode != store.FetchFast)
	if mode == store.FetchBrowser && !browser {
		return nil, errors.New("this watch uses the browser, but no browser is set up on this server (BROWSER_URL)")
	}
	viaBrowser := func() (*readResult, error) {
		browserChecks.Add(1)
		res, err := s.fetch.Browser.Get(ctx, url, hdr, time.Duration(wait)*time.Second, shot)
		if err != nil {
			return &readResult{res: res, via: "browser", ms: int(time.Since(start).Milliseconds())}, err
		}
		if res.FinalURL != "" && res.FinalURL != url {
			if err := s.fetch.Allowed(ctx, res.FinalURL); err != nil {
				return nil, err
			}
		}
		ext, err := page.Extract(res, selector, exclude)
		return &readResult{res: res, ext: ext, via: "browser", ms: int(time.Since(start).Milliseconds())}, err
	}
	if useBrowser {
		return viaBrowser()
	}
	res, err := s.fetch.Fast(ctx, url, hdr)
	var ext *page.Extracted
	text := ""
	if err == nil {
		ext, err = page.Extract(res, selector, exclude)
		if ext != nil {
			text = ext.Text
		}
	}
	if mode == store.FetchAuto && browser && page.NeedsBrowser(res, text, err) {
		r2, err2 := viaBrowser()
		if err2 == nil {
			r2.needsBrowser = true
			return r2, nil
		}
		if err == nil {
			// the plain page was readable after all
			return &readResult{res: res, ext: ext, via: "fast", ms: int(time.Since(start).Milliseconds())}, nil
		}
		return r2, err2
	}
	var missing *page.ErrMissing
	if mode == store.FetchAuto && browser && errors.As(err, &missing) {
		// the part may only be drawn by JavaScript
		if r2, err2 := viaBrowser(); err2 == nil {
			r2.needsBrowser = true
			return r2, nil
		}
	}
	return &readResult{res: res, ext: ext, via: "fast", ms: int(time.Since(start).Milliseconds())}, err
}

// ---- one check ----

// outcome is what a check found (for "check now").
type outcome struct {
	OK      bool          `json:"ok"`
	Changed bool          `json:"changed"`
	First   bool          `json:"first"` // the first look: this is the baseline
	Change  *store.Change `json:"change,omitempty"`
	Error   string        `json:"error,omitempty"`
	Via     string        `json:"via"`
	MS      int           `json:"ms"`
	Ignored bool          `json:"ignored,omitempty"` // changed, but the rules said it does not count
}

func (s *Server) retryIn(w *store.Watch, errCount int) time.Duration {
	iv := time.Duration(w.IntervalMinutes) * time.Minute
	if errCount <= 2 && iv > 10*time.Minute {
		return 10 * time.Minute
	}
	if errCount > 4 {
		iv *= 2
	}
	return min(iv, 24*time.Hour)
}

// checkWatch reads a watch's page, compares it with the last snapshot and
// records what it found; a change that counts is told the way the watch says.
func (s *Server) checkWatch(ctx context.Context, w *store.Watch) outcome {
	checksRun.Add(1)
	st, _ := s.store.GetSettings(ctx, w.UserID)
	s.politeWait(ctx, w.Host)
	cctx, cancel := context.WithTimeout(ctx, s.cfg.BrowserTimeout+45*time.Second)
	rr, err := s.read(cctx, w.URL, w.Selector, w.Exclude, w.Headers, w.FetchMode, w.NeedsBrowser, w.BrowserWait, w.Screenshot)
	cancel()
	now := time.Now()
	interval := time.Duration(w.IntervalMinutes) * time.Minute
	rec := store.CheckRecord{At: now, Via: "fast", Interval: interval, KeepSnapshots: st.KeepSnapshots}
	if rr != nil {
		rec.Via, rec.MS = rr.via, rr.ms
		if rr.res != nil {
			rec.Status, rec.Bytes = rr.res.Status, len(rr.res.Body)
		}
	}
	if err != nil {
		checksFailed.Add(1)
		errCount := w.ErrorCount + 1
		rec.Error = err.Error()
		rec.RetryIn = s.retryIn(w, errCount)
		alert := st.NotifyErrors && errCount >= max(1, st.ErrorsAfter) && !w.FailingAlerted
		rec.FailingAlerted = w.FailingAlerted || alert
		if _, err := s.store.RecordCheck(ctx, w, rec, nil); err != nil {
			s.log.Warn("record check", "watch", w.ID, "err", err)
		}
		if alert {
			s.tellFailing(ctx, w, st, errCount, rec.Error)
		}
		return outcome{Error: rec.Error, Via: rec.Via, MS: rec.MS}
	}
	rec.OK = true
	rec.PageTitle = rr.ext.Title
	rec.NeedsBrowser = rr.needsBrowser
	out := outcome{OK: true, Via: rec.Via, MS: rec.MS}

	ignore := store.Lines(w.IgnoreText)
	cur := page.Prepare(rr.ext.Text, ignore, w.NoiseFilter)
	var prev *store.Snapshot
	if w.LastSnapshotID != nil {
		prev, _ = s.store.GetSnapshot(ctx, w.UserID, *w.LastSnapshotID)
	}
	shotPath := ""
	if rr.res != nil && len(rr.res.Shot) > 0 {
		if p, err := s.saveFile(fmt.Sprintf("shots/%d", w.ID), ".jpg", rr.res.Shot); err == nil {
			shotPath = p
		}
	}
	rec.ShotPath = shotPath
	switch {
	case prev == nil:
		out.First = true
		rec.Body, rec.Hash = rr.ext.Text, cur.Hash()
	case page.Prepare(prev.Body, ignore, w.NoiseFilter).Hash() == cur.Hash():
		// nothing that counts changed; a newer picture replaces the old one
	default:
		before := page.Prepare(prev.Body, ignore, w.NoiseFilter)
		diff, added, removed := page.Diff(before, cur, 2)
		rec.Body, rec.Hash = rr.ext.Text, cur.Hash()
		if added+removed > 0 {
			c := s.judge(ctx, w, st, diff, added, removed)
			if shotPath != "" && prev.ShotPath != "" {
				a, err1 := os.ReadFile(s.filePath(prev.ShotPath))
				b := rr.res.Shot
				if err1 == nil {
					c.Visual = page.VisualBoxes(a, b)
				}
			}
			rec.Change = c
			out.Changed, out.Change, out.Ignored = true, c, !c.Matched
		}
	}
	var hook func(tx *sql.Tx, c *store.Change) error
	if rec.Change != nil && rec.Change.Matched && s.core.Configured() {
		email := ""
		if u, err := s.store.GetUser(ctx, w.UserID); err == nil {
			email = u.Email
		}
		hook = func(tx *sql.Tx, c *store.Change) error {
			return core.QueueEvent(ctx, tx, "lookout.changed", fmt.Sprintf("watch:%d", w.ID), map[string]any{
				"user_email": email, "watch_id": w.ID, "change_id": c.ID, "title": w.Name, "url": w.URL, "tags": store.SplitTags(w.Tags),
				"summary": c.Summary, "added": c.Added, "removed": c.Removed, "added_lines": firstN(changedLines(c.Diff, "+"), 20),
				"removed_lines": firstN(changedLines(c.Diff, "-"), 20), "reason": c.Reason, "link": s.link(fmt.Sprintf("/changes/%d", c.ID)),
			})
		}
	}
	gone, err := s.store.RecordCheck(ctx, w, rec, hook)
	if err != nil {
		s.log.Warn("record check", "watch", w.ID, "err", err)
		s.removeFile(shotPath)
		return outcome{Error: "saving the check failed: " + err.Error(), Via: rec.Via, MS: rec.MS}
	}
	for _, p := range gone {
		s.removeFile(p)
	}
	if w.ErrorCount > 0 && w.FailingAlerted && st.NotifyErrors {
		s.send(ctx, st, notify.Message{Title: w.Name + " works again", Body: "Lookout can read " + w.Host + " again.", Priority: "success", Tags: []string{"white_check_mark"}, Link: s.link(fmt.Sprintf("/watches/%d", w.ID))})
	}
	if c := rec.Change; c != nil && c.Matched {
		changesFound.Add(1)
		if c.Notified == "now" {
			state := "sent"
			if err := s.tellChange(ctx, w, st, c); err != nil {
				state = "failed"
				s.log.Warn("change push", "watch", w.ID, "err", err)
			}
			_ = s.store.SetNotified(ctx, []int64{c.ID}, state)
			c.Notified = state
		}
	}
	return out
}

// judge decides whether a change counts (the watch's "only when" rules and
// its AI condition) and writes its summary.
func (s *Server) judge(ctx context.Context, w *store.Watch, st store.Settings, diff []page.Line, added, removed int) *store.Change {
	c := &store.Change{Added: added, Removed: removed, Matched: true, SummaryBy: "auto"}
	for _, l := range diff {
		c.Diff = append(c.Diff, store.DiffLine{Op: l.Op, Text: l.Text})
	}
	c.Summary = page.AutoSummary(diff, added, removed)
	addedLines, removedLines := page.Changed(diff)
	var relevant []string
	switch w.NotifyOn {
	case "added":
		relevant = addedLines
		if len(relevant) == 0 {
			c.Matched, c.Reason = false, "only lines went away (this watch tells you about new lines)"
		}
	case "removed":
		relevant = removedLines
		if len(relevant) == 0 {
			c.Matched, c.Reason = false, "only lines were added (this watch tells you when lines go away)"
		}
	default:
		relevant = append(append([]string{}, addedLines...), removedLines...)
	}
	if triggers := store.Lines(w.TriggerText); c.Matched && len(triggers) > 0 {
		hit := ""
		for _, l := range relevant {
			if r := page.MatchesAny(l, triggers); r != "" {
				hit = r
				break
			}
		}
		if hit == "" {
			c.Matched, c.Reason = false, "no changed line mentions "+quoteList(triggers)
		} else {
			c.Reason = "a changed line mentions “" + strings.Trim(hit, "/") + "”"
		}
	}
	wantAI := c.Matched && (strings.TrimSpace(w.AICondition) != "" || st.AISummaries)
	if wantAI {
		if cl, _, why := s.aiFor(ctx, w.UserID, st); cl != nil {
			actx, cancel := context.WithTimeout(ctx, 90*time.Second)
			v, err := s.aiJudge(actx, cl, w, addedLines, removedLines)
			cancel()
			switch {
			case err != nil:
				s.log.Debug("ai summary", "watch", w.ID, "err", err)
				if w.AICondition != "" {
					c.Reason = "the AI did not answer, so this counts (" + oneLine(err.Error(), 80) + ")"
				}
			default:
				if v.Summary != "" {
					c.Summary, c.SummaryBy = oneLine(v.Summary, 220), "ai"
				}
				if strings.TrimSpace(w.AICondition) != "" && v.Match != nil {
					c.Matched = *v.Match
					c.Reason = oneLine(v.Reason, 240)
					if c.Reason == "" && !c.Matched {
						c.Reason = "the AI says this does not match your condition"
					}
				}
			}
		} else if w.AICondition != "" {
			c.Reason = "AI is not available (" + why + "), so this counts"
		}
	}
	if c.Matched {
		switch w.NotifyMode {
		case store.NotifyNow:
			c.Notified = "now"
		case store.NotifyDigest:
			c.Notified = "digest"
		}
	}
	return c
}

func quoteList(ls []string) string {
	q := make([]string, 0, len(ls))
	for i, l := range ls {
		if i == 4 {
			q = append(q, "…")
			break
		}
		q = append(q, "“"+strings.Trim(l, "/")+"”")
	}
	return strings.Join(q, ", ")
}

func changedLines(d []store.DiffLine, op string) []string {
	var out []string
	for _, l := range d {
		if l.Op == op {
			out = append(out, l.Text)
		}
	}
	return out
}

func firstN(ls []string, n int) []string {
	if ls == nil {
		return []string{}
	}
	if len(ls) > n {
		return ls[:n]
	}
	return ls
}

// ---- telling ----

func (s *Server) link(path string) string {
	if s.cfg.BaseURL == "" {
		return ""
	}
	return s.cfg.BaseURL + path
}

func (s *Server) send(ctx context.Context, st store.Settings, m notify.Message) error {
	if st.NotifyURL == "" {
		return nil
	}
	return notify.New(st.NotifyURL, st.NotifyFormat).Send(ctx, m)
}

func (s *Server) tellChange(ctx context.Context, w *store.Watch, st store.Settings, c *store.Change) error {
	if st.NotifyURL == "" {
		return nil
	}
	body := c.Summary
	counts := []string{}
	if c.Added > 0 {
		counts = append(counts, fmt.Sprintf("+%d", c.Added))
	}
	if c.Removed > 0 {
		counts = append(counts, fmt.Sprintf("−%d", c.Removed))
	}
	if len(counts) > 0 {
		body += "\n" + strings.Join(counts, " ") + " lines · " + w.Host
	}
	return s.send(ctx, st, notify.Message{Title: w.Name + " changed", Body: body, Priority: "info", Tags: []string{"eyes"}, Link: s.link(fmt.Sprintf("/changes/%d", c.ID))})
}

func (s *Server) tellFailing(ctx context.Context, w *store.Watch, st store.Settings, n int, msg string) {
	s.publish(ctx, w.UserID, "lookout.failing", fmt.Sprintf("watch:%d", w.ID), map[string]any{"watch_id": w.ID, "title": w.Name, "url": w.URL, "error": msg, "failed_checks": n})
	if err := s.send(ctx, st, notify.Message{Title: w.Name + " cannot be read", Body: fmt.Sprintf("%d checks in a row failed: %s", n, msg), Priority: "warning", Tags: []string{"warning"}, Link: s.link(fmt.Sprintf("/watches/%d", w.ID))}); err != nil {
		s.log.Warn("failing push", "watch", w.ID, "err", err)
	}
}

// digests sends each person's daily summary at their hour.
func (s *Server) digests(ctx context.Context) {
	ids, err := s.store.SettingsUsers(ctx)
	if err != nil {
		return
	}
	now := time.Now()
	day := now.Format("2006-01-02")
	for _, uid := range ids {
		st, _ := s.store.GetSettings(ctx, uid)
		if now.Hour() != st.DigestHour {
			continue
		}
		s.digestMu.Lock()
		done := s.digestDay[uid] == day
		s.digestMu.Unlock()
		if done {
			continue
		}
		if _, err := s.sendDigest(ctx, uid, st); err != nil {
			s.log.Warn("daily summary", "user", uid, "err", err)
			continue
		}
		s.digestMu.Lock()
		s.digestDay[uid] = day
		s.digestMu.Unlock()
	}
}

// sendDigest sends the waiting changes as one push (0 = nothing waiting).
func (s *Server) sendDigest(ctx context.Context, uid int64, st store.Settings) (int, error) {
	pending, err := s.store.DigestPending(ctx, uid)
	if err != nil || len(pending) == 0 {
		return 0, err
	}
	var b strings.Builder
	watches := map[int64]bool{}
	for i, c := range pending {
		watches[c.WatchID] = true
		if i < 12 {
			fmt.Fprintf(&b, "• %s — %s\n", c.WatchName, oneLine(c.Summary, 120))
		}
	}
	if len(pending) > 12 {
		fmt.Fprintf(&b, "…and %d more", len(pending)-12)
	}
	title := fmt.Sprintf("%d changes on %d pages", len(pending), len(watches))
	if len(pending) == 1 {
		title = pending[0].WatchName + " changed"
	}
	ids := make([]int64, len(pending))
	for i, c := range pending {
		ids[i] = c.ID
	}
	if st.NotifyURL == "" {
		return len(pending), s.store.SetNotified(ctx, ids, "")
	}
	if err := s.send(ctx, st, notify.Message{Title: title, Body: strings.TrimSpace(b.String()), Priority: "info", Tags: []string{"eyes"}, Link: s.link("/changes")}); err != nil {
		return 0, err
	}
	return len(pending), s.store.SetNotified(ctx, ids, "sent")
}

// ---- preview (adding a watch) ----

type previewResult struct {
	URL          string       `json:"url"`
	FinalURL     string       `json:"final_url"`
	Title        string       `json:"title"`
	Kind         string       `json:"kind"`
	Via          string       `json:"via"`
	MS           int          `json:"ms"`
	Status       int          `json:"status"`
	Text         string       `json:"text"`
	Lines        int          `json:"lines"`
	Blocks       []page.Block `json:"blocks"`
	HasPrice     bool         `json:"has_price"`
	Price        string       `json:"price,omitempty"`
	NeedsBrowser bool         `json:"needs_browser"`
	Browser      bool         `json:"browser"` // a browser is set up
	Error        string       `json:"error,omitempty"`
	Shot         string       `json:"shot,omitempty"` // data URL (browser only)
}

func (s *Server) preview(ctx context.Context, url, selector, exclude, headers, mode, find string, wait int, shot bool) previewResult {
	out := previewResult{URL: url, Blocks: []page.Block{}, Browser: s.fetch.Browser.Configured()}
	rr, err := s.read(ctx, url, selector, exclude, headers, mode, false, wait, shot)
	if rr != nil {
		out.Via, out.MS, out.NeedsBrowser = rr.via, rr.ms, rr.needsBrowser
		if rr.res != nil {
			out.FinalURL, out.Status = rr.res.FinalURL, rr.res.Status
			out.Kind = page.Kind(rr.res.ContentType, rr.res.Body)
			if out.Kind == "html" && len(rr.res.Body) > 0 {
				if bl, err := page.Blocks(rr.res.Body, find); err == nil {
					out.Blocks = bl
				}
				if p := product.Parse(string(rr.res.Body), rr.res.FinalURL); p != nil && p.Price != "" {
					out.HasPrice, out.Price = true, strings.TrimSpace(p.Price+" "+p.Currency)
				}
			}
			if len(rr.res.Shot) > 0 && len(rr.res.Shot) < 3<<20 {
				out.Shot = "data:image/jpeg;base64," + b64(rr.res.Shot)
			}
		}
		if rr.ext != nil {
			out.Title = rr.ext.Title
			ls := strings.Split(rr.ext.Text, "\n")
			out.Lines = len(ls)
			if len(ls) > 400 {
				ls = ls[:400]
			}
			out.Text = strings.Join(ls, "\n")
		}
	}
	if err != nil {
		out.Error = err.Error()
	}
	return out
}

// shotDir is where a watch's pictures live.
func (s *Server) shotDir(id int64) string {
	return filepath.Join(s.cfg.FilesDir, "shots", fmt.Sprint(id))
}
