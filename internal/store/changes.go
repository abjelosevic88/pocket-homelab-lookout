package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

const changeCols = `c.id, c.user_id, c.watch_id, c.at, c.before_id, c.after_id, c.added, c.removed, c.diff, c.summary, c.summary_by,
	c.matched, c.reason, c.notified, c.seen, c.dismissed, c.visual, w.title, w.page_title, w.url,
	COALESCE((SELECT shot_path FROM snapshots WHERE id = c.before_id), ''), COALESCE((SELECT shot_path FROM snapshots WHERE id = c.after_id), '')`

func scanChange(row interface{ Scan(...any) error }) (*Change, error) {
	var c Change
	var before, after sql.NullInt64
	var diff, visual, title, pageTitle, beforeShot, afterShot string
	var matched, seen, dismissed int
	err := row.Scan(&c.ID, &c.UserID, &c.WatchID, &c.At, &before, &after, &c.Added, &c.Removed, &diff, &c.Summary, &c.SummaryBy,
		&matched, &c.Reason, &c.Notified, &seen, &dismissed, &visual, &title, &pageTitle, &c.WatchURL, &beforeShot, &afterShot)
	if err != nil {
		return nil, notFound(err)
	}
	if before.Valid {
		c.BeforeID = &before.Int64
	}
	if after.Valid {
		c.AfterID = &after.Int64
	}
	c.Matched, c.Seen, c.Dismissed = matched == 1, seen == 1, dismissed == 1
	_ = json.Unmarshal([]byte(diff), &c.Diff)
	if c.Diff == nil {
		c.Diff = []DiffLine{}
	}
	if visual != "" {
		_ = json.Unmarshal([]byte(visual), &c.Visual)
	}
	w := Watch{Title: title, PageTitle: pageTitle, URL: c.WatchURL}
	w.Fill()
	c.WatchName, c.Host = w.Name, w.Host
	c.HasShots = beforeShot != "" && afterShot != ""
	return &c, nil
}

// ChangeFilter narrows the list of changes.
type ChangeFilter struct {
	WatchID int64
	Unseen  bool
	All     bool   // include changes that did not pass the "only when" rules
	Before  int64  // older than this id (paging)
	From    string // at >= (RFC3339)
	To      string // at < (RFC3339)
	Tag     string
	Limit   int
}

