package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"sort"
	"strings"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

const watchCols = `id, user_id, url, title, page_title, tags, notes, selector, exclude, fetch_mode, needs_browser, browser_wait,
	headers, screenshot, noise_filter, ignore_text, notify_on, trigger_text, ai_condition, notify_mode, interval_minutes, paused,
	source, status, last_checked_at, last_changed_at, next_check_at, last_error, error_count, failing_alerted, last_snapshot_id,
	unseen, created_at, updated_at`

func scanWatch(row interface{ Scan(...any) error }) (*Watch, error) {
	var w Watch
	var nb, shot, noise, paused, fa int
	var snap sql.NullInt64
	err := row.Scan(&w.ID, &w.UserID, &w.URL, &w.Title, &w.PageTitle, &w.Tags, &w.Notes, &w.Selector, &w.Exclude, &w.FetchMode,
		&nb, &w.BrowserWait, &w.Headers, &shot, &noise, &w.IgnoreText, &w.NotifyOn, &w.TriggerText, &w.AICondition, &w.NotifyMode,
		&w.IntervalMinutes, &paused, &w.Source, &w.Status, &w.LastCheckedAt, &w.LastChangedAt, &w.NextCheckAt, &w.LastError,
		&w.ErrorCount, &fa, &snap, &w.Unseen, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	w.NeedsBrowser, w.Screenshot, w.NoiseFilter, w.Paused, w.FailingAlerted = nb == 1, shot == 1, noise == 1, paused == 1, fa == 1
	if snap.Valid {
		w.LastSnapshotID = &snap.Int64
	}
	w.Fill()
	return &w, nil
}

// Host is the host of a URL without "www.".
func Host(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}

// Fill sets the computed name and host.
func (w *Watch) Fill() {
	w.Host = Host(w.URL)
	w.Name = w.Title
	if w.Name == "" {
		w.Name = w.PageTitle
	}
	if w.Name == "" {
		w.Name = w.Host
	}
}

// Normalize puts a watch's settings into their allowed ranges.
func (w *Watch) Normalize(minInterval int) {
	w.URL = strings.TrimSpace(w.URL)
	w.Title = strings.TrimSpace(w.Title)
	w.Tags = JoinTags(w.Tags)
	switch w.FetchMode {
	case FetchAuto, FetchFast, FetchBrowser:
	default:
		w.FetchMode = FetchAuto
	}
	switch w.NotifyMode {
	case NotifyNow, NotifyDigest, NotifyOff:
	default:
		w.NotifyMode = NotifyNow
	}
	switch w.NotifyOn {
	case "any", "added", "removed":
	default:
		w.NotifyOn = "any"
	}
	if w.IntervalMinutes < minInterval {
		w.IntervalMinutes = minInterval
	}
	if w.IntervalMinutes > 60*24*31 {
		w.IntervalMinutes = 60 * 24 * 31
	}
	if w.BrowserWait < 0 {
		w.BrowserWait = 0
	}
	if w.BrowserWait > 30 {
		w.BrowserWait = 30
	}
	if w.Status == "" {
		w.Status = StatusNew
	}
}

// CreateWatch adds a watch, due right away.
func (s *Store) CreateWatch(ctx context.Context, uid int64, w *Watch, after ...core.CommitHook) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := Now()
	if w.NextCheckAt == "" {
		w.NextCheckAt = now
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO watches (user_id, url, title, page_title, tags, notes, selector, exclude, fetch_mode,
		browser_wait, headers, screenshot, noise_filter, ignore_text, notify_on, trigger_text, ai_condition, notify_mode, interval_minutes,
		paused, source, status, next_check_at, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		uid, w.URL, w.Title, w.PageTitle, w.Tags, w.Notes, w.Selector, w.Exclude, w.FetchMode, w.BrowserWait, w.Headers, boolInt(w.Screenshot),
		boolInt(w.NoiseFilter), w.IgnoreText, w.NotifyOn, w.TriggerText, w.AICondition, w.NotifyMode, w.IntervalMinutes, boolInt(w.Paused),
		w.Source, StatusNew, w.NextCheckAt, now, now)
	if err != nil {
		return err
	}
	w.ID, _ = res.LastInsertId()
	w.UserID, w.Status, w.CreatedAt, w.UpdatedAt = uid, StatusNew, now, now
	w.Fill()
	return core.Commit(tx, after...)
}

// UpdateWatch saves what the person can change. A changed address or part of
// the page starts the history over at the next check (the old changes stay).
func (s *Store) UpdateWatch(ctx context.Context, uid int64, w *Watch, after ...core.CommitHook) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var oldURL, oldSel, oldEx string
	if err := tx.QueryRowContext(ctx, `SELECT url, selector, exclude FROM watches WHERE id = ? AND user_id = ?`, w.ID, uid).Scan(&oldURL, &oldSel, &oldEx); err != nil {
		return notFound(err)
	}
	reset := oldURL != w.URL || oldSel != w.Selector || oldEx != w.Exclude
	q := `UPDATE watches SET url = ?, title = ?, tags = ?, notes = ?, selector = ?, exclude = ?, fetch_mode = ?, browser_wait = ?, headers = ?,
		screenshot = ?, noise_filter = ?, ignore_text = ?, notify_on = ?, trigger_text = ?, ai_condition = ?, notify_mode = ?, interval_minutes = ?,
		paused = ?, updated_at = ?`
	args := []any{w.URL, w.Title, w.Tags, w.Notes, w.Selector, w.Exclude, w.FetchMode, w.BrowserWait, w.Headers, boolInt(w.Screenshot),
		boolInt(w.NoiseFilter), w.IgnoreText, w.NotifyOn, w.TriggerText, w.AICondition, w.NotifyMode, w.IntervalMinutes, boolInt(w.Paused), Now()}
	if reset {
		// read the new page right away; the next snapshot becomes the new baseline
		q += `, last_snapshot_id = NULL, needs_browser = 0, next_check_at = ?, status = CASE WHEN status = 'error' THEN 'new' ELSE status END`
		args = append(args, Now())
	}
	q += ` WHERE id = ? AND user_id = ?`
	args = append(args, w.ID, uid)
	if _, err := tx.ExecContext(ctx, q, args...); err != nil {
		return err
	}
	return core.Commit(tx, after...)
}

