package server

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-lookout/internal/page"
	"github.com/abjelosevic88/pocket-homelab-lookout/internal/store"
)

// watchInput is what a person (or a linked app) may set on a watch. Only the
// keys sent are applied.
type watchInput struct {
	URL             string `json:"url"`
	Title           string `json:"title"`
	Tags            string `json:"tags"`
	Notes           string `json:"notes"`
	Selector        string `json:"selector"`
	Exclude         string `json:"exclude"`
	FetchMode       string `json:"fetch_mode"`
	BrowserWait     int    `json:"browser_wait"`
	Headers         string `json:"headers"`
	Screenshot      bool   `json:"screenshot"`
	NoiseFilter     bool   `json:"noise_filter"`
	IgnoreText      string `json:"ignore_text"`
	NotifyOn        string `json:"notify_on"`
	TriggerText     string `json:"trigger_text"`
	AICondition     string `json:"ai_condition"`
	NotifyMode      string `json:"notify_mode"`
	IntervalMinutes int    `json:"interval_minutes"`
	Paused          bool   `json:"paused"`
}

func (in watchInput) apply(w *store.Watch, keys map[string]bool) {
	set := func(k string, dst *string, v string) {
		if keys[k] {
			*dst = v
		}
	}
	set("url", &w.URL, strings.TrimSpace(in.URL))
	set("title", &w.Title, strings.TrimSpace(in.Title))
	set("tags", &w.Tags, in.Tags)
	set("notes", &w.Notes, strings.TrimSpace(in.Notes))
	set("selector", &w.Selector, strings.Join(store.Lines(in.Selector), "\n"))
	set("exclude", &w.Exclude, strings.Join(store.Lines(in.Exclude), "\n"))
	set("fetch_mode", &w.FetchMode, in.FetchMode)
	set("headers", &w.Headers, strings.TrimSpace(in.Headers))
	set("ignore_text", &w.IgnoreText, strings.Join(store.Lines(in.IgnoreText), "\n"))
	set("notify_on", &w.NotifyOn, in.NotifyOn)
	set("trigger_text", &w.TriggerText, strings.Join(store.Lines(in.TriggerText), "\n"))
	set("ai_condition", &w.AICondition, strings.TrimSpace(in.AICondition))
	set("notify_mode", &w.NotifyMode, in.NotifyMode)
	if keys["browser_wait"] {
		w.BrowserWait = in.BrowserWait
	}
	if keys["screenshot"] {
		w.Screenshot = in.Screenshot
	}
	if keys["noise_filter"] {
		w.NoiseFilter = in.NoiseFilter
	}
	if keys["interval_minutes"] {
		w.IntervalMinutes = in.IntervalMinutes
	}
	if keys["paused"] {
		w.Paused = in.Paused
	}
}

// validate says what is wrong with a watch ("" = fine).
func validateWatch(w *store.Watch) string {
	if _, err := page.CheckURL(w.URL); err != nil {
		return err.Error()
	}
	for _, r := range append(store.Lines(w.IgnoreText), store.Lines(w.TriggerText)...) {
		if msg := page.ValidRule(r); msg != "" {
			return msg
		}
	}
	if len(w.AICondition) > 1000 {
		return "the AI condition is too long (1000 characters at most)"
	}
	return ""
}

func (s *Server) minInterval() int { return int(s.cfg.MinInterval / time.Minute) }

func (s *Server) newWatch(st store.Settings) *store.Watch {
	return &store.Watch{FetchMode: st.DefaultFetchMode, NoiseFilter: st.DefaultNoise, NotifyMode: st.DefaultNotifyMode,
		NotifyOn: "any", IntervalMinutes: st.DefaultInterval, BrowserWait: 2}
}

