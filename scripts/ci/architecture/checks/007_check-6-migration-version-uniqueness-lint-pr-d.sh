# ── Check 6: Migration version uniqueness lint (PR-D) ──────────────
# Fails when two or more files in `migrations/sqlite/` share the
# leading numeric version prefix. The canonical convention for this
# repo is one file per migration number; the slot ordering encodes the
# upgrade path, and a duplicate-prefix collision silently picks one
# candidate at runtime — historically observed as `069_*.sql` × 2 in
# the working tree (surface: composition-test panic at server startup).
#
# This lint catches the same pattern at pre-CI time so a new migration
# cannot land with a colliding slot.
#
# Implementation: list all migration files, project the prefix, then
# fail on any prefix that appears more than once. The regex
# `/^[0-9]+$/` (one or more digits) matches the canonical 3-digit slot
# AND any future widening (4-digit slot if a future numbering scheme
# requires it), while excluding vim backup files (`~001_foo.sql`),
# Emacs locks (`.#002_bar.sql`), and any other neighbour of a real
# migration that would otherwise look like a colliding slot.
migration_root="${MIGRATIONS_ROOT:-${REPO_ROOT}/migrations/sqlite}"
if [ -d "${migration_root}" ]; then
  dupes=$(ls -1 "${migration_root}/" 2>/dev/null \
    | awk -F_ '$1 ~ /^[0-9]+$/ {print $1}' \
    | sort \
    | uniq -d) || true
  if [ -n "${dupes}" ]; then
    echo "CI: duplicate migration version prefix(es) detected in ${migration_root}/:" >&2
    for v in ${dupes}; do
      echo >&2
      echo "  prefix ${v}:" >&2
      ls -1 "${migration_root}/${v}_"*.sql 2>/dev/null | sed 's|^|    |' >&2
    done
    echo >&2
    echo "Convention: one file per 3-digit version prefix." >&2
    echo "Resolve by renaming one of the colliding files to a free numeric slot." >&2
    exit 1
  fi
fi
echo "OK: no duplicate migration version prefixes in ${migration_root}/"