// GetWatch returns one of the person's watches.
func (s *Store) GetWatch(ctx context.Context, uid, id int64) (*Watch, error) {
	w, err := scanWatch(s.DB.QueryRowContext(ctx, `SELECT `+watchCols+` FROM watches WHERE id = ? AND user_id = ?`, id, uid))
	if err != nil {
		return nil, err
	}
	s.decorate(ctx, uid, []*Watch{w})
	return w, nil
}

// DeleteWatch removes a watch with its history and returns its pictures.
func (s *Store) DeleteWatch(ctx context.Context, uid, id int64, after ...core.CommitHook) ([]string, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT shot_path FROM snapshots WHERE watch_id = ? AND user_id = ? AND shot_path <> ''`, id, uid)
	if err != nil {
		return nil, err
	}
	var shots []string
	for rows.Next() {
		var p string
		if rows.Scan(&p) == nil {
			shots = append(shots, p)
		}
	}
	rows.Close()
	res, err := tx.ExecContext(ctx, `DELETE FROM watches WHERE id = ? AND user_id = ?`, id, uid)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return shots, core.Commit(tx, after...)
}

// WatchFilter narrows the list of watches.
type WatchFilter struct {
	Tag    string
	Q      string
	State  string // "" all | changed (unseen changes) | failing | paused | active
	Source string
}

// ListWatches returns the person's watches: newest change first, then by name.
func (s *Store) ListWatches(ctx context.Context, uid int64, f WatchFilter) ([]Watch, error) {
	q := `SELECT ` + watchCols + ` FROM watches WHERE user_id = ?`
	args := []any{uid}
	switch f.State {
	case "changed":
		q += ` AND unseen > 0`
	case "failing":
		q += ` AND status = 'error' AND paused = 0`
	case "paused":
		q += ` AND paused = 1`
	case "active":
		q += ` AND paused = 0`
	}
	if f.Source != "" {
		q += ` AND source = ?`
		args = append(args, f.Source)
	}
	if f.Q != "" {
		like := "%" + strings.ToLower(f.Q) + "%"
		q += ` AND (lower(title) LIKE ? OR lower(page_title) LIKE ? OR lower(url) LIKE ? OR lower(tags) LIKE ? OR lower(notes) LIKE ?)`
		args = append(args, like, like, like, like, like)
	}
	q += ` ORDER BY (unseen > 0) DESC, CASE WHEN last_changed_at = '' THEN created_at ELSE last_changed_at END DESC, id DESC`
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	var ptrs []*Watch
	for rows.Next() {
		w, err := scanWatch(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		if f.Tag != "" && !hasTag(w.Tags, f.Tag) {
			continue
		}
		ptrs = append(ptrs, w)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	s.decorate(ctx, uid, ptrs)
	out := make([]Watch, 0, len(ptrs))
	for _, w := range ptrs {
		out = append(out, *w)
	}
	return out, nil
}

func hasTag(tags, tag string) bool {
	tag = strings.ToLower(strings.TrimSpace(tag))
	for _, t := range SplitTags(tags) {
		if t == tag {
			return true
		}
	}
	return false
}

// decorate adds the change count, the newest summary and whether the latest
// snapshot has a picture (collected after the watch rows are closed).
func (s *Store) decorate(ctx context.Context, uid int64, ws []*Watch) {
	if len(ws) == 0 {
		return
	}
	type agg struct {
		n       int
		summary string
	}
	byID := map[int64]*agg{}
	rows, err := s.DB.QueryContext(ctx, `SELECT watch_id, COUNT(1),
		(SELECT c2.summary FROM changes c2 WHERE c2.watch_id = c.watch_id AND c2.user_id = c.user_id AND c2.matched = 1 ORDER BY c2.id DESC LIMIT 1)
		FROM changes c WHERE user_id = ? AND matched = 1 GROUP BY watch_id`, uid)
	if err == nil {
		for rows.Next() {
			var id int64
			var a agg
			if rows.Scan(&id, &a.n, &a.summary) == nil {
				byID[id] = &a
			}
		}
		rows.Close()
	}
	shots := map[int64]bool{}
	rows, err = s.DB.QueryContext(ctx, `SELECT w.id FROM watches w JOIN snapshots sn ON sn.id = w.last_snapshot_id WHERE w.user_id = ? AND sn.shot_path <> ''`, uid)
	if err == nil {
		for rows.Next() {
			var id int64
			if rows.Scan(&id) == nil {
				shots[id] = true
			}
		}
		rows.Close()
	}
	for _, w := range ws {
		if a := byID[w.ID]; a != nil {
			w.Changes, w.LastSummary = a.n, a.summary
		}
		w.HasShot = shots[w.ID]
	}
}

// Tags counts the person's tags.
func (s *Store) Tags(ctx context.Context, uid int64) (map[string]int, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT tags FROM watches WHERE user_id = ? AND tags <> ''`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		for _, x := range SplitTags(t) {
			out[x]++
		}
	}
	return out, rows.Err()
}

