#!/usr/bin/env bash
# 50_jobs sub-check (verbatim-extracted section of the original monolithic
# scripts/ci/architecture/checks/50_jobs.sh — see
# scripts/ci/architecture/checks/lib/50_jobs_section_map.json for the
# byte-precise line range, and the lib/50_jobs_profile.sh for the
# analysis that produced this split). Do NOT hand-edit body to fix
# checks; edit the original 50_jobs.sh and re-run the splitter (or
# move body content out-of-line manually here with a corresponding
# orchestrator update).

if [ -n "${BASH_SOURCE[0]:-}" ]; then
    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
else
    echo "CI: cannot resolve sub-check directory from BASH_SOURCE[0]=" >&2
    exit 1
fi
# shellcheck source=/dev/null
source "${SCRIPT_DIR}/lib/50_jobs_lib.sh"

# ── Verbatim section body extracted from the original monolithic ────────
# ── Check 51: forbid raw-string Enqueue(...) callers + Dispatcher-tied callers (P0 C4, July 2026) ──
# The canonical job-routing entry point in production code is the typed
# Dispatcher.Enqueue(ctx, jobType, payload any) method (introduced in P0
# Commit 4). Any direct caller that passes a raw job-type string as the
# immediate second argument to .Enqueue(...) is a SSOT regression — the
# canonical surface is the typed-PayloadCodec encode + EnqueuePort
# delegation inside Dispatcher.Enqueue, not a hand-rolled Service.Enqueue
# call with a string-literal jobType.
#
# Two failure modes the gate enforces:
#
#   (a) RAW-STRING CALLERS: rb.grep matches .Enqueue(<ctx>, "<literal>")
#       where "<literal>" is an identifier-shaped string (lowercase + digits
#       + dots + underscores = the canonical job-type wire-shape). This
#       catches both Service.Enqueue(ctx, "script.generate", rawJSON) and
#       any future Service.Enqueue(<typed-envelope>, ...) shape that
#       accidentally introduces a string literal as the immediate 2nd
#       arg. Existing typed callers (e.g. Service.Enqueue(ctx, &enqReq))
#       are NOT matched because the 2nd arg is a struct literal, not a
#       string literal.
#
#   (b) RECEIVER TYPO / WRONG PORT: the canonical surface is
#       `*Dispatcher` (this package); service-level callers MUST go
#       via Service.Enqueue(... *EnqueueRequest) or Dispatcher.Enqueue(
#       ctx, jobType, payload). The gate keeps the explicit EnqueuePort
#       surface narrow: production code paths MUST NOT call Enqueue on
#       JobEnqueuer, JobBroker, JobEmittor, JobCreator-adapter, or any
#       custom-named Enqueue receivers.
#
# Pre-flight audit (June 2026, pre-C4): `rg -l 'Enqueue(\s*ctx[^)]*,\s*"[a-z._]+"'`
# returned ZERO hits — every existing production caller routes through a
# typed EnqueueRequest struct (not raw-string). The gate is
# forward-looking: catches future regressions rather than closing an
# active debt.
#
# Allowlist (the ONLY permitted .Enqueue( surfaces in production):
#   - internal/application/jobs/service.go          : *Service.Enqueue METHOD definition site
#   - internal/application/jobs/dispatcher.go        : *Dispatcher.Enqueue METHOD definition site (C4)
#   - internal/application/jobs/dispatcher_test.go   : *Dispatcher.Enqueue UNIT TEST (passes the
#                                                     canonical typed surface; the canonical-form
#                                                     strings in tests are intentional because they
#                                                     pin the canonical job-type wire format).
#   - *_test.go (all others)                        : tests may stub Enqueue however they need;
#                                                     the CI gate excludes *_test.go by default.
#   - internal/domain/job/service.go                : *EnqueueTyped top-level generic helper, no
#                                                     raw-string 2nd arg (always *EnqueueRequest).
#
# Pattern anchor: `\.Enqueue\s*\(\s*[^,]+,\s*"[a-z][a-zA-Z0-9._]*"`
# — case-insensitive NOT needed because canonical job-type strings are
# always lowercase (initial). Dots inside the string are tolerated
# (semantic-version-style "script.generate" / "media.curate" shapes).
# Anchored to lowercase initial so config strings ("Default", "default")
# are NOT falsely matched.
echo "=== Check 51: forbid raw-string Enqueue(...) callers (P0 C4, July 2026) ==="
raw_string_enqueues=$(rg -n --type go \
    -e '\.Enqueue\s*\(\s*[^,]+,\s*"[a-z][a-zA-Z0-9._]*"' \
    --glob '!**/internal/domain/job/service.go' \
    --glob '!**/*_test.go' \
    internal/ 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next   # drop full-line comments
        print
    }' \
    || true)
if [ -n "$raw_string_enqueues" ]; then
    echo "FAIL: raw-string Enqueue(<ctx>, \"<literal>\") caller found outside the canonical Dispatcher.Enqueue surface:"
    echo "$raw_string_enqueues"
    echo ""
    echo "Fix: route typed-payload Enqueue through Dispatcher.Enqueue(ctx, jobType, typedPayload) so"
    echo "      def.PayloadCodec.EncodePayload drives the wire-format. Direct Service.Enqueue(ctx,"
    echo "      &EnqueueRequest{Type: \"literal\"}) callers bypass the compiled registry and"
    echo "      silently lose codec + queue/timeout/retry metadata."
    echo ""
    echo "If the call site is genuinely a backfill (rare), wrap it as:"
    echo "    def, ok := compiled.Definition(\"<type>\")"
    echo "    if !ok { return job.ErrUnknownJobTypeRouted }"
    echo "    rawBytes, err := def.PayloadCodec.EncodePayload(payload)"
    echo "    return service.Enqueue(ctx, &EnqueueRequest{Type: def.Type, Payload: rawBytes})"
    exit 1
fi
echo "OK: no raw-string Enqueue(...) callers outside the canonical Dispatcher.Enqueue surface"
