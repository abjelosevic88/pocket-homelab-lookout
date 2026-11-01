package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-lookout/internal/ai"
	"github.com/abjelosevic88/pocket-homelab-lookout/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-lookout/internal/store"
)

// ---- settings ----

func mask(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "••••"
	}
	return s[:4] + "••••" + s[len(s)-4:]
}

// feedURL is the person's RSS address (made on first use).
func (s *Server) feedURL(ctx context.Context, uid int64, st *store.Settings) string {
	if st.FeedToken == "" {
		st.FeedToken = store.RandomToken(24)
		_ = s.store.SaveUserSettings(ctx, uid, *st)
	}
	base := s.cfg.BaseURL
	return base + "/feed/" + st.FeedToken + ".xml"
}

// publicSettings is what one person sees: their settings with keys masked.
func (s *Server) publicSettings(ctx context.Context, u *store.User, st store.Settings) map[string]any {
	feed := ""
	if u != nil {
		feed = s.feedURL(ctx, u.ID, &st)
	}
	return map[string]any{
		"core_configured": s.core.Configured(), "core_registered": s.coreStatus()["registered"],
		"title": st.Title, "date_format": st.DateFormat, "time_format": st.TimeFormat,
		"default_interval": st.DefaultInterval, "default_noise": st.DefaultNoise, "default_notify_mode": st.DefaultNotifyMode,
		"default_fetch_mode": st.DefaultFetchMode, "keep_snapshots": st.KeepSnapshots, "keep_days": st.KeepDays,
		"notify_url": st.NotifyURL, "notify_format": st.NotifyFormat, "notify_configured": st.NotifyURL != "",
		"notify_errors": st.NotifyErrors, "errors_after": st.ErrorsAfter, "digest_hour": st.DigestHour,
		"ai_summaries": st.AISummaries, "ai_provider": st.AIProvider, "ai_pocket_model": st.AIPocketModel,
		"openai_api_key": mask(st.OpenAIKey), "openai_model": st.OpenAIModel, "openai_base_url": st.OpenAIBaseURL,
		"feed_url": feed, "browser": s.fetch.Browser.Configured(), "min_interval": s.minInterval(),
		"fetch_private": s.cfg.FetchPrivate, "setup_complete": st.SetupComplete, "base_url": s.cfg.BaseURL,
	}
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.GetSettings(r.Context(), actorID(r.Context()))
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	writeJSON(w, http.StatusOK, s.publicSettings(r.Context(), userFrom(r.Context()), st))
}

// handlePutSettings saves the caller's personal settings. The box-wide title
// is only taken from an admin and silently ignored for everyone else.
func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	me := userFrom(r.Context())
	isAdmin := me.Role == "admin"
	cur, err := s.store.GetSettings(r.Context(), me.ID)
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	var in store.Settings
	keys, err := decodeKeys(r, &in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if keys["title"] && isAdmin {
		cur.Title = strings.TrimSpace(in.Title)
		if cur.Title == "" {
			cur.Title = store.DefaultTitle
		}
	}
	text := func(k string, dst *string, v string) {
		if keys[k] {
			*dst = strings.TrimSpace(v)
		}
	}
	intIn := func(k string, dst *int, v, lo, hi int) {
		if keys[k] && v >= lo && v <= hi {
			*dst = v
		}
	}
	boolIn := func(k string, dst *bool, v bool) {
		if keys[k] {
			*dst = v
		}
	}
	oneOf := func(k string, dst *string, v string, allowed ...string) {
		if !keys[k] {
			return
		}
		for _, a := range allowed {
			if v == a {
				*dst = v
			}
		}
	}
	secret := func(k string, dst *string, v string) {
		if keys[k] && !strings.Contains(v, "••") {
			*dst = strings.TrimSpace(v)
		}
	}
	text("date_format", &cur.DateFormat, in.DateFormat)
	text("time_format", &cur.TimeFormat, in.TimeFormat)
	intIn("default_interval", &cur.DefaultInterval, in.DefaultInterval, s.minInterval(), 60*24*31)
	boolIn("default_noise", &cur.DefaultNoise, in.DefaultNoise)
	oneOf("default_notify_mode", &cur.DefaultNotifyMode, in.DefaultNotifyMode, store.NotifyNow, store.NotifyDigest, store.NotifyOff)
	oneOf("default_fetch_mode", &cur.DefaultFetchMode, in.DefaultFetchMode, store.FetchAuto, store.FetchFast, store.FetchBrowser)
	intIn("keep_snapshots", &cur.KeepSnapshots, in.KeepSnapshots, 2, 1000)
	intIn("keep_days", &cur.KeepDays, in.KeepDays, 0, 3650)
	text("notify_url", &cur.NotifyURL, in.NotifyURL)
	if keys["notify_format"] && in.NotifyFormat != "" {
		cur.NotifyFormat = in.NotifyFormat
	}
	boolIn("notify_errors", &cur.NotifyErrors, in.NotifyErrors)
	intIn("errors_after", &cur.ErrorsAfter, in.ErrorsAfter, 1, 50)
	intIn("digest_hour", &cur.DigestHour, in.DigestHour, 0, 23)
	boolIn("ai_summaries", &cur.AISummaries, in.AISummaries)
	if keys["ai_provider"] {
		if !validAIProvider(in.AIProvider) {
			writeErr(w, http.StatusBadRequest, "AI is automatic, Pocket AI or OpenAI")
			return
		}
		cur.AIProvider = in.AIProvider
	}
	if keys["ai_pocket_model"] {
		if !validPocketModel(in.AIPocketModel) {
			writeErr(w, http.StatusBadRequest, "unknown model")
			return
		}
		cur.AIPocketModel = in.AIPocketModel
	}
	secret("openai_api_key", &cur.OpenAIKey, in.OpenAIKey)
	text("openai_model", &cur.OpenAIModel, in.OpenAIModel)
	if keys["openai_base_url"] {
		cur.OpenAIBaseURL = strings.TrimRight(strings.TrimSpace(in.OpenAIBaseURL), "/")
	}
	if err := s.store.SaveUserSettings(r.Context(), me.ID, cur); err != nil {
		s.fail(w, err, "save settings")
		return
	}
	if isAdmin && keys["title"] {
		if err := s.store.SaveBoxSettings(r.Context(), cur); err != nil {
			s.fail(w, err, "save settings")
			return
		}
	}
	writeJSON(w, http.StatusOK, s.publicSettings(r.Context(), me, cur))
}