func (s *Server) watchEvent(ctx context.Context, topic string, w *store.Watch) core.CommitHook {
	if !s.core.Configured() {
		return nil
	}
	email := ""
	if u := userFrom(ctx); u != nil && u.ID == w.UserID {
		email = u.Email
	} else if owner, err := s.store.GetUser(ctx, w.UserID); err == nil {
		email = owner.Email
	}
	payload := map[string]any{"user_email": email, "watch_id": w.ID, "title": w.Name, "url": w.URL, "tags": store.SplitTags(w.Tags), "source": w.Source}
	return func(tx *sql.Tx) error {
		return core.QueueEvent(ctx, tx, topic, fmt.Sprintf("watch:%d", w.ID), payload)
	}
}

func (s *Server) handleListWatches(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ws, err := s.store.ListWatches(r.Context(), actorID(r.Context()), store.WatchFilter{Tag: q.Get("tag"), Q: q.Get("q"), State: q.Get("state"), Source: q.Get("source")})
	if err != nil {
		s.fail(w, err, "watches")
		return
	}
	for i := range ws {
		ws[i].Status = s.liveStatus(&ws[i])
	}
	writeJSON(w, http.StatusOK, ws)
}

// liveStatus is "checking" while a check runs.
func (s *Server) liveStatus(w *store.Watch) string {
	if s.isRunning(w.ID) {
		return "checking"
	}
	return w.Status
}

func (s *Server) handleCreateWatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	var in watchInput
	keys, err := decodeKeys(r, &in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	st, _ := s.store.GetSettings(ctx, uid)
	wa := s.newWatch(st)
	in.apply(wa, keys)
	wa.Source = linkApp(ctx)
	wa.Normalize(s.minInterval())
	if msg := validateWatch(wa); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.store.CreateWatch(ctx, uid, wa, s.watchEvent(ctx, "lookout.watch_added", wa)); err != nil {
		s.fail(w, err, "create watch")
		return
	}
	s.nudge()
	writeJSON(w, http.StatusCreated, wa)
}

func (s *Server) handleGetWatch(w http.ResponseWriter, r *http.Request) {
	wa, err := s.store.GetWatch(r.Context(), actorID(r.Context()), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "watch")
		return
	}
	wa.Status = s.liveStatus(wa)
	writeJSON(w, http.StatusOK, wa)
}

func (s *Server) handleUpdateWatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	wa, err := s.store.GetWatch(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "watch")
		return
	}
	if !mayChange(ctx, wa) {
		writeErr(w, http.StatusForbidden, "this app may only change the watches it added")
		return
	}
	var in watchInput
	keys, err := decodeKeys(r, &in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	in.apply(wa, keys)
	wa.Normalize(s.minInterval())
	if msg := validateWatch(wa); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.store.UpdateWatch(ctx, uid, wa); err != nil {
		s.fail(w, err, "update watch")
		return
	}
	if keys["paused"] {
		_ = s.store.SetPaused(ctx, uid, wa.ID, wa.Paused)
	}
	s.nudge()
	wa, _ = s.store.GetWatch(ctx, uid, wa.ID)
	writeJSON(w, http.StatusOK, wa)
}

func (s *Server) handleDeleteWatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	wa, err := s.store.GetWatch(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "watch")
		return
	}
	if !mayChange(ctx, wa) {
		writeErr(w, http.StatusForbidden, "this app may only delete the watches it added")
		return
	}
	if _, err := s.store.DeleteWatch(ctx, uid, wa.ID); err != nil {
		s.fail(w, err, "delete watch")
		return
	}
	_ = os.RemoveAll(s.shotDir(wa.ID))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDuplicateWatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	wa, err := s.store.GetWatch(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "watch")
		return
	}
	cp := *wa
	cp.ID, cp.LastSnapshotID, cp.NextCheckAt, cp.Source = 0, nil, "", linkApp(ctx)
	cp.Title = wa.Name + " (copy)"
	cp.Paused = true // so the copy can be changed before it runs
	if err := s.store.CreateWatch(ctx, uid, &cp); err != nil {
		s.fail(w, err, "duplicate watch")
		return
	}
	_ = s.store.SetPaused(ctx, uid, cp.ID, true)
	out, _ := s.store.GetWatch(ctx, uid, cp.ID)
	writeJSON(w, http.StatusCreated, out)
}

