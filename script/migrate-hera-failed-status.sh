#!/usr/bin/env bash
# One-off migration: widen the hera_role_status.status CHECK to accept the new
# `failed` role-status (make-hera-plan-living, Phase A / decision D2).
#
# WHY THIS IS NEEDED: schema.go's CREATE TABLE includes 'failed' in the CHECK, so
# a FRESH ~/.argus/data.sql is already correct and needs nothing. But CREATE TABLE
# IF NOT EXISTS is a no-op on a database that already has the table, and SQLite
# cannot ALTER a CHECK constraint. So an EXISTING DB keeps the old CHECK
# (idle|working|blocked|done); the first `failed` write fails the constraint and
# (via hera_send's soft-fail status-apply) silently no-ops. This rebuilds just the
# hera_role_status table with the widened CHECK, preserving all rows.
#
# Per CLAUDE.md ("breaking changes fine, no backwards-compat / no legacy migration
# code — write a ONE-OFF script for schema data moves"), this is that one-off
# script. Run it once against a live DB after deploying the Phase A binary; do NOT
# wire it into app startup.
#
# USAGE (stop the daemon first so it isn't holding the DB):
#   script/migrate-hera-failed-status.sh                 # default ~/.argus/data.sql
#   script/migrate-hera-failed-status.sh /path/to/data.sql
#
# Idempotent: safe to re-run. On a DB whose CHECK already allows 'failed' it just
# rebuilds the table identically.
set -euo pipefail

DB="${1:-$HOME/.argus/data.sql}"

if ! command -v sqlite3 >/dev/null 2>&1; then
  echo "error: sqlite3 not found on PATH" >&2
  exit 1
fi
if [ ! -f "$DB" ]; then
  echo "error: database not found at $DB (a fresh DB needs no migration)" >&2
  exit 1
fi

BACKUP="${DB}.bak-$(date +%Y%m%d-%H%M%S)"
cp "$DB" "$BACKUP"
echo "Backed up $DB -> $BACKUP"

# Rebuild hera_role_status with the widened CHECK, preserving rows. SQLite cannot
# ALTER a CHECK, so this is the create-new / copy / drop / rename dance.
sqlite3 "$DB" <<'SQL'
PRAGMA foreign_keys=OFF;
BEGIN;
CREATE TABLE hera_role_status_new (
    role_id    INTEGER PRIMARY KEY REFERENCES hera_roles(id) ON DELETE CASCADE,
    status     TEXT NOT NULL CHECK (status IN ('idle','working','blocked','done','failed')),
    updated_at TEXT NOT NULL
);
INSERT INTO hera_role_status_new (role_id, status, updated_at)
    SELECT role_id, status, updated_at FROM hera_role_status;
DROP TABLE hera_role_status;
ALTER TABLE hera_role_status_new RENAME TO hera_role_status;
COMMIT;
PRAGMA foreign_keys=ON;
PRAGMA foreign_key_check;
SQL

# Verify the widened CHECK now accepts 'failed' (uses a transaction it rolls back,
# so no test row is left behind). Requires a role row to reference; skip if none.
if sqlite3 "$DB" "SELECT 1 FROM hera_roles LIMIT 1;" | grep -q 1; then
  RID="$(sqlite3 "$DB" "SELECT id FROM hera_roles LIMIT 1;")"
  sqlite3 "$DB" <<SQL
BEGIN;
INSERT INTO hera_role_status (role_id, status, updated_at)
  VALUES ($RID, 'failed', '1970-01-01T00:00:00Z')
  ON CONFLICT(role_id) DO UPDATE SET status='failed';
ROLLBACK;
SQL
  echo "Verified: 'failed' is now an accepted hera_role_status value."
else
  echo "Verified: table rebuilt (no hera_roles rows to exercise the CHECK against)."
fi

echo "Migration complete. If anything looks wrong, restore with: cp '$BACKUP' '$DB'"