// handleRotateFeed gives the person a new RSS address (the old one stops working).
func (s *Server) handleRotateFeed(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	me := userFrom(ctx)
	st, _ := s.store.GetSettings(ctx, me.ID)
	st.FeedToken = ""
	writeJSON(w, http.StatusOK, map[string]string{"feed_url": s.feedURL(ctx, me.ID, &st)})
}

// handleIntegrationTest checks a connection with the caller's own saved
// settings (or the URL/key typed into the form).
func (s *Server) handleIntegrationTest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Which  string `json:"which"`
		URL    string `json:"url"`
		Key    string `json:"key"`
		Format string `json:"format"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	st, _ := s.store.GetSettings(r.Context(), actorID(r.Context()))
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	keyOr := func(saved string) string {
		if strings.Contains(in.Key, "••") || in.Key == "" {
			return saved
		}
		return in.Key
	}
	urlOr := func(saved string) string {
		if strings.TrimSpace(in.URL) == "" {
			return saved
		}
		return in.URL
	}
	var msg string
	var err error
	switch in.Which {
	case "openai":
		c := ai.New(urlOr(st.OpenAIBaseURL), keyOr(st.OpenAIKey), st.OpenAIModel, 20*time.Second)
		var ids []string
		if ids, err = c.Models(ctx); err == nil {
			msg = fmt.Sprintf("The key works (%d models)", len(ids))
		}
	case "notify":
		f := in.Format
		if f == "" {
			f = st.NotifyFormat
		}
		n := notify.New(urlOr(st.NotifyURL), f)
		err = n.Send(ctx, notify.Message{Title: "Pocket Lookout", Body: "Notifications work. Changes on the pages you watch will arrive here.", Priority: "info", Tags: []string{"eyes"}})
		msg = "Test message sent via " + n.Format
	case "browser":
		if !s.fetch.Browser.Configured() {
			err = fmt.Errorf("no browser is set up on this server (BROWSER_URL)")
			break
		}
		start := time.Now()
		r2, e := s.fetch.Browser.Get(ctx, "https://example.com/", nil, 0, false)
		if e != nil {
			err = e
			break
		}
		msg = fmt.Sprintf("The browser works (example.com in %d ms, %d bytes)", time.Since(start).Milliseconds(), len(r2.Body))
	default:
		writeErr(w, http.StatusBadRequest, "unknown integration")
		return
	}
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": msg})
}

// handleDigestNow sends the waiting daily summary right away.
func (s *Server) handleDigestNow(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, _ := s.store.GetSettings(ctx, actorID(ctx))
	if st.NotifyURL == "" {
		writeErr(w, http.StatusBadRequest, "notifications are not set up")
		return
	}
	n, err := s.sendDigest(ctx, actorID(ctx), st)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "sent": n})
}

// ---- system / backup ----

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	var size, files int64
	if fi, err := os.Stat(s.cfg.DBPath); err == nil {
		size = fi.Size()
	}
	_ = filepath.WalkDir(s.cfg.FilesDir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				files += fi.Size()
			}
		}
		return nil
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"version": Version, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH,
		"core":      s.coreStatus(),
		"auth_mode": s.cfg.AuthMode, "data_dir": s.cfg.DataDir, "db_path": s.cfg.DBPath, "db_bytes": size, "files_bytes": files,
		"uptime": time.Since(s.started).Round(time.Second).String(), "timezone": time.Now().Location().String(),
		"counts": s.store.CountsFor(r.Context(), actorID(r.Context())), "checks_run": checksRun.Load(), "checks_failed": checksFailed.Load(),
		"changes_found": changesFound.Load(), "browser_checks": browserChecks.Load(), "browser": s.fetch.Browser.Configured(),
		"workers": s.cfg.CheckWorkers, "min_interval": s.minInterval(), "fetch_private": s.cfg.FetchPrivate,
	})
}

// handleBackup downloads the whole database, so it is a box operation for an
// admin only (everyone can export their own watches as JSON).
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	dir := filepath.Join(s.cfg.DataDir, "backups")
	name := fmt.Sprintf("pocket-lookout-%s.db", time.Now().Format("20060102-150405"))
	dst := filepath.Join(dir, name)
	if err := s.store.Backup(r.Context(), dst); err != nil {
		s.fail(w, err, "backup")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	http.ServeFile(w, r, dst)
}
