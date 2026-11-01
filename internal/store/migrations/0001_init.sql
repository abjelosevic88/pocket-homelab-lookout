CREATE TABLE users (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  email         TEXT NOT NULL UNIQUE,
  name          TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL DEFAULT '',
  role          TEXT NOT NULL DEFAULT 'member',
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at    TEXT
);

CREATE TABLE sessions (
  id         TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE api_tokens (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL DEFAULT '',
  token_hash   TEXT NOT NULL UNIQUE,
  prefix       TEXT NOT NULL DEFAULT '',
  last_used_at TEXT,
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Box-wide settings (title, setup flag); everything else is personal.
CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL DEFAULT ''
);

CREATE TABLE user_settings (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key     TEXT NOT NULL,
  value   TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (user_id, key)
);

CREATE TABLE cache (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL DEFAULT '',
  fetched_at TEXT NOT NULL
);

-- One watched page (or one part of it). Ids are never reused
-- (AUTOINCREMENT): links from other apps and feeds point at them.
CREATE TABLE watches (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id          INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  url              TEXT NOT NULL,
  title            TEXT NOT NULL DEFAULT '',     -- the person's name for it ('' = the page title)
  page_title       TEXT NOT NULL DEFAULT '',     -- what the page calls itself
  tags             TEXT NOT NULL DEFAULT '',     -- "a, b"
  notes            TEXT NOT NULL DEFAULT '',
  -- what to read
  selector         TEXT NOT NULL DEFAULT '',     -- CSS selectors (one per line), "$.a.b" for JSON, "@price"; '' = whole page
  exclude          TEXT NOT NULL DEFAULT '',     -- CSS selectors left out (one per line)
  fetch_mode       TEXT NOT NULL DEFAULT 'auto', -- auto | fast | browser
  needs_browser    INTEGER NOT NULL DEFAULT 0,   -- auto found the page needs the browser
  browser_wait     INTEGER NOT NULL DEFAULT 2,   -- seconds to let the page settle
  headers          TEXT NOT NULL DEFAULT '',     -- "Name: value" per line
  screenshot       INTEGER NOT NULL DEFAULT 0,   -- keep a picture with every check (browser)
  -- when it counts as a change
  noise_filter     INTEGER NOT NULL DEFAULT 1,   -- ignore dates, times, counters
  ignore_text      TEXT NOT NULL DEFAULT '',     -- lines containing these (or /regex/) never count
  notify_on        TEXT NOT NULL DEFAULT 'any',  -- any | added | removed
  trigger_text     TEXT NOT NULL DEFAULT '',     -- only when a changed line contains one of these
  ai_condition     TEXT NOT NULL DEFAULT '',     -- "only if a 2-bedroom under 800 shows up"
  -- telling
  notify_mode      TEXT NOT NULL DEFAULT 'now',  -- now | digest | off
  interval_minutes INTEGER NOT NULL DEFAULT 360,
  paused           INTEGER NOT NULL DEFAULT 0,
  source           TEXT NOT NULL DEFAULT '',     -- '' = the person; else the Pocket app (slug) that added it
  -- state
  status           TEXT NOT NULL DEFAULT 'new',  -- new | ok | error
  last_checked_at  TEXT NOT NULL DEFAULT '',
  last_changed_at  TEXT NOT NULL DEFAULT '',
  next_check_at    TEXT NOT NULL DEFAULT '',
  last_error       TEXT NOT NULL DEFAULT '',
  error_count      INTEGER NOT NULL DEFAULT 0,
  failing_alerted  INTEGER NOT NULL DEFAULT 0,
  last_snapshot_id INTEGER,
  unseen           INTEGER NOT NULL DEFAULT 0,   -- changes not looked at yet
  created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX watches_user ON watches(user_id, paused);
CREATE INDEX watches_due ON watches(paused, next_check_at);

-- What a watched page said; a new row only when it says something else.
CREATE TABLE snapshots (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  watch_id   INTEGER NOT NULL REFERENCES watches(id) ON DELETE CASCADE,
  taken_at   TEXT NOT NULL,
  hash       TEXT NOT NULL,
  body       TEXT NOT NULL,
  shot_path  TEXT NOT NULL DEFAULT ''             -- FILES_DIR relative
);
CREATE INDEX snapshots_watch ON snapshots(user_id, watch_id, id);

-- One change between two snapshots. The diff is kept with the change so it
-- survives old snapshots being pruned.
CREATE TABLE changes (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  watch_id    INTEGER NOT NULL REFERENCES watches(id) ON DELETE CASCADE,
  at          TEXT NOT NULL,
  before_id   INTEGER REFERENCES snapshots(id) ON DELETE SET NULL,
  after_id    INTEGER REFERENCES snapshots(id) ON DELETE SET NULL,
  added       INTEGER NOT NULL DEFAULT 0,
  removed     INTEGER NOT NULL DEFAULT 0,
  diff        TEXT NOT NULL DEFAULT '[]',       -- JSON [{op: "+"|"-"|"=", text}] (with some context)
  summary     TEXT NOT NULL DEFAULT '',
  summary_by  TEXT NOT NULL DEFAULT '',          -- ai | auto
  matched     INTEGER NOT NULL DEFAULT 1,        -- passed the "only when" rules
  reason      TEXT NOT NULL DEFAULT '',          -- why it did (not) count
  notified    TEXT NOT NULL DEFAULT '',          -- '' | now | digest | sent | failed
  seen        INTEGER NOT NULL DEFAULT 0,
  dismissed   INTEGER NOT NULL DEFAULT 0,        -- "not important"
  visual      TEXT NOT NULL DEFAULT ''           -- JSON [[x,y,w,h]] changed areas of the picture (0..1)
);
CREATE INDEX changes_user ON changes(user_id, at);
CREATE INDEX changes_watch ON changes(user_id, watch_id, at);

-- The last checks of each watch (pruned to 100 per watch).
CREATE TABLE checks (
  id        INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  watch_id  INTEGER NOT NULL REFERENCES watches(id) ON DELETE CASCADE,
  at        TEXT NOT NULL,
  ok        INTEGER NOT NULL DEFAULT 1,
  changed   INTEGER NOT NULL DEFAULT 0,
  via       TEXT NOT NULL DEFAULT 'fast',       -- fast | browser
  ms        INTEGER NOT NULL DEFAULT 0,
  bytes     INTEGER NOT NULL DEFAULT 0,
  status    INTEGER NOT NULL DEFAULT 0,         -- HTTP status
  error     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX checks_watch ON checks(user_id, watch_id, id);
