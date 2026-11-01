# API

JSON over HTTP at `/api/v1`. Authenticate with the session cookie (the web app), `Authorization: Bearer <token>` (a personal token from Settings → API & shortcuts, prefix `plk_`), a Pocket Core device/person token, or a Pocket Core link token (`pcx_`, another Pocket app acting for a person — see below). Everything is per person: another person's watches and changes answer `404`, admins included.

## Watches

| | |
|---|---|
| `GET /watches?state=changed\|failing\|paused\|active&tag=&q=` | The person's watches (newest change first) |
| `POST /watches` | `{"url": "https://…"}` plus any of the fields below. Checked right away |
| `GET /watches/{id}` · `PUT /watches/{id}` · `DELETE /watches/{id}` | One watch; `PUT` changes only the fields sent |
| `POST /watches/{id}/check` | Check now; answers `{outcome, watch}` (`outcome.changed`, `outcome.change`, `outcome.error`) |
| `POST /watches/{id}/pause` · `/resume` · `/duplicate` | |
| `POST /watches/{id}/ignore` | `{"lines": ["…"]}` — add lines that never count |
| `GET /watches/{id}/changes?all=1&before=&limit=` · `/checks` · `/snapshot` | History, the last 100 checks, the current text |
| `POST /watches/bulk` | `{"ids": [1,2], "action": "check\|pause\|resume\|delete\|tag\|untag", "tag": "…"}`; `{"action":"check"}` without ids checks everything |
| `POST /preview` | `{"url", "selector", "exclude", "fetch_mode", "find"}` → title, text, suggested parts (`blocks`), `has_price` |

Watch fields: `url`, `title`, `tags` ("a, b"), `notes`, `selector` (CSS selectors one per line, `$.json.path`, or `@price`), `exclude`, `fetch_mode` (`auto|fast|browser`), `browser_wait` (s), `headers` ("Name: value" lines), `screenshot`, `noise_filter`, `ignore_text`, `notify_on` (`any|added|removed`), `trigger_text`, `ai_condition`, `notify_mode` (`now|digest|off`), `interval_minutes`, `paused`.

## Changes

| | |
|---|---|
| `GET /changes?unseen=1&all=1&tag=&watch=&before=&limit=` | Newest first; `all=1` includes changes that did not count |
| `GET /changes/{id}` | `{change, prev, next, watch}` — `change.diff` is `[{op: "+"\|"-"\|"="\|"~", text}]` |
| `POST /changes/seen` | `{"watch_id": 3}` or nothing (= all) · `POST /changes/{id}/seen` |
| `POST /changes/{id}/dismiss` | `{"ignore": true}` also ignores its lines from now on; `{"undo": true}` |
| `GET /snapshots/{id}/shot` | A page picture (browser watches with pictures on) |

## Other

`GET /dashboard` (Homepage widget: `watches`, `unseen`, `failing`, `changes_today`, `changes_week`, `latest`), `GET /tags`, `GET /day/{YYYY-MM-DD}` (Pocket Day), `GET /export.json`, `POST /import` (raw body: a URL list, a changedetection.io backup `.zip` / `url-watches.json`, or a Lookout export; `?tags=` adds tags), `GET /backup` (admin), `GET /settings`, `PUT /settings`, `POST /feed/rotate`.

RSS without login: `GET /feed/<secret>.xml` (all changes that counted) and `/feed/<secret>/<watch id>.xml`. The secret is in Settings → Notifications.

## Pocket Core links

Lookout offers two scopes to other Pocket apps (switched on per app and person in Pocket Core → Links):

- `watches:read` — `GET /watches`, `/watches/{id}`, `/watches/{id}/changes|checks|snapshot`, `/changes`, `/changes/{id}`, `/dashboard`, `/tags`, `/day/{date}`
- `watches:write` — `POST /watches`, `POST /preview`, `PUT|DELETE /watches/{id}`, `POST /watches/{id}/check|pause|resume`; a linked app may change only the watches it added (`source` = its slug)

Events on the Core bus: `lookout.changed` (`watch_id`, `change_id`, `title`, `url`, `tags`, `summary`, `added`, `removed`, `added_lines`, `removed_lines`, `reason`, `link`), `lookout.failing` (`watch_id`, `title`, `url`, `error`, `failed_checks`), `lookout.watch_added`. Pocket Flows can start a flow on any of them.