// handleCheckNow checks a watch right away and answers with what it found.
func (s *Server) handleCheckNow(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	wa, err := s.store.GetWatch(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "watch")
		return
	}
	if !mayChange(ctx, wa) {
		writeErr(w, http.StatusForbidden, "this app may only check the watches it added")
		return
	}
	// a check already running (the scheduler's) is waited for, then this one runs
	deadline := time.Now().Add(s.cfg.BrowserTimeout + 60*time.Second)
	for !s.claim(wa.ID) {
		if time.Now().After(deadline) {
			writeErr(w, http.StatusConflict, "this page is being checked right now")
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(200 * time.Millisecond):
		}
	}
	defer s.release(wa.ID)
	if fresh, err := s.store.GetWatch(ctx, uid, wa.ID); err == nil {
		wa = fresh
	}
	out := s.checkWatch(context.WithoutCancel(ctx), wa)
	fresh, _ := s.store.GetWatch(ctx, uid, wa.ID)
	writeJSON(w, http.StatusOK, map[string]any{"outcome": out, "watch": fresh})
}

func (s *Server) handlePause(paused bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		uid := actorID(ctx)
		wa, err := s.store.GetWatch(ctx, uid, idParam(r, "id"))
		if err != nil {
			s.fail(w, err, "watch")
			return
		}
		if !mayChange(ctx, wa) {
			writeErr(w, http.StatusForbidden, "this app may only change the watches it added")
			return
		}
		if err := s.store.SetPaused(ctx, uid, wa.ID, paused); err != nil {
			s.fail(w, err, "pause")
			return
		}
		if !paused {
			s.nudge()
		}
		wa, _ = s.store.GetWatch(ctx, uid, wa.ID)
		writeJSON(w, http.StatusOK, wa)
	}
}

