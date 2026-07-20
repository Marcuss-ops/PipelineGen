# scripts/ci/architecture/checks/all_checks/check_60_forbid_t_skip_without_honest_limitation.sh
#
# Layer-split (July 2026): extracted from monolithic
# scripts/ci/architecture/checks/all_checks/check_60_governance.sh
# (857 LOC) into 12 sourceable rule files.
#
# Rule 60: forbid t.Skip / t.Skipf / t.SkipNow without godlike/07
#                          honest-limitation comment (forward-prevention)
#                          in processor_persistence_test.go.
#
# Per godlike/06 SSOT: canonical forward-prevention gate for the
# zero-skip contract.
# Per godlike/07 NO-FAKE-AVAILABILITY: fails closed with a structured
# failure message + fix recipe.

# === Check 60: forbid t.Skip without godlike/07 honest-limitation comment (forward-prevention) ===
# PR-PR6-TEST-REACTIVATE (Wave 1 P0 #3, deadline 2026-07-15): the 2
# t.Skip markers in processor_persistence_test.go were removed in
# PR-PERSIST-PR6-CANONICAL. This gate bans NEW t.Skip(...) / t.Skipf(...)
# calls unless preceded (within a 25-line scroll window) by a
# `// godlike/07 honest-limitation` comment.
# Pattern: t\.Skip[a-zA-Z]*\( catches t.Skip(, t.Skipf(, t.SkipNow().
echo "Check 60 (forbid t.Skip without godlike/07 honest-limitation comment)"
skip_hits=$(rg -n 't\.Skip[a-zA-Z]*\(' internal/application/scripts/adapters/processor_persistence_test.go 2>/dev/null || true)
filtered_skip=""
if [ -n "$skip_hits" ]; then
  filtered_skip=$(echo "$skip_hits" | while IFS= read -r hit; do
    [ -z "$hit" ] && continue
    f=$(echo "$hit" | cut -d: -f1)
    l=$(echo "$hit" | cut -d: -f2)
    start=$((l - 25))
    [ "$start" -lt 1 ] && start=1
    end=$((l - 1))
    marker=$(sed -n "${start},${end}p" "$f" 2>/dev/null | grep -c "godlike/07 honest-limitation" || true)
    if [ "$marker" = "0" ]; then
      echo "$hit"
    fi
  done)
fi
fail_count=0
if [ -n "$filtered_skip" ]; then
  echo "  FAIL: t.Skip/t.Skipf/t.SkipNow calls in processor_persistence_test.go without" >&2
  echo "  a godlike/07 honest-limitation comment in the preceding 25 lines:" >&2
  echo "$filtered_skip" | sed 's/^/    /' >&2
  fail_count=$((fail_count + 1))
fi
if [ "$fail_count" -gt 0 ]; then
  echo "Check 60: FAIL (${fail_count} unjustified t.Skip hits)" >&2
  exit 1
fi
echo "Check 60: OK (0 unjustified t.Skip hits)"
