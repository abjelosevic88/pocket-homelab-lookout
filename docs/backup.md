# Backup & restore

Everything lives in `DATA_DIR` (`/data` in the container):

- `pocket-lookout.db` (+ `-wal` / `-shm` while running) — watches, page versions, changes, checks, settings, users
- `files/shots/<watch>/…` — page pictures (browser watches with pictures on)
- `backups/` — where the CLI writes snapshots

## Consistent copies

Never copy a live WAL database file. Use one of:

- **UI**: Settings → Backup & data → *Download database*
- **API**: `GET /api/v1/backup` with a bearer token
- **CLI**: `docker exec pocket-lookout pocket-lookout backup` → prints the path under `/data/backups/`
- **sqlite3**: `sqlite3 data/pocket-lookout.db "VACUUM INTO 'snap.db'"`

Then back up `files/` with your usual tool (restic, Backrest, rsync). Exclude `pocket-lookout.db*` from raw file backups and include the snapshot instead.

## Restore

1. Stop the container.
2. Replace `data/pocket-lookout.db` with the snapshot (remove any stale `-wal` / `-shm`).
3. Restore `files/`.
4. Start the container. Migrations run forward automatically.

## JSON export

Settings → Backup & data exports everything but users and tokens as one JSON file; importing it merges (watches by name and kind, watches by list and title).