// DueWatches are the active watches whose next check has come (all people).
func (s *Store) DueWatches(ctx context.Context, limit int) ([]Watch, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+watchCols+` FROM watches WHERE paused = 0 AND next_check_at <> '' AND next_check_at <= ?
		ORDER BY next_check_at LIMIT ?`, Now(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Watch
	for rows.Next() {
		w, err := scanWatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *w)
	}
	return out, rows.Err()
}

// NextDue is when the next watch is due ("" = none).
func (s *Store) NextDue(ctx context.Context) string {
	var at sql.NullString
	_ = s.DB.QueryRowContext(ctx, `SELECT MIN(next_check_at) FROM watches WHERE paused = 0 AND next_check_at <> ''`).Scan(&at)
	return at.String
}

// CheckSoon makes watches due now (ids nil = all the person's active watches).
func (s *Store) CheckSoon(ctx context.Context, uid int64, ids []int64) (int, error) {
	now := Now()
	if ids == nil {
		res, err := s.DB.ExecContext(ctx, `UPDATE watches SET next_check_at = ? WHERE user_id = ? AND paused = 0`, now, uid)
		if err != nil {
			return 0, err
		}
		n, _ := res.RowsAffected()
		return int(n), nil
	}
	n := 0
	for _, id := range ids {
		res, err := s.DB.ExecContext(ctx, `UPDATE watches SET next_check_at = ? WHERE user_id = ? AND id = ?`, now, uid, id)
		if err != nil {
			return n, err
		}
		k, _ := res.RowsAffected()
		n += int(k)
	}
	return n, nil
}

