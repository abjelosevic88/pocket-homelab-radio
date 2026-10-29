# Backup

One SQLite file: `DATA_DIR/pocket-radio.db` (WAL mode). Take a consistent copy with `pocket-radio backup` (writes `DATA_DIR/backups/pocket-radio-<time>.db` with `VACUUM INTO`) or Settings → Backup & data → Download database (admin). Back up that copy, not the live file. `DATA_DIR/core.token*` is the Pocket Core registration.

Restore: stop the app, replace `pocket-radio.db` (remove `-wal`/`-shm` files), start.

Every person can also export their own data as JSON (`/api/v1/export.json`: stations, channels, listening and heard songs; re-importable, importing twice adds nothing), their stations as an M3U playlist other radio apps read (`/api/v1/stations/export.m3u`) and their listening as CSV (`/api/v1/export.csv`).

`DATA_DIR/logos/` is a cache and need not be backed up.
