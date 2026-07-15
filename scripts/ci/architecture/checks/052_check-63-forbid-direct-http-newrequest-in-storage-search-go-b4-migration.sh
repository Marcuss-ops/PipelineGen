# ── Check 63: forbid direct http.NewRequest in storage_search.go (B4 migration lock, July 2026) ───
# Forward-prevention for PR-IMAGES-AI-VS-NORMAL-PLAN B4 (July 2026): the
# canonical HTTP-GET single-call surface for storage_search.go is
# pkg/httpjson (GetJSON[T] / GetBytes). The pre-B4 inline
# http.NewRequest + Do + ReadAll copies (~180 LoC of boilerplate × 9
# sites) were collapsed into single-call sites per the explicit
# exit-gate documented on pkg/httpjson/get_json.go:16:
#   `rg "http.NewRequest" storage_search → 0`.
#
# This CI gate locks the post-B4 invariant: any new caller in
# storage_search.go that bypasses pkg/httpjson is a regression. The
# test-side counterpart (compile-time GetJSON pin + runtime scan
# inside the package's *_test.go suite) lives at
# internal/application/images/storage_search_contract_test.go.
#
# ARCH-ALLOWLIST opt-in: a future PR that, after due consideration,
# legitimately needs to call http.NewRequest directly in
# storage_search.go (e.g. a streaming upload that GetBytes cannot
# handle) MUST prepend the magic marker
# `// ARCH-ALLOWLIST: storage-search-httpreq` on the line preceding
# the call. The awk pre-pass strips such hits from the failing-set
# via the 25-line window tolerated by Checks 5 / 8 / 33 / 50 / 54.
# Per AGENTS.md §8 zero-baseline rule, every new allowlist entry
# requires explicit owner + deadline; the marker is the call-site
# equivalent of an allowlist row.
#
# Pattern anchors (ripgrep regex):
#   http\.NewRequest                  matches `http.NewRequest(` AND
#                                     `http.NewRequestWithContext(`.
# Mirrors the explicit migration exit-gate on pkg/httpjson's package godoc:
# the gate output is byte-stable with the godoc-documented target.
#
# Scope: strictly internal/application/images/storage_search.go.
# Widening to the entire package would over-block legitimate cross-
# layer composition wiring. The test-side companion
# scanPackageForHTTPNewRequest in storage_search_contract_test.go
# covers the wider package scope (any future split to
# storage_search_wikipedia.go + storage_search_searxng.go +
# storage_search_ddg.go stays regression-locked).
echo "=== Check 63: forbid http.NewRequest in storage_search.go (B4 lock, July 2026) ==="
all_hits=$(rg -n --type go \
    -e 'http\.NewRequest' \
    internal/application/images/storage_search.go 2>/dev/null \
    || true)
literal_calls=$(printf '%s\n' "$all_hits" \
    | awk -F: '
        {
            rest = ""
            for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
            if (rest ~ /^[[:space:]]*\/\/.*ARCH-ALLOWLIST:[[:space:]]*storage-search-httpreq/) {
                markers[$1] = (markers[$1] == "" ? $2 : markers[$1] "," $2)
                next
            }
            if (rest ~ /^[[:space:]]*\/\//) next   # drop full-line comments
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
if [ -n "$literal_calls" ]; then
    echo "FAIL: http.NewRequest detected in storage_search.go (B4 migration lock):"
    echo "$literal_calls"
    echo ""
    echo "Fix: route the call through pkg/httpjson.GetJSON[T] or pkg/httpjson.GetBytes"
    echo "(the canonical single-call surface post-B4). If the call is genuinely"
    echo "a streaming upload or other GetBytes-cannot-handle case, prepend an"
    echo "ARCH-ALLOWLIST marker on the line preceding the call:"
    echo "    // ARCH-ALLOWLIST: storage-search-httpreq"
    echo "    req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)"
    echo "The marker strips the hit from the failing-set via a 25-line window."
    exit 1
fi
echo "OK: 0 http.NewRequest calls in storage_search.go (B4 lock upheld)"

