# Configuration

Everything is read from environment variables; every value has a default, so a bare `docker run -v ./data:/data` works. Values marked *owner* seed the first admin's own settings once on first start; after that Settings wins.

| Variable | Default | What it does |
|---|---|---|
| `PORT` / `HOST` | `8080` / `0.0.0.0` | Listen address |
| `BASE_URL` | – | Public URL: links in pushes and the RSS feed, the OIDC callback |
| `DATA_DIR` | `./data` (`/data` in the image) | SQLite database, page pictures and backups |
| `DB_PATH` / `FILES_DIR` | `$DATA_DIR/pocket-lookout.db` / `$DATA_DIR/files` | Override locations |
| `TZ` | – | Container timezone (the daily summary goes out at the person's hour in it) |
| `LOG_LEVEL` / `LOG_FORMAT` | `info` / `text` | `debug`, `warn`, `error`; `json` |
| `TRUST_PROXY` | `true` | Honour `X-Forwarded-For` |
| `METRICS_ENABLED` | `true` | Prometheus `/metrics` (row counts and check counters only, never content) |
| `MAX_BODY_BYTES` | `67108864` | Largest import (a changedetection.io backup zip) |
| `AUTH_MODE` | `local` | `local` (built-in accounts), `proxy` (trust `AUTH_PROXY_HEADER`), `none` (single user, LAN only) |
| `AUTH_PROXY_HEADER` / `AUTH_PROXY_EMAIL_HEADER` | `Remote-User` / `Remote-Email` | Headers for proxy mode |
| `SESSION_TTL` | `720h` | Cookie lifetime |
| `SECURE_COOKIES` | `false` | Set `true` behind HTTPS (on plain http the browser drops the cookie and login bounces) |
| `OIDC_ISSUER` / `OIDC_CLIENT_ID` / `OIDC_CLIENT_SECRET` | – | Sign in with an OpenID Connect provider. Needs `BASE_URL`; the callback is `BASE_URL/api/v1/auth/oidc/callback`. With Pocket Core none of these are needed. |
| `OIDC_NAME` / `OIDC_AUTO_CREATE` | `single sign-on` / `true` | Button label; give an unknown verified email a member account |
| `ADMIN_EMAIL` / `ADMIN_PASSWORD` / `ADMIN_NAME` | – | Create the first admin on an empty database (otherwise the UI asks) |
| `NOTIFY_URL` / `NOTIFY_FORMAT` | – / guessed | *Owner.* Apprise `…/notify/<key>`, ntfy topic URL or JSON webhook; `apprise`, `ntfy`, `json` |
| `OPENAI_API_KEY` / `OPENAI_MODEL` / `OPENAI_BASE_URL` | – / `gpt-4o-mini` / OpenAI | *Owner.* An OpenAI-compatible AI for summaries and conditions. With Pocket Core, Pocket AI is used instead when the `inference:write` link is on |
| `BROWSER_URL` | – | A headless Chrome for pages that need JavaScript and for page pictures: a DevTools websocket (`ws://browser:3000` for [sockpuppetbrowser](https://github.com/dgtlmoon/sockpuppetbrowser), a browserless URL) or `http://chrome:9222` (Chrome with `--remote-debugging-port`). Empty = pages are read without JavaScript |
| `BROWSER_TIMEOUT` | `60s` | Longest a page may take in the browser |
| `CHECK_WORKERS` | `3` | Pages checked at the same time (the browser gets one less) |
| `MIN_INTERVAL` | `5m` | The shortest check interval anyone may pick |
| `FETCH_PRIVATE` | `false` | Let watches reach private / loopback / tailnet addresses. Off, a member cannot make the server probe your LAN (the browser path checks the address too) |
| `USER_AGENT` | a desktop Chrome string + `PocketLookout/1.0` | Sent with plain requests |
| `CORE_URL` / `CORE_REGISTRATION_KEY` | – | [Pocket Core](https://github.com/abjelosevic88/pocket-homelab-core): register on start, sign in with Pocket Core, Pocket AI, offer `watches:read` / `watches:write`, publish `lookout.changed`, `lookout.failing`, `lookout.watch_added` |
| `CORE_APP_URL` | `BASE_URL` | How Core reaches this app for health checks |
| `DEMO_DATA` | `false` | Seed eighteen watches with months of changes into an empty owner account. Demo watches are not checked on their own |

## How a check works

1. **Read** — plain HTTP first (`auto`); when the page is nearly empty, says "enable JavaScript", or refuses robots (401/403/429/503), the browser reads it and the watch remembers that. `fast` never uses the browser, `browser` always does (and can keep a picture).
2. **Pick** — the whole page as text (blocks on their own lines, table cells joined with `|`, list items with `•`), or only what CSS selectors find (one per line; parts can be left out with selectors too), `$.path[0].to.value` on JSON, or `@price` — the price and availability a shop publishes in its schema.org / Open Graph data. A picked part that disappears is an error, not an empty page.
3. **Compare** — lines matching an ignore rule are left out. With the noise filter on, dates, times, "5 minutes ago" and view / like / comment counters are masked, so only real changes differ. Unchanged text adds no new version.
4. **Decide** — *counts as a change*: any, only additions, only removals. *Only when a changed line mentions*: plain text (any case) or `/regular expression/`. *AI condition*: the AI reads the added and removed lines and answers yes or no with a reason. Changes that do not count are kept (Changes → show the ones that did not count) but tell nobody.
5. **Tell** — a one-line summary (AI or plain), then a push right away, a line in the daily summary, or nothing; always the RSS feed and the `lookout.changed` event.

A page that fails is tried again after 10 minutes twice, then at its interval (doubled after five failures). After *n* failures in a row (Settings → Notifications) one push says so, and one more when it works again.

## CLI

```
pocket-lookout                     start the server (default)
pocket-lookout backup [-o FILE]    write a consistent SQLite backup
pocket-lookout reset-password [-email USER] [-password NEW]
pocket-lookout version
```

Inside the container: `docker exec pocket-lookout pocket-lookout backup`.
