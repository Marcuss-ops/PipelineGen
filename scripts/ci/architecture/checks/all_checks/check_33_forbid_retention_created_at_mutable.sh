# scripts/ci/architecture/checks/all_checks/check_33_forbid_retention_created_at_mutable.sh
#
# Layer-split (July 2026): extracted from monolithic
# scripts/ci/architecture/checks/all_checks/check_30_database.sh
# (170 LOC, 4 stacked rules).
#
# Rule 33: forbid retention:created_at:mutable SQL tag in jobs.
# Source-block: lines ~78-170 of check_30_database.sh (pre-split,
# most complex rule: env-gated fail-closed + 25-line allowlist).
#
# Per godlike/06 SSOT: jobs package is canonical owner of
# retention-immutability contract. TagWeaver sql-tag
# `retention:created_at:mutable` is the operator-side signal that
# the column has been declared mutable (forbidden in production).
#
# Per godlike/07 NO-FAKE-AVAILABILITY: env-gated
# fail-closed semantics — eventTimestampIsImmutable=true triggers
# exit 1 on hits, otherwise pass-through with INFO logging.

# ── Anti-bleed reset ──────────────────────────────────────────────
all_hits=""
literal_calls=""
hits_count="0"
literal_count="0"

# ── Check 33: forbid retention:created_at:mutable in sqlite/jobs ────
# The retention sweeper (lifecycle.go::NewRetentionSweeper) deletes
# aged-out outbox events by `created_at`. The canonical contract:
# created_at is IMMUTABLE — once an event is inserted, the timestamp
# MUST NOT be updated.
#
# Allowlist marker `// ARCH-ALLOWLIST: retention-created-at-mutable`
# on the line preceding the hit (25-line window) suppresses failure.
echo "=== Check 33: forbid retention:created_at:mutable in sqlite/jobs ==="
all_hits=$(rg -n --type go \
    -e 'retention:created_at:mutable' \
    --glob '!**/*_test.go' \
    internal/infrastructure/database/sqlite/jobs/ 2>/dev/null \
    || true)
literal_calls=$(printf '%s\n' "$all_hits" \
    | awk -F: '
        BEGIN { prev_marker = 0 }
        {
            rest = ""
            for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
            if (rest ~ /^[[:space:]]*\/\/.*ARCH-ALLOWLIST:[[:space:]]*retention-created-at-mutable/) {
                markers[$1] = (markers[$1] == "" ? $2 : markers[$1] "," $2)
                next
            }
            if (rest ~ /^[[:space:]]*\/\//) next
            n = (markers[$1] == "" ? 0 : split(markers[$1], mlist, ","))
            allowed = 0
            for (mi = 1; mi <= n; mi++) {
              m = mlist[mi] + 0
              if (m > 0 && $2 + 0 >= m + 1 && $2 + 0 <= m + 25) { allowed = 1; break }
            }
            if (allowed) next
            print
        }' \
    || true)
hits_count=${all_hits:+$(printf '%s' "$all_hits" | wc -l | awk '{print $1+0}')}
hits_count=${hits_count:-0}
literal_count=${literal_calls:+$(printf '%s' "$literal_calls" | wc -l | awk '{print $1+0}')}
literal_count=${literal_count:-0}
echo "INFO: retention:created_at:mutable scan in internal/infrastructure/database/sqlite/jobs/:"
echo "      total hits: ${hits_count}"
echo "      non-allowlisted hits: ${literal_count}"
if [ -n "$literal_calls" ]; then
    if [ "${eventTimestampIsImmutable:-}" = "true" ]; then
        echo "FAIL: retention:created_at:mutable annotation in production jobs package (eventTimestampIsImmutable=true):"
        echo "$literal_calls"
        exit 1
    else
        echo "INFO: eventTimestampIsImmutable!=true — non-allowlisted hits present but permitted (transitional pass-through):"
        echo "$literal_calls"
    fi
else
    if [ "${eventTimestampIsImmutable:-}" = "true" ]; then
        echo "OK: eventTimestampIsImmutable=true, 0 retention:created_at:mutable hits in production jobs package"
    else
        echo "OK: 0 retention:created_at:mutable hits in production jobs package (eventTimestampIsImmutable not set; gate is informational)"
    fi
fi
