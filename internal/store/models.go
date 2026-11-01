package store

import "strings"

// User is an account on this box.
type User struct {
	ID           int64  `json:"id"`
	Email        string `json:"email"`
	Name         string `json:"name"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"` // admin | member
	CreatedAt    string `json:"created_at"`
}

// APIToken is a personal access token (hash stored, plaintext shown once).
type APIToken struct {
	ID         int64   `json:"id"`
	UserID     int64   `json:"user_id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"`
	LastUsedAt *string `json:"last_used_at"`
	CreatedAt  string  `json:"created_at"`
}

// Fetch modes.
const (
	FetchAuto    = "auto"    // fast, and the browser when the page needs it
	FetchFast    = "fast"    // plain HTTP only
	FetchBrowser = "browser" // always the headless browser
)

// Notify modes.
const (
	NotifyNow    = "now"
	NotifyDigest = "digest"
	NotifyOff    = "off"
)

// Watch statuses.
const (
	StatusNew   = "new"
	StatusOK    = "ok"
	StatusError = "error"
)

// Watch is one watched page (or a part of it).
type Watch struct {
	ID        int64  `json:"id"`
	UserID    int64  `json:"user_id"`
	URL       string `json:"url"`
	Title     string `json:"title"`
	PageTitle string `json:"page_title"`
	Tags      string `json:"tags"`
	Notes     string `json:"notes"`

	Selector     string `json:"selector"`
	Exclude      string `json:"exclude"`
	FetchMode    string `json:"fetch_mode"`
	NeedsBrowser bool   `json:"needs_browser"`
	BrowserWait  int    `json:"browser_wait"`
	Headers      string `json:"headers"`
	Screenshot   bool   `json:"screenshot"`

	NoiseFilter bool   `json:"noise_filter"`
	IgnoreText  string `json:"ignore_text"`
	NotifyOn    string `json:"notify_on"`
	TriggerText string `json:"trigger_text"`
	AICondition string `json:"ai_condition"`

	NotifyMode      string `json:"notify_mode"`
	IntervalMinutes int    `json:"interval_minutes"`
	Paused          bool   `json:"paused"`
	Source          string `json:"source"`

	Status         string `json:"status"`
	LastCheckedAt  string `json:"last_checked_at"`
	LastChangedAt  string `json:"last_changed_at"`
	NextCheckAt    string `json:"next_check_at"`
	LastError      string `json:"last_error"`
	ErrorCount     int    `json:"error_count"`
	FailingAlerted bool   `json:"-"`
	LastSnapshotID *int64 `json:"last_snapshot_id"`
	Unseen         int    `json:"unseen"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`

	// computed
	Name        string `json:"name"`         // title, else page title, else host
	Host        string `json:"host"`         // the page's host
	Changes     int    `json:"changes"`      // changes recorded
	LastSummary string `json:"last_summary"` // the newest change's summary
	HasShot     bool   `json:"has_shot"`     // the latest snapshot has a picture
}

// Snapshot is what a page said at one moment.
type Snapshot struct {
	ID       int64  `json:"id"`
	WatchID  int64  `json:"watch_id"`
	TakenAt  string `json:"taken_at"`
	Hash     string `json:"-"`
	Body     string `json:"body"`
	ShotPath string `json:"-"`
	HasShot  bool   `json:"has_shot"`
}

// DiffLine is one line of a change: "+" added, "-" removed, "=" context,
// "~" a gap of unchanged lines left out.
type DiffLine struct {
	Op   string `json:"op"`
	Text string `json:"text"`
}

// Change is one difference between two snapshots.
type Change struct {
	ID        int64        `json:"id"`
	UserID    int64        `json:"user_id"`
	WatchID   int64        `json:"watch_id"`
	At        string       `json:"at"`
	BeforeID  *int64       `json:"before_id"`
	AfterID   *int64       `json:"after_id"`
	Added     int          `json:"added"`
	Removed   int          `json:"removed"`
	Diff      []DiffLine   `json:"diff"`
	Summary   string       `json:"summary"`
	SummaryBy string       `json:"summary_by"` // ai | auto
	Matched   bool         `json:"matched"`
	Reason    string       `json:"reason"`
	Notified  string       `json:"notified"`
	Seen      bool         `json:"seen"`
	Dismissed bool         `json:"dismissed"`
	Visual    [][4]float64 `json:"visual"`

	// joined
	WatchName string `json:"watch_name"`
	WatchURL  string `json:"watch_url"`
	Host      string `json:"host"`
	HasShots  bool   `json:"has_shots"` // both sides have a picture
}

// Check is one look at a page.
type Check struct {
	ID      int64  `json:"id"`
	WatchID int64  `json:"watch_id"`
	At      string `json:"at"`
	OK      bool   `json:"ok"`
	Changed bool   `json:"changed"`
	Via     string `json:"via"`
	MS      int    `json:"ms"`
	Bytes   int    `json:"bytes"`
	Status  int    `json:"status"`
	Error   string `json:"error"`
}

// Settings are the box-wide values plus one person's preferences.
type Settings struct {
	Title         string `json:"title"`
	SetupComplete bool   `json:"setup_complete"`

	DateFormat string `json:"date_format"`
	TimeFormat string `json:"time_format"`

	// What a new watch starts with.
	DefaultInterval   int    `json:"default_interval"` // minutes
	DefaultNoise      bool   `json:"default_noise"`
	DefaultNotifyMode string `json:"default_notify_mode"`
	DefaultFetchMode  string `json:"default_fetch_mode"`
	KeepSnapshots     int    `json:"keep_snapshots"` // per watch
	KeepDays          int    `json:"keep_days"`      // changes older than this go (0 = keep)

	NotifyURL    string `json:"notify_url"`
	NotifyFormat string `json:"notify_format"` // apprise | ntfy | json
	NotifyErrors bool   `json:"notify_errors"` // tell me when a watch keeps failing
	ErrorsAfter  int    `json:"errors_after"`  // …after this many failed checks in a row
	DigestHour   int    `json:"digest_hour"`   // when the daily summary goes out

	// AI: a one-line summary of every change, and "only if" conditions.
	AISummaries   bool   `json:"ai_summaries"`
	AIProvider    string `json:"ai_provider"` // auto | pocket | openai
	AIPocketModel string `json:"ai_pocket_model"`
	OpenAIKey     string `json:"openai_api_key"`
	OpenAIModel   string `json:"openai_model"`
	OpenAIBaseURL string `json:"openai_base_url"`

	// The secret in this person's RSS feed address.
	FeedToken string `json:"feed_token"`
}

// SplitTags turns "a, b ,c" into clean lowercase tags.
func SplitTags(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range strings.Split(s, ",") {
		t = strings.ToLower(strings.TrimSpace(t))
		if t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// JoinTags is the stored form of tags.
func JoinTags(s string) string { return strings.Join(SplitTags(s), ", ") }

// Lines splits a multi-line setting into its trimmed, non-empty lines.
func Lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}