// SetPaused pauses or resumes a watch (resuming checks it right away).
func (s *Store) SetPaused(ctx context.Context, uid, id int64, paused bool) error {
	q := `UPDATE watches SET paused = ?, updated_at = ? WHERE id = ? AND user_id = ?`
	args := []any{boolInt(paused), Now(), id, uid}
	if !paused {
		q = `UPDATE watches SET paused = 0, next_check_at = ?, updated_at = ? WHERE id = ? AND user_id = ?`
		args = []any{Now(), Now(), id, uid}
	}
	res, err := s.DB.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AddIgnore appends lines to a watch's "never counts" list (no duplicates).
func (s *Store) AddIgnore(ctx context.Context, uid, id int64, lines []string) error {
	w, err := s.GetWatch(ctx, uid, id)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	cur := Lines(w.IgnoreText)
	for _, l := range cur {
		have[l] = true
	}
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" && !have[l] {
			have[l] = true
			cur = append(cur, l)
		}
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE watches SET ignore_text = ?, updated_at = ? WHERE id = ? AND user_id = ?`, strings.Join(cur, "\n"), Now(), id, uid)
	return err
}

// CheckRecord is the outcome of one check, written in one transaction.
type CheckRecord struct {
	At        time.Time
	OK        bool
	Via       string
	MS        int
	Bytes     int
	Status    int
	Error     string
	PageTitle string
	// auto found the page needs the browser
	NeedsBrowser bool

	// set when the page says something new
	Body     string
	Hash     string
	ShotPath string

	// set when that is a change worth recording (the first snapshot is not)
	Change *Change

	// failing: the person was told it keeps failing
	FailingAlerted bool
	Interval       time.Duration
	RetryIn        time.Duration // on errors, when to look again
	KeepSnapshots  int
}

// RecordCheck stores a check: the check row, a new snapshot, the change and
// the watch's state. onChange runs inside the transaction once the change has
// its id (for the Core event). It returns pictures of pruned snapshots.
func (s *Store) RecordCheck(ctx context.Context, w *Watch, r CheckRecord, onChange func(tx *sql.Tx, c *Change) error) ([]string, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	at := r.At.UTC().Format(time.RFC3339Nano)
	changed := r.Change != nil
	if _, err := tx.ExecContext(ctx, `INSERT INTO checks (user_id, watch_id, at, ok, changed, via, ms, bytes, status, error) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		w.UserID, w.ID, at, boolInt(r.OK), boolInt(changed), r.Via, r.MS, r.Bytes, r.Status, r.Error); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM checks WHERE watch_id = ? AND id NOT IN (SELECT id FROM checks WHERE watch_id = ? ORDER BY id DESC LIMIT 100)`, w.ID, w.ID); err != nil {
		return nil, err
	}
	var gone []string
	if !r.OK {
		next := r.At.Add(r.RetryIn)
		if _, err := tx.ExecContext(ctx, `UPDATE watches SET status = 'error', last_error = ?, error_count = error_count + 1, last_checked_at = ?,
			next_check_at = ?, failing_alerted = ? WHERE id = ?`, r.Error, at, next.UTC().Format(time.RFC3339Nano), boolInt(r.FailingAlerted), w.ID); err != nil {
			return nil, err
		}
		return nil, tx.Commit()
	}
	snapID := w.LastSnapshotID
	if r.Body != "" || r.Hash != "" {
		res, err := tx.ExecContext(ctx, `INSERT INTO snapshots (user_id, watch_id, taken_at, hash, body, shot_path) VALUES (?,?,?,?,?,?)`,
			w.UserID, w.ID, at, r.Hash, r.Body, r.ShotPath)
		if err != nil {
			return nil, err
		}
		id, _ := res.LastInsertId()
		snapID = &id
		keep := r.KeepSnapshots
		if keep < 2 {
			keep = 2
		}
		rows, err := tx.QueryContext(ctx, `SELECT id, shot_path FROM snapshots WHERE watch_id = ? AND id NOT IN
			(SELECT id FROM snapshots WHERE watch_id = ? ORDER BY id DESC LIMIT ?)`, w.ID, w.ID, keep)
		if err != nil {
			return nil, err
		}
		var old []int64
		for rows.Next() {
			var oid int64
			var p string
			if rows.Scan(&oid, &p) == nil {
				old = append(old, oid)
				if p != "" {
					gone = append(gone, p)
				}
			}
		}
		rows.Close()
		for _, oid := range old {
			if _, err := tx.ExecContext(ctx, `DELETE FROM snapshots WHERE id = ?`, oid); err != nil {
				return nil, err
			}
		}
	} else if r.ShotPath != "" && snapID != nil {
		// same text, newer picture: keep the picture current
		var old string
		_ = tx.QueryRowContext(ctx, `SELECT shot_path FROM snapshots WHERE id = ?`, *snapID).Scan(&old)
		if _, err := tx.ExecContext(ctx, `UPDATE snapshots SET shot_path = ? WHERE id = ?`, r.ShotPath, *snapID); err != nil {
			return nil, err
		}
		if old != "" && old != r.ShotPath {
			gone = append(gone, old)
		}
	}
	if changed {
		c := r.Change
		c.UserID, c.WatchID, c.At, c.AfterID = w.UserID, w.ID, at, snapID
		c.BeforeID = w.LastSnapshotID
		diff, _ := json.Marshal(c.Diff)
		visual := ""
		if len(c.Visual) > 0 {
			b, _ := json.Marshal(c.Visual)
			visual = string(b)
		}
		// a watch another Pocket app started is that app's business: its
		// changes are kept but not waiting here as new
		c.Seen = w.Source != ""
		res, err := tx.ExecContext(ctx, `INSERT INTO changes (user_id, watch_id, at, before_id, after_id, added, removed, diff, summary, summary_by,
			matched, reason, notified, visual, seen) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, c.UserID, c.WatchID, at, nullI(c.BeforeID), nullI(c.AfterID),
			c.Added, c.Removed, string(diff), c.Summary, c.SummaryBy, boolInt(c.Matched), c.Reason, c.Notified, visual, boolInt(c.Seen))
		if err != nil {
			return nil, err
		}
		c.ID, _ = res.LastInsertId()
		if c.Matched {
			if _, err := tx.ExecContext(ctx, `UPDATE watches SET unseen = unseen + ?, last_changed_at = ? WHERE id = ?`, boolInt(!c.Seen), at, w.ID); err != nil {
				return nil, err
			}
		}
		if onChange != nil {
			if err := onChange(tx, c); err != nil {
				return nil, err
			}
		}
	}
	status := StatusOK
	next := r.At.Add(r.Interval)
	if _, err := tx.ExecContext(ctx, `UPDATE watches SET status = ?, last_error = '', error_count = 0, failing_alerted = 0, last_checked_at = ?,
		next_check_at = ?, last_snapshot_id = ?, page_title = CASE WHEN ? <> '' THEN ? ELSE page_title END,
		needs_browser = CASE WHEN ? THEN 1 ELSE needs_browser END WHERE id = ?`,
		status, at, next.UTC().Format(time.RFC3339Nano), nullI(snapID), r.PageTitle, r.PageTitle, r.NeedsBrowser, w.ID); err != nil {
		return nil, err
	}
	return gone, tx.Commit()
}

