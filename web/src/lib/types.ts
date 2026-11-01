export interface User { id: number; email: string; name: string; role: 'admin' | 'member'; created_at: string }
export interface APIToken { id: number; user_id: number; name: string; prefix: string; last_used_at: string | null; created_at: string }

export type FetchMode = 'auto' | 'fast' | 'browser'
export type NotifyMode = 'now' | 'digest' | 'off'
export type NotifyOn = 'any' | 'added' | 'removed'
export type WatchStatus = 'new' | 'ok' | 'error' | 'checking'

export interface Watch {
  id: number; user_id: number; url: string; title: string; page_title: string; tags: string; notes: string
  selector: string; exclude: string; fetch_mode: FetchMode; needs_browser: boolean; browser_wait: number; headers: string; screenshot: boolean
  noise_filter: boolean; ignore_text: string; notify_on: NotifyOn; trigger_text: string; ai_condition: string
  notify_mode: NotifyMode; interval_minutes: number; paused: boolean; source: string
  status: WatchStatus; last_checked_at: string; last_changed_at: string; next_check_at: string; last_error: string; error_count: number
  last_snapshot_id: number | null; unseen: number; created_at: string; updated_at: string
  // computed
  name: string; host: string; changes: number; last_summary: string; has_shot: boolean
}

/** What a watch form sends (create or update). */
export type WatchInput = Partial<Pick<Watch, 'url' | 'title' | 'tags' | 'notes' | 'selector' | 'exclude' | 'fetch_mode' | 'browser_wait' | 'headers' | 'screenshot' |
  'noise_filter' | 'ignore_text' | 'notify_on' | 'trigger_text' | 'ai_condition' | 'notify_mode' | 'interval_minutes' | 'paused'>>

/** "+" added, "-" removed, "=" unchanged context, "~" a gap of unchanged lines. */
export interface DiffLine { op: '+' | '-' | '=' | '~'; text: string }

export interface Change {
  id: number; user_id: number; watch_id: number; at: string; before_id: number | null; after_id: number | null
  added: number; removed: number; diff: DiffLine[]; summary: string; summary_by: '' | 'ai' | 'auto'
  matched: boolean; reason: string; notified: '' | 'now' | 'digest' | 'sent' | 'failed'; seen: boolean; dismissed: boolean
  visual: [number, number, number, number][] | null
  watch_name: string; watch_url: string; host: string; has_shots: boolean
}

export interface ChangeDetail { change: Change; prev: number; next: number; watch: Watch | null }

export interface Check { id: number; watch_id: number; at: string; ok: boolean; changed: boolean; via: 'fast' | 'browser'; ms: number; bytes: number; status: number; error: string }

export interface Snapshot { id: number; watch_id: number; taken_at: string; body?: string; has_shot: boolean }

export interface Outcome { ok: boolean; changed: boolean; first: boolean; change?: Change; error?: string; via: string; ms: number; ignored?: boolean }

export interface Block { selector: string; label: string; text: string; lines: number; found?: boolean }

export interface Preview {
  url: string; final_url: string; title: string; kind: 'html' | 'json' | 'text' | 'other' | ''; via: string; ms: number; status: number
  text: string; lines: number; blocks: Block[]; has_price: boolean; price?: string; needs_browser: boolean; browser: boolean; error?: string; shot?: string
}

export interface TagCount { tag: string; count: number }

export interface Dashboard {
  watches: number; active: number; paused: number; failing: number; unseen: number; changed_watches: number
  changes_today: number; changes_week: number; per_day: Record<string, number>; latest: Change[]; browser: boolean
}

export interface ImportResult { from: string; added: number; skipped: number; notes: string[] }

export interface AIStatus { ready: boolean; provider: '' | 'pocket' | 'openai'; detail: string }

export interface Settings {
  core_configured: boolean
  core_registered: boolean | undefined
  title: string
  date_format: string
  time_format: string
  default_interval: number
  default_noise: boolean
  default_notify_mode: NotifyMode
  default_fetch_mode: FetchMode
  keep_snapshots: number
  keep_days: number
  notify_url: string
  notify_format: string
  notify_configured: boolean
  notify_errors: boolean
  errors_after: number
  digest_hour: number
  ai_summaries: boolean
  ai_provider: 'auto' | 'pocket' | 'openai'
  ai_pocket_model: string
  openai_api_key: string
  openai_model: string
  openai_base_url: string
  feed_url: string
  browser: boolean
  min_interval: number
  fetch_private: boolean
  setup_complete: boolean
  base_url: string
}