// ListChanges returns the person's changes, newest first.
func (s *Store) ListChanges(ctx context.Context, uid int64, f ChangeFilter) ([]Change, error) {
	q := `SELECT ` + changeCols + ` FROM changes c JOIN watches w ON w.id = c.watch_id WHERE c.user_id = ?`
	args := []any{uid}
	if f.WatchID > 0 {
		q += ` AND c.watch_id = ?`
		args = append(args, f.WatchID)
	}
	if !f.All {
		q += ` AND c.matched = 1`
	}
	if f.Unseen {
		q += ` AND c.seen = 0`
	}
	if f.Before > 0 {
		q += ` AND c.id < ?`
		args = append(args, f.Before)
	}
	if f.From != "" {
		q += ` AND c.at >= ?`
		args = append(args, f.From)
	}
	if f.To != "" {
		q += ` AND c.at < ?`
		args = append(args, f.To)
	}
	if f.Tag != "" {
		q += ` AND (', ' || lower(w.tags) || ',') LIKE ?`
		args = append(args, "%, "+strings.ToLower(f.Tag)+",%")
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 50
	}
	q += ` ORDER BY c.id DESC LIMIT ?`
	args = append(args, f.Limit)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Change{}
	for rows.Next() {
		c, err := scanChange(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// GetChange returns one of the person's changes.
func (s *Store) GetChange(ctx context.Context, uid, id int64) (*Change, error) {
	return scanChange(s.DB.QueryRowContext(ctx, `SELECT `+changeCols+` FROM changes c JOIN watches w ON w.id = c.watch_id WHERE c.id = ? AND c.user_id = ?`, id, uid))
}

// Neighbours are the previous and next change of the same watch.
func (s *Store) Neighbours(ctx context.Context, uid int64, c *Change) (prev, next int64) {
	_ = s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM changes WHERE user_id = ? AND watch_id = ? AND id < ? AND matched = 1`, uid, c.WatchID, c.ID).Scan(&prev)
	_ = s.DB.QueryRowContext(ctx, `SELECT COALESCE(MIN(id), 0) FROM changes WHERE user_id = ? AND watch_id = ? AND id > ? AND matched = 1`, uid, c.WatchID, c.ID).Scan(&next)
	return
}

// MarkSeen marks changes as looked at: one change, one watch's, or all
// (both 0). The watches' unseen counts follow.
func (s *Store) MarkSeen(ctx context.Context, uid, watchID, changeID int64) (int, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	q := `UPDATE changes SET seen = 1 WHERE user_id = ? AND seen = 0`
	args := []any{uid}
	if changeID > 0 {
		q += ` AND id = ?`
		args = append(args, changeID)
	} else if watchID > 0 {
		q += ` AND watch_id = ?`
		args = append(args, watchID)
	}
	res, err := tx.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if _, err := tx.ExecContext(ctx, `UPDATE watches SET unseen = (SELECT COUNT(1) FROM changes c WHERE c.watch_id = watches.id AND c.seen = 0 AND c.matched = 1)
		WHERE user_id = ?`, uid); err != nil {
		return 0, err
	}
	return int(n), tx.Commit()
}

// Dismiss marks a change as not important (and seen).
func (s *Store) Dismiss(ctx context.Context, uid, id int64, dismissed bool) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE changes SET dismissed = ?, seen = 1 WHERE id = ? AND user_id = ?`, boolInt(dismissed), id, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE watches SET unseen = (SELECT COUNT(1) FROM changes c WHERE c.watch_id = watches.id AND c.seen = 0 AND c.matched = 1)
		WHERE user_id = ?`, uid)
	return err
}

// SetNotified records how a change was told.
func (s *Store) SetNotified(ctx context.Context, ids []int64, state string) error {
	for _, id := range ids {
		if _, err := s.DB.ExecContext(ctx, `UPDATE changes SET notified = ? WHERE id = ?`, state, id); err != nil {
			return err
		}
	}
	return nil
}

// SetSummary replaces a change's summary (an AI summary written later).
func (s *Store) SetSummary(ctx context.Context, uid, id int64, summary, by string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE changes SET summary = ?, summary_by = ? WHERE id = ? AND user_id = ?`, summary, by, id, uid)
	return err
}

// DigestPending lists a person's changes waiting for the daily summary.
func (s *Store) DigestPending(ctx context.Context, uid int64) ([]Change, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+changeCols+` FROM changes c JOIN watches w ON w.id = c.watch_id
		WHERE c.user_id = ? AND c.notified = 'digest' ORDER BY c.id`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Change{}
	for rows.Next() {
		c, err := scanChange(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// PruneChanges drops a person's changes older than days (0 = keep all).
func (s *Store) PruneChanges(ctx context.Context, uid int64, days int) (int, error) {
	if days <= 0 {
		return 0, nil
	}
	cut := time.Now().AddDate(0, 0, -days).UTC().Format(time.RFC3339Nano)
	res, err := s.DB.ExecContext(ctx, `DELETE FROM changes WHERE user_id = ? AND at < ?`, uid, cut)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ChangeCountsByDay counts a person's changes per local day for the last n days.
func (s *Store) ChangeCountsByDay(ctx context.Context, uid int64, days int) (map[string]int, error) {
	from := time.Now().AddDate(0, 0, -days).UTC().Format(time.RFC3339Nano)
	rows, err := s.DB.QueryContext(ctx, `SELECT at FROM changes WHERE user_id = ? AND matched = 1 AND at >= ?`, uid, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var at string
		if rows.Scan(&at) == nil {
			if t, err := time.Parse(time.RFC3339Nano, at); err == nil {
				out[t.Local().Format("2006-01-02")]++
			}
		}
	}
	return out, rows.Err()
}