// GetSnapshot returns one of the person's snapshots.
func (s *Store) GetSnapshot(ctx context.Context, uid, id int64) (*Snapshot, error) {
	var sn Snapshot
	err := s.DB.QueryRowContext(ctx, `SELECT id, watch_id, taken_at, hash, body, shot_path FROM snapshots WHERE id = ? AND user_id = ?`, id, uid).
		Scan(&sn.ID, &sn.WatchID, &sn.TakenAt, &sn.Hash, &sn.Body, &sn.ShotPath)
	if err != nil {
		return nil, notFound(err)
	}
	sn.HasShot = sn.ShotPath != ""
	return &sn, nil
}

// Snapshots lists a watch's snapshots, newest first (without their text).
func (s *Store) Snapshots(ctx context.Context, uid, watchID int64, limit int) ([]Snapshot, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, watch_id, taken_at, shot_path FROM snapshots WHERE user_id = ? AND watch_id = ? ORDER BY id DESC LIMIT ?`, uid, watchID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Snapshot{}
	for rows.Next() {
		var sn Snapshot
		if err := rows.Scan(&sn.ID, &sn.WatchID, &sn.TakenAt, &sn.ShotPath); err != nil {
			return nil, err
		}
		sn.HasShot = sn.ShotPath != ""
		out = append(out, sn)
	}
	return out, rows.Err()
}

// Checks lists a watch's last checks, newest first.
func (s *Store) Checks(ctx context.Context, uid, watchID int64, limit int) ([]Check, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, watch_id, at, ok, changed, via, ms, bytes, status, error FROM checks
		WHERE user_id = ? AND watch_id = ? ORDER BY id DESC LIMIT ?`, uid, watchID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Check{}
	for rows.Next() {
		var c Check
		var ok, ch int
		if err := rows.Scan(&c.ID, &c.WatchID, &c.At, &ok, &ch, &c.Via, &c.MS, &c.Bytes, &c.Status, &c.Error); err != nil {
			return nil, err
		}
		c.OK, c.Changed = ok == 1, ch == 1
		out = append(out, c)
	}
	return out, rows.Err()
}