// handleBulk runs one action on many watches: check, pause, resume, delete, tag.
func (s *Server) handleBulk(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	var in struct {
		IDs    []int64 `json:"ids"`
		Action string  `json:"action"`
		Tag    string  `json:"tag"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	n := 0
	if in.Action == "check" && len(in.IDs) == 0 {
		n, _ = s.store.CheckSoon(ctx, uid, nil)
		s.nudge()
		writeJSON(w, http.StatusOK, map[string]any{"count": n})
		return
	}
	for _, id := range in.IDs {
		wa, err := s.store.GetWatch(ctx, uid, id)
		if err != nil || !mayChange(ctx, wa) {
			continue
		}
		switch in.Action {
		case "check":
			if k, _ := s.store.CheckSoon(ctx, uid, []int64{id}); k > 0 {
				n++
			}
		case "pause", "resume":
			if s.store.SetPaused(ctx, uid, id, in.Action == "pause") == nil {
				n++
			}
		case "delete":
			if _, err := s.store.DeleteWatch(ctx, uid, id); err == nil {
				_ = os.RemoveAll(s.shotDir(id))
				n++
			}
		case "tag", "untag":
			tags := store.SplitTags(wa.Tags)
			t := strings.ToLower(strings.TrimSpace(in.Tag))
			if t == "" {
				continue
			}
			var next []string
			for _, x := range tags {
				if x != t {
					next = append(next, x)
				}
			}
			if in.Action == "tag" {
				next = append(next, t)
			}
			wa.Tags = strings.Join(next, ", ")
			if s.store.UpdateWatch(ctx, uid, wa) == nil {
				n++
			}
		default:
			writeErr(w, http.StatusBadRequest, "unknown action")
			return
		}
	}
	s.nudge()
	writeJSON(w, http.StatusOK, map[string]any{"count": n})
}

func (s *Server) handleWatchChanges(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	wa, err := s.store.GetWatch(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "watch")
		return
	}
	cs, err := s.store.ListChanges(ctx, uid, store.ChangeFilter{WatchID: wa.ID, All: queryBool(r, "all"), Before: int64(queryInt(r, "before")), Limit: queryInt(r, "limit")})
	if err != nil {
		s.fail(w, err, "changes")
		return
	}
	writeJSON(w, http.StatusOK, cs)
}

func (s *Server) handleWatchChecks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	wa, err := s.store.GetWatch(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "watch")
		return
	}
	cs, err := s.store.Checks(ctx, uid, wa.ID, 100)
	if err != nil {
		s.fail(w, err, "checks")
		return
	}
	writeJSON(w, http.StatusOK, cs)
}

// handleWatchSnapshot is what the page says now (the latest snapshot).
func (s *Server) handleWatchSnapshot(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	wa, err := s.store.GetWatch(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "watch")
		return
	}
	if wa.LastSnapshotID == nil {
		writeJSON(w, http.StatusOK, map[string]any{"snapshot": nil, "history": []store.Snapshot{}})
		return
	}
	sn, err := s.store.GetSnapshot(ctx, uid, *wa.LastSnapshotID)
	if err != nil {
		s.fail(w, err, "snapshot")
		return
	}
	hist, _ := s.store.Snapshots(ctx, uid, wa.ID, 100)
	writeJSON(w, http.StatusOK, map[string]any{"snapshot": sn, "history": hist})
}

func (s *Server) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	sn, err := s.store.GetSnapshot(r.Context(), actorID(r.Context()), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "snapshot")
		return
	}
	writeJSON(w, http.StatusOK, sn)
}

func (s *Server) handleSnapshotShot(w http.ResponseWriter, r *http.Request) {
	sn, err := s.store.GetSnapshot(r.Context(), actorID(r.Context()), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "snapshot")
		return
	}
	s.serveFile(w, r, sn.ShotPath)
}

func (s *Server) handleIgnoreLines(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	var in struct {
		Lines []string `json:"lines"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := s.store.AddIgnore(ctx, uid, idParam(r, "id"), in.Lines); err != nil {
		s.fail(w, err, "ignore")
		return
	}
	wa, _ := s.store.GetWatch(ctx, uid, idParam(r, "id"))
	writeJSON(w, http.StatusOK, wa)
}

func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL         string `json:"url"`
		Selector    string `json:"selector"`
		Exclude     string `json:"exclude"`
		Headers     string `json:"headers"`
		FetchMode   string `json:"fetch_mode"`
		Find        string `json:"find"`
		BrowserWait int    `json:"browser_wait"`
		Screenshot  bool   `json:"screenshot"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	u := strings.TrimSpace(in.URL)
	if u != "" && !strings.Contains(u, "://") {
		u = "https://" + u
	}
	if _, err := page.CheckURL(u); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	switch in.FetchMode {
	case store.FetchAuto, store.FetchFast, store.FetchBrowser:
	default:
		in.FetchMode = store.FetchAuto
	}
	if in.BrowserWait <= 0 || in.BrowserWait > 30 {
		in.BrowserWait = 2
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.BrowserTimeout+45*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, s.preview(ctx, u, in.Selector, in.Exclude, in.Headers, in.FetchMode, in.Find, in.BrowserWait, in.Screenshot))
}

// ---- changes ----

func (s *Server) handleListChanges(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	cs, err := s.store.ListChanges(r.Context(), actorID(r.Context()), store.ChangeFilter{Unseen: queryBool(r, "unseen"), All: queryBool(r, "all"),
		Before: int64(queryInt(r, "before")), Tag: q.Get("tag"), WatchID: int64(queryInt(r, "watch")), Limit: queryInt(r, "limit")})
	if err != nil {
		s.fail(w, err, "changes")
		return
	}
	writeJSON(w, http.StatusOK, cs)
}

func (s *Server) handleGetChange(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	c, err := s.store.GetChange(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "change")
		return
	}
	prev, next := s.store.Neighbours(ctx, uid, c)
	wa, _ := s.store.GetWatch(ctx, uid, c.WatchID)
	writeJSON(w, http.StatusOK, map[string]any{"change": c, "prev": prev, "next": next, "watch": wa})
}

func (s *Server) handleSeen(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		WatchID  int64 `json:"watch_id"`
		ChangeID int64 `json:"change_id"`
	}
	_ = decode(r, &in)
	if id := idParam(r, "id"); id > 0 {
		in.ChangeID = id
	}
	n, err := s.store.MarkSeen(ctx, actorID(ctx), in.WatchID, in.ChangeID)
	if err != nil {
		s.fail(w, err, "seen")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": n})
}

// handleDismiss marks a change "not important"; with ignore it also adds
// its changed lines to the watch's ignore list so they never count again.
func (s *Server) handleDismiss(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	var in struct {
		Ignore bool `json:"ignore"`
		Undo   bool `json:"undo"`
	}
	_ = decode(r, &in)
	c, err := s.store.GetChange(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "change")
		return
	}
	if err := s.store.Dismiss(ctx, uid, c.ID, !in.Undo); err != nil {
		s.fail(w, err, "dismiss")
		return
	}
	if in.Ignore && !in.Undo {
		lines := append(changedLines(c.Diff, "+"), changedLines(c.Diff, "-")...)
		if len(lines) > 50 {
			lines = lines[:50]
		}
		if err := s.store.AddIgnore(ctx, uid, c.WatchID, lines); err != nil {
			s.fail(w, err, "ignore")
			return
		}
	}
	c, _ = s.store.GetChange(ctx, uid, c.ID)
	writeJSON(w, http.StatusOK, c)
}

// ---- tags, dashboard, day ----

func (s *Server) handleTags(w http.ResponseWriter, r *http.Request) {
	tags, err := s.store.Tags(r.Context(), actorID(r.Context()))
	if err != nil {
		s.fail(w, err, "tags")
		return
	}
	writeJSON(w, http.StatusOK, store.SortTags(tags))
}

// handleDashboard is a small summary (Homepage widget, Pocket Hub).
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	stats := s.store.Stats(ctx, uid)
	days, _ := s.store.ChangeCountsByDay(ctx, uid, 30)
	latest, _ := s.store.ListChanges(ctx, uid, store.ChangeFilter{Limit: 5})
	writeJSON(w, http.StatusOK, map[string]any{
		"watches": stats["watches"], "active": stats["active"], "paused": stats["paused"], "failing": stats["failing"],
		"unseen": stats["unseen"], "changed_watches": stats["changed_watches"], "changes_today": stats["changes_today"],
		"changes_week": stats["changes_week"], "per_day": days, "latest": latest, "browser": s.fetch.Browser.Configured(),
	})
}

// handleDay is one person's day for Pocket Day: the changes that counted.
func (s *Server) handleDay(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	day := chi.URLParam(r, "date")
	t, err := time.ParseInLocation("2006-01-02", day, time.Local)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "date must be YYYY-MM-DD")
		return
	}
	cs, err := s.store.ListChanges(ctx, actorID(ctx), store.ChangeFilter{From: t.UTC().Format(time.RFC3339Nano), To: t.AddDate(0, 0, 1).UTC().Format(time.RFC3339Nano), Limit: 200})
	if err != nil {
		s.fail(w, err, "day")
		return
	}
	type item struct {
		ID      int64  `json:"id"`
		At      string `json:"at"`
		Watch   string `json:"watch"`
		WatchID int64  `json:"watch_id"`
		URL     string `json:"url"`
		Summary string `json:"summary"`
		Link    string `json:"link"`
	}
	out := []item{}
	for _, c := range cs {
		out = append(out, item{c.ID, c.At, c.WatchName, c.WatchID, c.WatchURL, c.Summary, s.link(fmt.Sprintf("/changes/%d", c.ID))})
	}
	writeJSON(w, http.StatusOK, map[string]any{"date": day, "changes": out})
}