// ReplaceShot points a snapshot at a picture (demo data, re-takes).
func (s *Store) ReplaceShot(ctx context.Context, uid, snapID int64, path string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE snapshots SET shot_path = ? WHERE id = ? AND user_id = ?`, path, snapID, uid)
	return err
}

// Stats counts one person's watches by state.
func (s *Store) Stats(ctx context.Context, uid int64) map[string]int {
	out := map[string]int{"watches": 0, "active": 0, "paused": 0, "failing": 0, "unseen": 0, "changed_watches": 0}
	var w, act, p, f, u, cw sql.NullInt64
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1), SUM(paused = 0), SUM(paused = 1), SUM(status = 'error' AND paused = 0), SUM(unseen), SUM(unseen > 0)
		FROM watches WHERE user_id = ?`, uid).Scan(&w, &act, &p, &f, &u, &cw)
	out["watches"], out["active"], out["paused"], out["failing"], out["unseen"], out["changed_watches"] =
		int(w.Int64), int(act.Int64), int(p.Int64), int(f.Int64), int(u.Int64), int(cw.Int64)
	var today sql.NullInt64
	y, m, d := time.Now().Date()
	start := time.Date(y, m, d, 0, 0, 0, 0, time.Local).UTC().Format(time.RFC3339Nano)
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM changes WHERE user_id = ? AND matched = 1 AND at >= ?`, uid, start).Scan(&today)
	out["changes_today"] = int(today.Int64)
	var week sql.NullInt64
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM changes WHERE user_id = ? AND matched = 1 AND at >= ?`, uid,
		time.Now().AddDate(0, 0, -7).UTC().Format(time.RFC3339Nano)).Scan(&week)
	out["changes_week"] = int(week.Int64)
	return out
}

// SortTags orders tag counts by count, then name.
func SortTags(m map[string]int) []TagCount {
	out := make([]TagCount, 0, len(m))
	for t, n := range m {
		out = append(out, TagCount{t, n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Tag < out[j].Tag
	})
	return out
}

// TagCount is one tag and how many watches carry it.
type TagCount struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}
