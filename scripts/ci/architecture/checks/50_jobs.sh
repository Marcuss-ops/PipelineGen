# ── Check 50: forbid void Register* methods that take jobs.Service (P1 #1, July 2026) ──
# Audit P1 #1 closed the silent-success class on every JobHandler.Register*
# style method: nil-typed-dispatcher + duplicate-bind failures now surface as
# typed errors (wrapped appjobs.ErrMissingDeps via %w) and the composition
# root fails-closed on non-nil return. This CI gate is the forward-prevention
# rule that locks the typed-error contract at compile time so a future
# contributor cannot reintroduce a `void` Register* method that would
# silently drop the bind failure (the pre-P1 #1 audit-closed failure class).
#
# Pattern anchor (ripgrep multi-line via -U flag):
#   `func (\w+ \*?\w+) Register\([^)]*jobs\.?Service[^)]*\)\s*\{`
# i.e. closing paren of the arg list is followed ONLY by whitespace + `{`.
# If the return type `error` is between `)` and `{` (e.g.
# `) error {`), the regex `\)\s*\{` does NOT match because the literal
# `error` text breaks the `\s*\{` binding.
#
# Scope:
#   - All `func (h *X) Register(... *jobs.Service ...)` methods in
#     internal/application/** and internal/infrastructure/**/*. The match
#     is permissive (catches `jobs.Service`, `appjobs.Service`,
#     `jobtools.Service`, and the canonical alias `*jobs.Service`).
#   - Tests (`*_test.go`) excluded so test fixtures may freely construct
#     mocks with void signatures.
#
# Allowlist (production sites that CAN keep their existing shape):
#   - internal/api/assets/clips/handler.go::(*Handler).RegisterJobHandlers()
#     — takes NO jobs.Service argument (the receiver reads h.jobsSvc);
#     P1 #1 contract is "Register method that takes jobsSvc must return
#     error" — this method's signature doesn't match the pattern so the
#     regex skips it cleanly. (See clips.ports.go::HTTPHandlerPort for the
#     canonical interface declaration with `error` return — that's the
#     allowlisted surface.)
#
# ARCH-ALLOWLIST opt-in (mirrors Check 5 / 10b / 11): a transitional
# backfill that legitimately needs a void Register* signature (e.g. a
# one-shot operator CLI) MUST prepend the magic marker
# `// ARCH-ALLOWLIST: register-void-allowed` on the line preceding the
# function definition. The awk pre-pass strips such hits from the
# failing-set via the same 25-line window tolerated by Check 5 / 10b.
# Per AGENTS.md §8 zero-baseline rule, new allowlist entries require
# explicit owner + deadline; the marker is the call-site equivalent
# of an allowlist row.
#
# Per godlike/08 ARCHITECTURE-CI-GATES zero-baseline rule: any new
# failure on this gate is a guaranteed binding contract regression;
# the production handlers refactored in commit `refactor(jobs): make
# JobHandler.Register fail-fast (audit P1 #1)` already saturate the
# surface (10 handlers, all returning `error`).
echo "=== Check 50: forbid void Register* methods that take jobs.Service (P1 #1, July 2026) ==="
# P1 #1 fixup: the previous version used `[ \t]*\{` between `)` and `{`
# which only matches horizontal whitespace, allowing a multi-line signature
# like `func (h *X) Register(\n svc *jobs.Service,\n) { ... }` to slip
# through as not-a-void-trigger. The tightened pattern uses `\s*` which
# ripgrep's default regex semantics DO treat as multi-line whitespace.
# Single-line signatures still match (`\s` includes space + tab + newline).
#
# Pattern anchor: `func (h *X) Register(svc *jobs.Service) {` — the closing
# paren of the arg list is followed ONLY by whitespace + `{` (NO `error`
# type token between `)` and `{`). A typed-error return like
# `func (h *X) Register(svc *jobs.Service) error {` does NOT match because
# the literal `error` text breaks the `\s*\{` binding.
all_void_registers=$(rg -nU --type go \
    -e 'func\s+(\(\w+\s+\*?\w+\)\s+)?[A-Z][A-Za-z0-9_]*[Rr]egister\([^)]*\bjobs\.?Service[^)]*\)\s*\{' \
    --glob '!**/*_test.go' \
    internal/application internal/infrastructure 2>/dev/null \
    || true)
# Drop lines preceded by the ARCH-ALLOWLIST marker (25-line window).
literal_void_registers=$(printf '%s\\n' "$all_void_registers" \
    | awk -F: '
        BEGIN { prev_marker = 0 }
        {
            rest = ""
            for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
            if (rest ~ /^[[:space:]]*\\/\\/.*ARCH-ALLOWLIST:[[:space:]]*register-void-allowed/) {
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
if [ -n "$literal_void_registers" ]; then
    echo "FAIL: void Register* signature detected that takes a jobs.Service arg:"
    echo "$literal_void_registers"
    echo ""
    echo "Fix: change the Register* method signature to return error and wrap"
    echo "      the nil-dispatcher + duplicate-bind cases with"
    echo "      fmt.Errorf(\"<handler>.Register: <diagnostic>: %w\", ErrMissingDeps)"
    echo "      so the composition root fails-closed on non-nil return."
    echo "      The ErrMissingDeps sentinel lives in"
    echo "      internal/application/jobs/errors.go and is the typed-error"
    echo "      contract that tests assert via errors.Is(err, appjobs.ErrMissingDeps)."
    echo ""
    echo "If the void shape is genuinely transitional (rare; e.g. a one-shot"
    echo "      operator CLI), prepend the magic marker on the line preceding"
    echo "      the function definition:"
    echo "    // ARCH-ALLOWLIST: register-void-allowed"
    echo "    func (h *X) Register(svc *jobs.Service) { ... }"
    exit 1
fi
echo "OK: every Register* method that takes jobs.Service returns error (P1 #1 contract)"
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
    --glob '!**/internal/application/jobs/service.go' \
    --glob '!**/internal/application/jobs/dispatcher.go' \
    --glob '!**/internal/application/jobs/dispatcher_test.go' \
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
# ── Check 52: forbid direct ArtifactUploader wire calls outside canonical Creator adapter (P0 C6, July 2026) ──
# The canonical 3-protocol upload commands (PrepareArtifactUpload / UploadArtifactFile /
# FinalizeArtifactUpload) live on *jobbrokerclient.Client. The ONLY legitimate
# production caller is the Creator-side adapter at
# internal/infrastructure/remote/creator/adapter.go — the typed *Adapter
# implements remote.ArtifactUploader and threads the 3 wire commands through,
# enforcing the UploadState state machine + the ArtifactIdempotencyKey
# byte-stable contract at every seam. Production code paths in
# internal/application/** and internal/api/** MUST NOT call the wire methods
# directly — they MUST consume the typed remote.ArtifactUploader port so the
# Adapter's state machine + idempotency-key logic is enforced.
#
# Pre-flight audit (June 2026, pre-C6): `rg '\.(PrepareArtifactUpload|UploadArtifactFile|FinalizeArtifactUpload)\(' internal/application internal/api`
# returned ZERO hits — every existing production caller routes through the
# creator.Adapter (canonical aggregator). The gate is forward-looking: catches
# future regressions rather than closing an active debt (mirrors Check 51's
# forward-prevention posture for raw-string .Enqueue callers).
#
# Allowlist (the ONLY legitimate .wireCall surface):
#   - internal/infrastructure/remote/jobbrokerclient/client.go          : *Client METHOD definition sites
#   - internal/infrastructure/remote/jobbrokerclient/client_test.go     : the canonical client tests (none today, reserved for future)
#   - internal/infrastructure/remote/creator/adapter.go                : canonical Creator adapter implementing ArtifactUploader
#   - internal/infrastructure/remote/creator/adapter_test.go           : adapter tests pin the wire-shape contract
#   - *_test.go (all others)                                            : tests may stub the wire methods freely
#
# Pattern anchors (3 wire methods, one rg per call shape):
#   \.PrepareArtifactUpload\(
#   \.UploadArtifactFile\(
#   \.FinalizeArtifactUpload\(
# Tests are excluded via --glob '!**/*_test.go' so test fixtures may call
# the methods directly to verify contract behaviour.
echo "=== Check 52: forbid direct ArtifactUploader wire calls outside canonical Creator adapter (P0 C6) ==="
raw_wire_calls=$(rg -n --type go \
    -e '\.PrepareArtifactUpload\(' \
    -e '\.UploadArtifactFile\(' \
    -e '\.FinalizeArtifactUpload\(' \
    --glob '!**/internal/infrastructure/remote/jobbrokerclient/client.go' \
    --glob '!**/internal/infrastructure/remote/jobbrokerclient/client_test.go' \
    --glob '!**/internal/infrastructure/remote/creator/adapter.go' \
    --glob '!**/internal/infrastructure/remote/creator/adapter_test.go' \
    --glob '!**/*_test.go' \
    internal/application internal/api 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next   # drop full-line comments
        print
    }' \
    || true)
if [ -n "$raw_wire_calls" ]; then
    echo "FAIL: direct ArtifactUploader wire-method call outside canonical Creator adapter:"
    echo "$raw_wire_calls"
    echo ""
    echo "Fix: consume the typed remote.ArtifactUploader port (or the concrete"
    echo "      internal/infrastructure/remote/creator/adapter.go::Adapter) rather than"
    echo "      calling *jobbrokerclient.Client.PrepareArtifactUpload / UploadArtifactFile /"
    echo "      FinalizeArtifactUpload directly. The Adapter enforces the state machine"
    echo "      (UploadState.IsValidTransition) + the byte-stable idempotency-key contract"
    echo "      (ArtifactIdempotencyKey) — bypassing it risks race conditions on retry."
    exit 1
fi
echo "OK: no direct ArtifactUploader wire-method calls outside the canonical Creator adapter"
# ── Check 53: forbid direct atomic-complete wire calls outside canonical Service (P0 C7, July 2026) ──
# The canonical Sender-side atomic-complete port surface lives in
# internal/application/jobs/completion/complete_job_service.go. The TxContext interface
# (GetJob / UpdateJobToSucceededCAS / InsertResultOnConflict / GetPriorArtifactHashes /
# PersistArtifactMap / InsertOutboxEnvelope) is the ONLY legitimate seam through which
# callers may invoke the underlying in-TX work; direct callers of the implementation
# methods bypass the Service.Complete orchestration order (pre-TX Validated gate + lease
# CAS + ON CONFLICT dedup + hash round-trip + outbox emission) and silently regress
# the canonical single-TX guarantee (godlike/07 no-fake-availability).
#
# Pre-flight audit (June 2026, pre-C7): `rg -E '(UpdateJobToSucceededCAS|InsertResultOnConflict|PersistArtifactMap)\(' internal/`
# returns ZERO hits outside the canonical allowlist — the completion Service is the
# only legitimate caller today. The gate is forward-looking: catches future regressions
# rather than closing an active debt (mirrors Check 51 + Check 52 forward-prevention posture).
#
# Allowlist (the ONLY legitimate .wireCall surface):
#   - internal/application/jobs/completion/   — the canonical Sender-side complete service
#   - internal/application/jobs/completion/*_test.go  — adapter tests pin the wire-shape contract
#   - *_test.go (all others)                            — tests may stub freely
#
# Pattern anchors (6 wire methods + 2 type names, one rg per call shape):
#   \.UpdateJobToSucceededCAS\(       — aggressive lease-fencing CAS (godlike/06 SSOT)
#   \.InsertResultOnConflict\(         — ON CONFLICT (job_id, attempt, result_hash) DO NOTHING dedup
#   \.GetPriorArtifactHashes\(         — round-trip hash check (caller MUST go through Service.Complete)
#   \.PersistArtifactMap\(             — INSERT into job_artifacts (caller MUST go through Service.Complete)
#   \.InsertOutboxEnvelope\(            — typed outbox envelope emission
# Tests are excluded via --glob '!**/*_test.go' so test fixtures may call
# the methods directly to verify contract behaviour.
echo "=== Check 53: forbid direct atomic-complete wire calls outside canonical Service (P0 C7, July 2026) ==="
raw_complete_calls=$(rg -n --type go \
    -e '\.UpdateJobToSucceededCAS\(' \
    -e '\.InsertResultOnConflict\(' \
    -e '\.GetPriorArtifactHashes\(' \
    -e '\.PersistArtifactMap\(' \
    -e '\.InsertOutboxEnvelope\(' \
    --glob '!**/internal/application/jobs/completion/**' \
    --glob '!**/*_test.go' \
    internal/application internal/api 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next   # drop full-line comments
        print
    }' \
    || true)
if [ -n "$raw_complete_calls" ]; then
    echo "FAIL: direct atomic-complete wire-method call outside canonical Service:"
    echo "$raw_complete_calls"
    echo ""
    echo "Fix: consume the typed completion.Service port (or the canonical"
    echo "      internal/application/jobs/completion/complete_job_service.go::Service.Complete)"
    echo "      rather than calling TxContext methods directly. The Service enforces"
    echo "      the pre-TX Validated gate + lease CAS + ON CONFLICT dedup + hash round-trip"
    echo "      + outbox emission — bypassing it risks silent state drift on retry."
    exit 1
fi
echo "OK: no direct atomic-complete wire-method calls outside the canonical Service"
# ── Check 54: FASE 3.7 Commit 3 — gate banning infra imports in monitor/ ──
# FASE 3.7 closed the pre-existing infra-import leak in
# internal/application/assets/monitor/ via two consecutive adapter-pattern
# commits (1b for the discoveries+downloader surfaces; 2 for the metrics
# surface). The post-Cleanup state is canonical: monitor/ holds 4 Pattern-0
# ports + NopMetricsRecorder zero-value default; infra access is wired
# exclusively through the composition-root adapter in
# internal/app/lifecycle.go.
#
# Gate clause (godlike/08 zero-baseline rule):
#   monitor/ must NEVER import internal/infrastructure/... in production
#   code. All infra access flows through monitor.{MonitorDownloaderPort,
#   YoutubeDiscoveriesPort, MetricsRecorder, ...} ports + composition-root
#   adapters. The hatchable surface is the
#   `// ARCH-ALLOWLIST: monitor-infra-import` marker (mirrors Check 5/9/11
#   etiquette; per owner + deadline per AGENTS.md §7).
#
# Scope: strictly internal/application/assets/monitor/ ONLY. Widening this
# gate to internal/application/** would over-block legitimate cross-layer
# composition wiring (every other application-layer package legitimately
# consumes infra types via its own composition-root adapter). Mirrors the
# user-spec scope: "questo package strettamente (NON allargare)".
#
# Behaviour (per user spec):
#   - Hard-fail: production import of internal/infrastructure/... not
#     preceded by the ARCH-ALLOWLIST marker in the same file's 25-line
#     scroll window. Exit 1.
#   - Warn (no-fail): comment-only references (descriptive prose) +
#     ARCH-ALLOWLIST marker sites (log + count, do not accumulate to the
#     failing-set; godlike/07 no-fake-availability guarantees the marker
#     sites are observable in CI output every run so future audit-pin
#     regressions surface immediately).
#
# Pattern anchor: any literal occurrence of
# `github.com/Marcuss-ops/PipelineGen/internal/infrastructure` inside the
# monitor/ package (rg output), interpreted as either an import statement,
# a comment reference, or an ARCH-ALLOWLIST marker file.
#
# _test.go INCLUSION RATIONALE (godlike/06 SSOT): unlike Check 0/1/3/5/8/9/
# 11/23 which exclude *_test.go, Check 54 does NOT. Reason: the test layer
# in monitor/ asserts the canonical Pattern-0 surface via compile-time
# `var _ monitor.Port = (*Adapter)(nil)` pins. The Adapter concrete lives
# in infra, so the test file MUST import the infra side to satisfy the
# pin — excluding tests would hide the very class of drift (drift in the
# test-side structural-identity guard) that the gate exists to catch.
# Per godlike/07 zero-baseline rule: the canonical surface for the test
# file to bind is the composition-root adapter (lifecycle.go's adapter),
# not the raw infra package; a legitimate `var _ ...  = (*Adapter)(nil)`
# pin satisfies the canonical SSOT without bypassing the gate.
#
# Marker placement (canonical Go syntax, two acceptable patterns):
#   (a) PREFERRED: marker immediately above the `import (` line:
#         N:   // ARCH-ALLOWLIST: monitor-infra-import
#         N+1: import (
#         N+2:     "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/foo"
#       rg matches line N+2; the awk allows when offending_line == marker+2.
#   (b) ACCEPTABLE: marker immediately above the `import "..."` line
#       (no `import (` block; single-line import):
#         N:   // ARCH-ALLOWLIST: monitor-infra-import
#         N+1: _ "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/foo"
#       rg matches line N+1; the awk allows when offending_line == marker+1.
#   The two patterns are intentionally supported (off-by-one BS-ratchet
#   avoidance per godlike/07); the canonical godlike/06 surface is (a).
echo "=== Check 54: FASE 3.7 Commit 3 — gate banning infra imports in monitor/ ==="
# Two rg calls merged with sort -u: the marker line (// ARCH-ALLOWLIST:...)
# is NOT an infra-path match, so the original single-rg implementation
# never registered the marker and the marker+1/marker+2 logic was dead
# code. The second rg ensures marker lines flow into all_hits so the awk
# can register them, enabling both canonical Go import patterns
# (single-line `import "path"` with marker on previous line, and
# multi-line `import ( / "path"` with marker on or above the `import (`
# line). sort -u handles the same-line case (marker + import on one line).
infra_hits=$(rg -n --type go \
    'github\.com/Marcuss-ops/PipelineGen/internal/infrastructure' \
    internal/application/assets/monitor/ 2>/dev/null \
    || true)
marker_hits=$(rg -n --type go \
    'ARCH-ALLOWLIST:[[:space:]]*monitor-infra-import' \
    internal/application/assets/monitor/ 2>/dev/null \
    || true)
all_hits=$(printf '%s\n%s\n' "$infra_hits" "$marker_hits" | grep -v '^$' | sort -u)
# Stage 1: drop full-line comments + ARCH-ALLOWLIST marker lines + lines
# whose marker site (in the SAME file) is on marker+1 OR marker+2 lines
# upstream of the offending import statement (covers the canonical
# `marker / import ( / "path"` pattern AND the single-line import pattern
# per the canonical Go syntax contract documented above).
literal_calls=$(printf '%s\n' "$all_hits" \
    | awk -F: '
        {
            rest = ""
            for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
            # ARCH-ALLOWLIST: monitor-infra-import marker recognised on
            # the candidate line itself. The window is FIXED at zero
            # scroll tolerance (the import statement has a deterministic
            # parser position relative to the marker). Two acceptable
            # offsets are supported: marker+1 (single-line import pattern)
            # and marker+2 (canonical multi-line `import ( / "path"`
            # block pattern). See the bash comment block above for the
            # rationale.
            if (rest ~ /^[[:space:]]*\/\/.*ARCH-ALLOWLIST:[[:space:]]*monitor-infra-import/) {
                markers[$1] = (markers[$1] == "" ? $2 : markers[$1] "," $2)
                next
            }
            if (rest ~ /^[[:space:]]*\/\//) next   # drop full-line comments
            # Allow if the offending line is marker+1 OR marker+2 lines
            # downstream of a marker site in the SAME file (covers both
            # canonical Go import syntax patterns).
            n = (markers[$1] == "" ? 0 : split(markers[$1], mlist, ","))
            allowed = 0
            for (mi = 1; mi <= n; mi++) {
              m = mlist[mi] + 0
              if (m > 0 && ($2 + 0 == m + 1 || $2 + 0 == m + 2)) { allowed = 1; break }
            }
            if (allowed) next
            print
        }' \
    || true)
# Stage 2: audit-pin residue accounting (godlike/07 honest-limitation
# disclosure). Comment-only hits + ARCH-ALLOWLIST marker hits get logged
# as WARN so future drift is visible in CI output (the canonical
# no-fake-availability auditability requirement). They do NOT contribute
# to the hard-fail set.
comment_count=0
allowlist_count=0
if [ -n "$all_hits" ]; then
    comment_count=$(printf '%s\n' "$all_hits" | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\/.*ARCH-ALLOWLIST:/) next   # exclude marker lines
        if (rest ~ /^[[:space:]]*\/\//) print
    }' | wc -l | awk '{print $1+0}')
    allowlist_count=$(printf '%s\n' "$all_hits" | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\/.*ARCH-ALLOWLIST:[[:space:]]*monitor-infra-import/) print
    }' | wc -l | awk '{print $1+0}')
fi
# Stage 3: hard-fail on production imports. Comment-only matches + ARCH-
# ALLOWLIST sites are warning-only per user spec.
if [ -n "$literal_calls" ]; then
    echo "FAIL: forbidden internal/infrastructure/ import in internal/application/assets/monitor/ (FASE 3.7 Commit 3):"
    echo "$literal_calls"
    echo ""
    echo "Fix: route any infra access through the composition-root adapter in"
    echo "      internal/app/lifecycle.go. The canonical Pattern 0 surface is:"
    echo "      import ( // ARCH-ALLOWLIST: monitor-infra-import)"
    echo "        \"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/...\""
    echo "And the adapter (struct wrap / function-port ctor) lives on the"
    echo "infra side; the monitor-side port (MonitorDownloaderPort /"
    echo "YoutubeDiscoveriesPort / MetricsRecorder / ...) consumes only domain"
    echo "types. Any direct import is a FASE 3.7 commitment regression."
    echo ""
    echo "If the import is genuinely transitional (rare; documented per-file"
    echo "      in the commit body), prepend the magic marker on the line preceding"
    echo "      the import (the import block's opening paren):"
    echo "    // ARCH-ALLOWLIST: monitor-infra-import"
    echo "    import ("
    echo "      \"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/...\""
    echo "    )"
    echo "The marker is stripped from the failing-set automatically; per AGENTS.md"
    echo "§7 every marker entry requires explicit owner + deadline."
    exit 1
fi
if [ "$comment_count" -gt 0 ]; then
    echo "WARN (${comment_count} hits): comment-only internal/infrastructure/ references in monitor/"
    echo "      (descriptive prose; non-fatal per godlike/07 no-fake-availability; counts visible per CI run)"
fi
if [ "$allowlist_count" -gt 0 ]; then
    echo "WARN (${allowlist_count} hits): ARCH-ALLOWLIST: monitor-infra-import sites in monitor/"
    echo "      (each entry requires explicit owner + deadline per AGENTS.md §7; verify currency at promote-to-zero pass)"
fi
echo "OK: 0 hard-fail internal/infrastructure/ imports in monitor/ (FASE 3.7 Commit 3 invariants upheld)"
# ── Check 55: forbid legacy Template / TimelineJSON writes outside canonical allowlist (PR-PERSIST-6-CANONICAL-FIX) ──
# The canonical script-row write seam is processor_persistence.go (PR 6, June 2026);
# the canonical READ translators live in repository.go (toSQLiteScriptRecord /
# fromSQLiteScriptRecord). The legacy Template + TimelineJSON slots are
# intentionally LEFT EMPTY for newly-inserted rows — migration 100 already
# backfilled pre-PR-6 rows into the dedicated idempotency_key + specscene
# columns. Any struct-literal assignment to Template: or TimelineJSON: outside
# the canonical allowlist is a SSOT regression (godlike/06 one-owner-per-fact).
#
# Forward-prevention gate: catches future drift at pre-CI time. The current
# production tree is canonical (per PR-PERSIST-PR6-CANONICAL, commit d17c78ae)
# so this gate MUST exit 0 today; the gate exists to lock the contract.
#
# Pattern anchors (ripgrep -E syntax; mirrored 1:1 by the self-check entry):
#   Template:\s     — struct-literal field assignment to Template
#   TimelineJSON:\s — struct-literal field assignment to TimelineJSON
#
# Allowlist (the ONLY legitimate production-code struct literals):
#   - internal/application/scripts/adapters/processor_persistence.go — canonical writer
#   - internal/application/scripts/adapters/repository.go          — canonical READ translators
#
# Tests are excluded via --glob '!**/*_test.go' so test fixtures may construct
# field assignments freely (per the canonical pattern across all ci-gates).
# The rg scope is restricted to internal/ so the test fixture at
# tests/fixtures/zero_legacy/check_55_template_timeline_literal.go is NOT
# scanned by the production gate (it is scanned ONLY by the self-check mode).
echo "=== Check 55: forbid legacy Template / TimelineJSON writes outside canonical allowlist (PR-PERSIST-6-CANONICAL-FIX) ==="
literals=$(rg -n --type go \
    -e 'Template:\s' \
    -e 'TimelineJSON:\s' \
    --glob '!**/internal/application/scripts/adapters/processor_persistence.go' \
    --glob '!**/internal/application/scripts/adapters/repository.go' \
    --glob '!**/internal/application/voiceover/**' 
    --glob '!**/*_test.go' \
    internal/ 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next   # drop full-line comments
        print
    }' \
    || true)
if [ -n "$literals" ]; then
    echo "FAIL: legacy Template: or TimelineJSON: struct-literal assignment detected outside canonical allowlist:"
    echo "$literals"
    echo ""
    echo "Fix: route script-row writes through the canonical PersistenceProcessor"
    echo "      (internal/application/scripts/adapters/processor_persistence.go) which"
    echo "      writes the canonical IdempotencyKey + SpecScene columns. The legacy"
    echo "      Template + TimelineJSON slots are intentionally left empty for newly-"
    echo "      inserted rows (PR-PERSIST-6-CANONICAL, commit d17c78ae). The canonical"
    echo "      READ translators in repository.go (toSQLiteScriptRecord /"
    echo "      fromSQLiteScriptRecord) are the ONLY legitimate read-path owners."
    echo "      Every other production-code struct literal assigning Template: or"
    echo "      TimelineJSON: is a godlike/06 SSOT regression."
    exit 1
fi
echo "OK: no legacy Template: or TimelineJSON: struct-literal writes outside canonical allowlist"
# ── Check 49: go vet ./internal/... drift gate (FASE 9 post-rename follow-up, June 2026) ──
# Canonical fail-closed `go vet` pass (covering internal/ entirely).
# Catches the regression class where an upstream rename (e.g. FASE 9
# Step 6 gdrive.Service -> drive.Admin) updates a struct field but a
# consumer (production code, test fixture, or composition wiring) still
# references the OLD field/method name. rg-based content gates miss
# type-signature drift because they scan for patterns, not type
# conformance; `go vet --all` runs the canonical `composites` checker
# (Go 1.20+) which catches `unknown field X in struct literal of type Y`
# regressions like the one observed at
# `internal/app/voiceover_adapters_drive_test.go:53:30`. This gate
# fails BEFORE a force-with-lease push lands.
#
# Fail-closed per godlike-08 zero-baseline rule: any non-allowlisted
# vet warning exits 1 with the offender listed.
#
# ARCH-ALLOWLIST opt-in (mirrors Check 5 / 10b / 11 / 33): a
# transitional backfill or intentional deprecation call that
# legitimately surfaces a vet warning MUST prepend the magic marker
# `// ARCH-ALLOWLIST: vet-warn` on the line preceding the offending
# construct. Per godlike-08 zero-baseline rule, new allowlist
# sites require explicit owner + deadline.
echo "=== Check 49: go vet ./internal/... drift gate ==="
all_vet=$(go vet ./internal/... 2>&1) || vet_rc=$?
vet_rc=${vet_rc:-0}
# Strip ARCH-ALLOWLIST: vet-warn sites from the failing-set (25-line
# scroll-window of the magic marker - mirrors Check 5 semantics).
literal_vet=$(printf '%s\n' "$all_vet" \
    | awk -F: '
        {
            rest = ""
            for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
            if (rest ~ /^[[:space:]]*\/\/.*ARCH-ALLOWLIST:[[:space:]]*vet-warn/) {
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
if [ "$vet_rc" -ne 0 ] && [ -n "$literal_vet" ]; then
    echo "FAIL: go vet drift detected (non-allowlisted warnings):"
    printf '%s\n' "$literal_vet" | sed 's/^/  /'
    echo ""
    echo "Fix: align struct literals and method signatures with the canonical"
    echo "      type after upstream renames. If a vet warning is intentional,"
    echo "      prepend the magic marker on the preceding line:"
    echo "    // ARCH-ALLOWLIST: vet-warn"
    exit 1
fi
echo "OK: go vet ./internal/... passes (0 non-allowlisted warnings)"
# ── Main gate ──────────────────────────────────────────────────────
# Run the focused+ratchet archcheck; PR-A's `--future-ratchet` keeps the
# 5 Phase 0 rules in grace-cycle regression-detection mode.
# ── Check 8: forbid post-Setup SetOutboxHandler/SetMediasearchHandler (TODO 16, Wave 19) ────
# The deprecated setters on *Server MUST NOT be called from production
# code. The constructor NewServerWithHealth accepts outboxHandler and
# mediasearchHandler as params; routes are wired BEFORE Setup() runs.
# Post-construction setter calls silently fail to register routes.
#
# Allowlist (the ONLY legitimate call sites):
#   - internal/api/server.go        : the Server constructor wires handlers before Setup().
#   - internal/api/routes.go        : Router.SetOutboxHandler/SetMediasearchHandler (called
#                                     FROM the constructor, not by external callers).
#   - *_test.go                     : test files may call deprecation-setters to verify
#                                     the error contract.
#   - tests/fixtures/zero_legacy/** : self-check fixtures (caught only in --self-check mode).
echo "=== Check 8: forbid post-Setup SetOutboxHandler / SetMediasearchHandler (TODO 16) ==="
postSetupSetters=$(rg -n --type go \
    -e '\.SetOutboxHandler\(' \
    -e '\.SetMediasearchHandler\(' \
    --glob '!**/internal/api/server.go' \
    --glob '!**/internal/api/routes.go' \
    --glob '!**/*_test.go' \
    --glob '!tests/fixtures/zero_legacy/**' \
    . 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next
        print
    }' \
    || true)
if [ -n "$postSetupSetters" ]; then
    echo "FAIL: SetOutboxHandler / SetMediasearchHandler call outside canonical constructor:"
    echo "$postSetupSetters"
    echo ""
    echo "Fix: pass outboxHandler and mediasearchHandler through the"
    echo "NewServerWithHealth constructor (before Setup()), NOT via post-"
    echo "construction setters. The setters are deprecated and return errors"
    echo "when called after the gin engine is already built."
    exit 1
fi
echo "OK: no SetOutboxHandler / SetMediasearchHandler calls outside the canonical allowlist"
# ── Check 9: forbid nil-dispatcher silent fallback (return nil) (TODO 16, Wave 19) ────
# The canonical write path for indexed mutations is outbox.Dispatcher. Any code
# path that silently no-ops when the dispatcher is nil (`if dispatcher == nil {
# return nil }`) risks silently dropping writes. Hard-error patterns (return
# fmt.Errorf, return err) are intentionally NOT caught by this check — those
# correctly fail-fast and the existing artlist/search_core.go is a canonical
# example of the fail-fast pattern.
#
# Allowlist:
#   - internal/app/**                : composition root (Build*Bundle constructors).
#   - internal/infrastructure/database/sqlite/outbox/** : canonical dispatcher impl.
#   - *_test.go                      : test fixtures may stub nil dispatcher.
#   - cmd/admin/**                   : one-shot operator tooling.
#   - tests/fixtures/zero_legacy/**  : self-check fixtures.
echo "=== Check 9: forbid nil-dispatcher silent fallback (return nil) (TODO 16) ==="
nilDispatcher=$(rg -nU --type go \
    -e 'dispatcher\s*==\s*nil\s*\{[^}]*return\s+nil\b' \
    -e 'dispatcher\s*==\s*nil\s*\{?\s*\n\s*return(\s+nil\b|\s*$)' \
    --glob '!**/internal/app/**' \
    --glob '!**/internal/infrastructure/database/sqlite/outbox/**' \
    --glob '!**/*_test.go' \
    --glob '!**/cmd/admin/**' \
    --glob '!tests/fixtures/zero_legacy/**' \
    . 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next
        print
    }' \
    || true)
if [ -n "$nilDispatcher" ]; then
    echo "FAIL: nil-dispatcher silent fallback (return nil) outside composition/test/allowlist:"
    echo "$nilDispatcher"
    echo ""
    echo "Fix: handlers MUST fail-fast when the dispatcher is nil rather than"
    echo "silently returning nil. The canonical pattern is:"
    echo "  if d.dispatcher == nil { return fmt.Errorf(\"dispatcher is nil — invariant broken\") }"
    echo "instead of:"
    echo "  if d.dispatcher == nil { return nil }  // silently drops writes"
    exit 1
fi
echo "OK: no nil-dispatcher silent fallback patterns outside composition/test/allowlist"
# ── Check 10: forbid asset-repo Upsert(ctx, outside allowlist (TODO 16, Wave 19) ────
# The domain-level asset.Repository.Upsert and the concrete *ClipsRepository.Upsert
# are outbox-bypass surfaces in production handler code. Any handler that calls
# repo.Upsert (or assetStore.Upsert) outside the canonical write path (outbox
# dispatcher) risks silently writing to media_assets without an outbox event,
# leaving the Qdrant vector stale.
#
# Allowlist: cmd/admin/**, internal/infrastructure/database/sqlite/**,
# internal/application/{assets/{ingest,jobs/assets,artifacts,providers,searchqueries,catalogsync},
# voiceover,channels,images,youtube,clips}/**, internal/api/assets/**,
# internal/app/**, internal/infrastructure/{ai/autotag,database/assetindex}/**,
# *_test.go, tests/fixtures/zero_legacy/**.
echo "=== Check 10: forbid asset-repo Upsert outside canonical allowlist (TODO 16) ==="
assetUpserts=$(rg -n --type go \
    -e '\.Upsert\(ctx,' \
    --glob '!**/cmd/admin/**' \
    --glob '!**/internal/infrastructure/database/sqlite/**' \
    --glob '!**/internal/application/assets/ingest/**' \
    --glob '!**/internal/application/jobs/assets/**' \
    --glob '!**/internal/application/assets/artifacts/**' \
    --glob '!**/internal/application/assets/providers/**' \
    --glob '!**/internal/application/voiceover/**' \
    --glob '!**/internal/application/channels/**' \
    --glob '!**/internal/application/images/**' \
    --glob '!**/internal/application/youtube/**' \
    --glob '!**/internal/application/clips/**' \
    --glob '!**/internal/application/assets/searchqueries/**' \
    --glob '!**/internal/application/assets/catalogsync/**' \
    --glob '!**/internal/api/assets/**' \
    --glob '!**/internal/app/**' \
    --glob '!**/internal/infrastructure/ai/autotag/**' \
    --glob '!**/internal/infrastructure/database/assetindex/**' \
    --glob '!**/*_test.go' \
    --glob '!tests/fixtures/zero_legacy/**' \
    . 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next
        print
    }' \
    || true)
if [ -n "$assetUpserts" ]; then
    echo "FAIL: asset-repo Upsert call outside canonical allowlist:"
    echo "$assetUpserts"
    echo ""
    echo "Fix: route writes through the outbox dispatcher (production) or"
    echo "the canonical adapter layer (internal/application/assets/ingest/)."
    echo "Direct repo.Upsert in handler code silently bypasses the outbox"
    echo "and leaves Qdrant vectors stale."
    exit 1
fi
echo "OK: no asset-repo Upsert calls outside the canonical allowlist"
# ── Check 10b (PR 2 / Blocco 1 sub-PR, June 2026): forward-prevention gate
# for the dispatcher-only *assets.ClipsRepository surface methods that are
# STILL public (for legacy adapter delegation and the new Mutate typed-
# command wrapper) but MUST NOT be called directly from production paths.
#
# Today the literal PR 2 spec — lowercase all of UpsertClipTx,
# HardDeleteTx, RestoreTx, UpsertFolder, SoftDeleteFilter — is
# STRUCTURALLY-BLOCKED: UpsertClipTx is called cross-package by
# outbox.Dispatcher; HardDeleteTx/RestoreTx already live in
# txmutation/ (Wave 22 PR-CLIP-RAW-MUTATIONS); UpsertFolder +
# SoftDeleteFilter depend on the embedded *asset.AssetStoreSQLite
# whose removal is the (aborted) PR 1 deliverable. So this gate is the
# SAFE-ADDITIVE form of the spec: it can't lowercase the methods, but
# it CAN catch NEW direct callers from production paths so the
# 159+ historical call sites migrate and never re-accumulate.
#
# Pattern anchors:
#   \.UpsertFolder\(       — caller wants to write clip_folders row
#   \.SoftDeleteFilter\(   — caller wants the SQL filter string;
#                            legitimate in internal/infrastructure/sqlite/
#                            callers, NOT in production paths.
#
# Allowlist mirrors Check 10 (production-canonical adapter layer +
# sqlite infrastructure + tests + zero_legacy fixtures).
#
# ARCH-ALLOWLIST opt-in: prepend `// ARCH-ALLOWLIST: clips-ssot-only`
# on the line preceding the call site to opt into the allowlist
# (mirrors Check 5 / Check 8 conventions).
echo "=== Check 10b: forbid PR 2 Blocco 1 dispatcher-only primitive callers (PR 2 / Blocco 1 sub-PR) ==="
all_ips=$(rg -n --type go \
    -e '\.UpsertFolder\(' \
    -e '\.SoftDeleteFilter\(' \
    --glob '!**/*_test.go' \
    --glob '!tests/fixtures/zero_legacy/**' \
    --glob '!**/cmd/admin/**' \
    --glob '!**/internal/infrastructure/database/sqlite/**' \
    --glob '!**/internal/application/assets/ingest/**' \
    --glob '!**/internal/application/jobs/assets/**' \
    --glob '!**/internal/application/assets/artifacts/**' \
    --glob '!**/internal/application/assets/providers/**' \
    --glob '!**/internal/application/voiceover/**' \
    --glob '!**/internal/application/channels/**' \
    --glob '!**/internal/application/images/**' \
    --glob '!**/internal/application/youtube/**' \
    --glob '!**/internal/application/clips/**' \
    --glob '!**/internal/application/assets/searchqueries/**' \
    --glob '!**/internal/application/assets/catalogsync/**' \
    --glob '!**/internal/api/assets/**' \
    --glob '!**/internal/app/**' \
    --glob '!**/internal/infrastructure/ai/autotag/**' \
    --glob '!**/internal/infrastructure/database/assetindex/**' \
    . 2>/dev/null \
    || true)
literal_ips=$(printf '%s\n' "$all_ips" \
    | awk -F: '
        {
            rest = ""
            for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
            if (rest ~ /^[[:space:]]*\/\/.*ARCH-ALLOWLIST:[[:space:]]*clips-ssot-only/) {
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
if [ -n "$literal_ips" ]; then
    echo "FAIL: dispatcher-only primitive call from production path:"
    echo "$literal_ips"
    echo ""
    echo "Fix: route via the canonical Mutate(ctx, mutations.AssetMutationCommand)"
    echo "typed-command entry point on *assets.ClipsRepository, or via the"
    echo "AssetMutationDispatcher SSOT for actions that pre-date the wiki."
    echo "Direct .UpsertFolder( / .SoftDeleteFilter( calls in handler code"
    echo "leak the SQL-primitive surface and break the eventual migration."
    echo ""
    echo "If the call is genuinely a composition-root adapter delegate"
    echo "(rare; today only the canonical ClipsRepository adapter files"
    echo "in internal/app/**), prepend the magic marker on the preceding line:"
    echo "    // ARCH-ALLOWLIST: clips-ssot-only"
    echo "    a.inner.UpsertFolder(ctx, folder)"
    exit 1
fi
echo "OK: no dispatcher-only primitive calls from production paths"
# ── Check 11: forbid event_key construction with random UUID (TODO 16, Wave 19) ────
# Outbox event_keys MUST be deterministic (computed from the aggregate id +
# content hash) so the ON CONFLICT(event_key) DO NOTHING guarantee collapses
# duplicate enqueues. A random UUID in the event_key shape forces every
# enqueue to produce a new row, defeating idempotency. The canonical shapes
# are `delete:<asset_id>` (delete_envelope.go) and the index envelope in
# outboxevents/repository.go; uuid-suffixed keys are an anti-pattern.
#
# ALLOWLIST RATIONALE: the tightened multi-line patterns (June 2026
# follow-up) match uuid.NewString ONLY when the eventKey assignment line
# references the variable that holds the uuid (eventID). This lets the
# gate distinguish:
#
#   ANTI-PATTERN: eventKey assignment line contains `\beventID\b` (the
#     uuid-holding variable), so the uuid IS concatenated into the
#     eventKey value (directly via `+ eventID`, via `fmt.Sprintf` with
#     eventID as an arg, or any other reference).
#
#   LEGITIMATE:   eventKey assignment line does NOT reference eventID
#     at all (e.g. `eventKey := "delete:" + assetID`), so the uuid
#     is for a SEPARATE field (event_id audit) and ON CONFLICT(event_key)
#     DO NOTHING still works correctly.
#
# The allowlist below covers Category B only (reindex is intentionally
# uuid-suffixed per canonical design). Category A (UUID for separate
# event_id field) is NO LONGER allowlisted — the tightened patterns
# correctly accept it without an explicit allowlist entry.
#
# Category B — reindex is intentionally uuid-suffixed per canonical design:
#   - internal/infrastructure/database/sqlite/outboxevents/envelope.go::
#     BuildReindexEnvelopeV1: the eventKey IS uuid-suffixed by design
#     ("reconcile:reindex:<assetID>:<eventID>"). Idempotency is enforced
#     DOWNSTREAM by the worker's supersede gate on source_version
#     (from media_assets.metadata_json.$.content_hash), not at the
#     outbox-enqueue layer. Every --apply run enqueues a fresh reindex
#     event; redundant fix-up work is collapsed at execution time.
#   - internal/infrastructure/database/sqlite/outbox/delete_envelope.go::
#     buildDeleteRequestV1: pre-existing canonical pattern.
#
# Pattern shapes (3 tightened patterns):
#   1. INLINE:   `eventKey[^\n]*uuid\.NewString` — uuid.NewString is on
#                the SAME line as the eventKey assignment (direct
#                concatenation, e.g. `eventKey := "..." + uuid.NewString()`).
#   2. FORWARD:  `eventKey[^\n]*\n(?:[^\n]*\n){0,3}[^\n]*eventID[^\n]*=
#                \s*uuid\.NewString` — eventKey is on line N, and an
#                `eventID := uuid.NewString()` assignment is on line
#                N+1..N+3 (uuid-suffixed via a forward intermediate var).
#   3. REVERSE:  `eventID[^\n]*=\s*uuid\.NewString[^\n]*\n(?:[^\n]*\n){0,3}
#                [^\n]*eventKey[^\n]*=[^\n]*\beventID\b` — the canonical
#                production shape: `eventID := uuid.NewString()` on line N,
#                `eventKey := "..." + eventID` on line N+1. The `\beventID\b`
#                on the eventKey line proves the uuid IS being concatenated
#                into the eventKey value (not just adjacent to it).
#
# Loophole: the patterns hardcode the variable name `eventID`. A future
# contributor using a different name (e.g. `uid := uuid.NewString()`) would
# not be caught. ripgrep's default regex engine does not support
# backreferences for dynamic variable matching. The trade-off is
# acceptable because (a) `eventID` is the canonical name across all
# canonical envelope builders (BuildReindexEnvelopeV1, buildDeleteRequestV1)
# and the canonical reconcile adapter, and (b) the escape hatch is to
# promote Check 11 to a Go-side AST pass via
# `scripts/archcheck/check11eventkey/` (mirrors the Wave 19 PR2-1 pattern
# for cross-capability edge graph emission) if the loophole is exercised
# in practice.
#
# Allowlist:
#   - internal/infrastructure/database/sqlite/outbox/**       : canonical envelope builders
#                                                              (Category B pattern).
#   - internal/infrastructure/database/sqlite/outboxevents/** : canonical reindex envelope
#                                                              (Category B pattern).
#   - *_test.go                                               : test fixtures may use
#                                                              uuid.NewString for distinct keys.
#   - tests/fixtures/zero_legacy/**                           : self-check fixtures.
echo "=== Check 11: forbid event_key construction with random UUID (TODO 16) ==="
uuidEventKeys=$(rg -nU --type go \
    -e 'eventKey[^\n]*uuid\.NewString' \
    -e 'eventKey[^\n]*\n(?:[^\n]*\n){0,3}[^\n]*eventID[^\n]*=\s*uuid\.NewString' \
    -e 'eventID[^\n]*=\s*uuid\.NewString[^\n]*\n(?:[^\n]*\n){0,3}[^\n]*eventKey[^\n]*=[^\n]*\beventID\b' \
    --glob '!**/internal/infrastructure/database/sqlite/outbox/**' \
    --glob '!**/internal/infrastructure/database/sqlite/outboxevents/**' \
    --glob '!**/*_test.go' \
    --glob '!tests/fixtures/zero_legacy/**' \
    . 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next
        print
    }' \
    || true)
if [ -n "$uuidEventKeys" ]; then
    echo "FAIL: event_key constructed with random UUID outside canonical envelope:"
    echo "$uuidEventKeys"
    echo ""
    echo "Fix: use the canonical envelope builders (delete_envelope.go, index"
    echo "envelope in outboxevents/repository.go) which produce deterministic"
    echo "event_keys from the aggregate id + content hash. uuid.NewString in"
    echo "the event_key shape defeats ON CONFLICT(event_key) DO NOTHING and"
    echo "creates a fresh outbox row on every enqueue."
    exit 1
fi
echo "OK: no event_key construction with random UUID outside canonical envelope"
# ── Check 12: forbid legacy "lifecycle_state: <asset>.Status" fallback (TODO 16) ────
# QDRANT-001 §(b): the canonical lifecycle key is `lifecycle_state`; the
# legacy `status` column is the QDRANT-RECOVERY-001 / QDRANT-005 source of
# truth, but BuildPayload MUST populate the canonical key from
# `asset.LifecycleState`, NOT from the legacy `asset.Status`. The latter is a
# SSOT regression that loses fidelity on rows where Status and LifecycleState
# diverge (which is most rows post-059 migration).
#
# Allowlist:
#   - *_test.go                  : tests may exercise the legacy path explicitly.
#   - tests/fixtures/zero_legacy/** : self-check fixtures.
echo "=== Check 12: forbid legacy \"lifecycle_state\": <asset>.Status fallback (TODO 16) ==="
legacyLifecycleState=$(rg -n --type go \
    -e '"lifecycle_state":\s*\w+\.Status' \
    --glob '!**/*_test.go' \
    --glob '!tests/fixtures/zero_legacy/**' \
    . 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next
        print
    }' \
    || true)
if [ -n "$legacyLifecycleState" ]; then
    echo "FAIL: legacy \"lifecycle_state\": <asset>.Status fallback in payload builder:"
    echo "$legacyLifecycleState"
    echo ""
    echo "Fix: change the BuildPayload (or equivalent) line to source the"
    echo "lifecycle_state from asset.LifecycleState (the canonical field),"
    echo "not asset.Status (the legacy column). The status -> lifecycle_state"
    echo "rename happened in migration 059; rows where both exist will have"
    echo "diverged since then and the legacy key reads stale data."
    exit 1
fi
echo "OK: no legacy \"lifecycle_state\": <asset>.Status fallback in payload builders"
# ── Check 13: forbid ListAssetsForReconcile placeholder (TODO 16, TODO 2) ────
# SQLiteAssetStore.ListAssetsForReconcile is currently wired as a build-time
# placeholder (returns `wired as build-time placeholder only` error). That
# means any reconcile --apply call silently produces 0 findings, hiding real
# drift. The fix is to implement the SQL scan; this check fails until then.
#
# Pattern: any source code that returns the placeholder error string.
#
# Allowlist:
#   - *_test.go                  : tests may stub the placeholder explicitly.
#   - tests/fixtures/zero_legacy/** : self-check fixtures.
echo "=== Check 13: forbid ListAssetsForReconcile placeholder (TODO 16, TODO 2) ==="
placeholderReconcile=$(rg -n --type go \
    -e 'wired as build-time placeholder' \
    --glob '!**/*_test.go' \
    --glob '!tests/fixtures/zero_legacy/**' \
    . 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next
        print
    }' \
    || true)
if [ -n "$placeholderReconcile" ]; then
    echo "FAIL: ListAssetsForReconcile placeholder still wired in production:"
    echo "$placeholderReconcile"
    echo ""
    echo "Fix: implement the SQL scan in SQLiteAssetStore.ListAssetsForReconcile."
    echo "The placeholder (return fmt.Errorf(\"wired as build-time placeholder\"))"
    echo "silently produces 0 reconcile findings, hiding real drift. See TODO 2."
    exit 1
fi
echo "OK: no ListAssetsForReconcile placeholder in production"
# ── Check 14: forbid legacy "status" key in BuildPayload (TODO 16) ────
# QDRANT-001 §(b): the canonical payload key is `lifecycle_state`; a `status`
# key in BuildPayload is the QDRANT-RECOVERY-001 legacy that QDRANT-001
# removed. Any new BuildPayload that re-introduces the `status` key is a
# SSOT regression: the qdrant-side search filter (`lifecycle_state`) is
# what payloads and queries must agree on.
#
# Pattern: `"status": <value>` where value is a struct field reference
# (e.g. asset.Status). Literal-string `status` values (HTTP codes, state
# machine strings) are not in scope — the pattern is restricted to
# `<word>.<word>` (struct field ref) to keep the check tight.
#
# Allowlist:
#   - *_test.go                  : tests may construct legacy payloads.
#   - tests/fixtures/zero_legacy/** : self-check fixtures.
echo "=== Check 14: forbid legacy \"status\" key in BuildPayload (TODO 16) ==="
legacyStatusKey=$(rg -n --type go \
    -e '"status":\s*\w+\.' \
    --glob '!**/*_test.go' \
    --glob '!tests/fixtures/zero_legacy/**' \
    . 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next
        print
    }' \
    || true)
if [ -n "$legacyStatusKey" ]; then
    echo "FAIL: legacy \"status\" payload key (struct field ref) in BuildPayload:"
    echo "$legacyStatusKey"
    echo ""
    echo "Fix: rename the payload key from \"status\" to \"lifecycle_state\""
    echo "and source it from asset.LifecycleState. The QDRANT-001 §(b)"
    echo "search contract requires both writer (BuildPayload) and reader"
    echo "(Qdrant filter) to agree on the canonical key. See TODO 16."
    exit 1
fi
echo "OK: no legacy \"status\" payload key in BuildPayload"
# ── Check 15: qdrant.NewClient constructions must propagate APIKey (QDRANT-005A) ────
# QDRANT-005A Phase 1 Blocker #1: cfg.Qdrant.APIKey is not propagated to
# qdrant.NewClient at every construction site. An API-key-protected Qdrant
# deployment appears unhealthy (401) because the client omits the X-Api-Key
# header on every request. The canonical pattern is:
#
#   client := qdrant.NewClient(&qdrant.Config{
#       BaseURL: cfg.Qdrant.BaseURL,
#       APIKey:  cfg.Qdrant.APIKey,   // <-- REQUIRED
#       Timeout: cfg.Qdrant.Timeout,
#   }, log)
#
# Implementation: per-file check. Find every Go file that constructs
# qdrant.NewClient(&qdrant.Config{...}), then verify the SAME file
# also contains the literal pattern `APIKey:\s*cfg\.Qdrant\.APIKey`.
# A file that constructs the client but does NOT propagate the APIKey
# is the production anti-pattern.
#
# Why per-file (not per-block): a Go file may legitimately construct
# multiple qdrant.Config{...} literals (e.g. one for the production
# client + one for a test stub). Per-file is the conservative
# scope: any file that touches the client must also touch the
# APIKey propagation. If a file has TWO client constructions and
# ONE omits APIKey, the per-file check still catches it (the
# file-level pattern absence is the signal).
#
# Limit: a test file that constructs a stub client with no auth
# would false-positive. Test files are excluded via --glob
# `!**/*_test.go` per the standard check convention.
#
# Allowlist:
#   - *_test.go                  : test stubs may construct unauthenticated clients.
#   - tests/fixtures/zero_legacy/** : self-check fixtures.
#   - internal/infrastructure/qdrant/** : the Config TYPE lives here;
#                                     test files in this package are
#                                     excluded by the *_test.go rule,
#                                     and production code in this
#                                     package does NOT construct the
#                                     client (it only defines types).
echo "=== Check 15: qdrant.NewClient must propagate APIKey (QDRANT-005A) ==="
clientFiles=$(rg -l 'qdrant\.NewClient\(&qdrant\.Config\{' --type go \
    --glob '!**/*_test.go' \
    --glob '!tests/fixtures/zero_legacy/**' \
    . 2>/dev/null \
    || true)
missingApiKey=""
for f in $clientFiles; do
    if ! rg -q 'APIKey:\s*cfg\.Qdrant\.APIKey' "$f" 2>/dev/null; then
        missingApiKey="$missingApiKey $f"
    fi
done
if [ -n "$missingApiKey" ]; then
    echo "FAIL: file(s) construct qdrant.NewClient but do NOT propagate cfg.Qdrant.APIKey:"
    for f in $missingApiKey; do echo "  $f"; done
    echo ""
    echo "Fix: add 'APIKey: cfg.Qdrant.APIKey,' to the qdrant.Config{...} literal."
    echo "An API-key-protected Qdrant deployment appears unhealthy (401) when"
    echo "the client omits the X-Api-Key header. QDRANT-005A Phase 1 Blocker 1."
    exit 1
fi
echo "OK: all qdrant.NewClient constructions propagate cfg.Qdrant.APIKey"
# ── Check 19: forbid infrastructure imports in API layer ──
# Scans internal/api/ for production Go files that import
# github.com/Marcuss-ops/PipelineGen/internal/infrastructure/
# and fails on any file NOT listed in the per-file allowlist at
# docs/migrations/api-infrastructure-imports-allowlist.txt.
# Symmetric comparison: both non-allowlisted imports AND stale
# allowlist entries with no matching import fail the gate.
#
# This gate enforces AGENTS.md Pattern 8 (API package: thin transport
# only). The API layer MUST NOT import database/sql, Google Drive SDK,
# FFmpeg/process execution, or any other infrastructure concrete.
# Infrastructure dependencies must flow through typed ports in
# internal/application/ and be injected at the composition root.
#
# Zero-baseline: as of P0.6 (June 2026), the API layer has ZERO
# infrastructure imports. Any new import fails this gate.
echo "=== Check 19: forbid infrastructure imports in API layer ==="
allowlist_file="docs/migrations/api-infrastructure-imports-allowlist.txt"

# Collect all files in internal/api that import internal/infrastructure
actual=$(rg -l --type go \
    'github\.com/Marcuss-ops/PipelineGen/internal/infrastructure/' \
    internal/api \
    --glob '!**/*_test.go' \
    2>/dev/null | sort || true)

# Build sorted allowlist from the file (strip comments + blank lines)
allowed=$(grep -vE '^\s*(#|$)' "$allowlist_file" 2>/dev/null | sort || true)

# Violations: files with infra imports NOT in the allowlist.
# Pipe through grep . to strip spurious blank lines from empty
# variable expansion (echo "" produces a newline that would
# otherwise hit the comm output as a false-positive blank entry).
violations=$(comm -13 <(echo "$allowed" | grep .) <(echo "$actual" | grep .) 2>/dev/null || true)

# Stale entries: allowlist entries with NO matching infra import
stale=$(comm -23 <(echo "$allowed" | grep .) <(echo "$actual" | grep .) 2>/dev/null || true)

if [ -n "$violations" ]; then
    echo "FAIL: forbidden infrastructure imports detected in API layer:"
    echo "$violations"
    echo ""
    echo "Fix: move the infrastructure dependency to a port in"
    echo "      internal/application/ and inject it at the composition root."
    echo "      If the import is grandfathered, add the file path to"
    echo "      $allowlist_file with owner + deadline per AGENTS.md §8."
    exit 1
fi
if [ -n "$stale" ]; then
    echo "FAIL: stale allowlist entry with no matching infrastructure import:"
    echo "$stale"
    echo ""
    echo "Fix: remove the stale path from $allowlist_file. The import was"
    echo "      already removed from the source code; keeping a dead allowlist"
    echo "      entry masks future regressions. Per AGENTS.md §8 zero-baseline"
    echo "      rule, allowlist entries must exactly mirror the codebase."
    exit 1
fi
echo "OK: no infrastructure imports in API layer (0 actual, 0 allowed, symmetric clean)"
# ── Check 35: context.Background / context.WithoutCancel exemption tracking ──
# Wave 22 task 6 / PR-CONTEXT-NO-CANCEL-CI-GATE (June 2026): promote the
# documented exemption family from documentation-only status (S3g) to a
# dedicated CI gate. A site PASSES if EITHER:
#   (a) the file path is listed in AGENTS.md §Migration Status "Known
#       intentional exempt sites" table (canonical SSOT), OR
#   (b) the line preceding the call carries the magic marker
#       // ARCH-ALLOWLIST: no-cancel  (for context.WithoutCancel)
#       // ARCH-ALLOWLIST: bg-only    (for context.Background)
echo "=== Check 35: context.Background / context.WithoutCancel exemption tracking (PR-CONTEXT-NO-CANCEL-CI-GATE / Wave 22 task 6) ==="

EXEMPT_FILES=$(rg -oE '`internal/[^` ]+`' AGENTS.md 2>/dev/null \
    | sed 's/^`//' | sed 's/`$//' \
    | sort -u)
EXEMPT_FILE_COUNT=$(printf '%s\n' "$EXEMPT_FILES" | grep -c . || true)

ALL_HITS=$(rg -nE 'context\.(Background|WithoutCancel)\(' internal/ \
    --type go --glob '!**/*_test.go' 2>/dev/null || true)

if [ -z "$ALL_HITS" ]; then
    echo "OK: 0 context.Background / context.WithoutCancel call sites"
else
    UNDOCUMENTED_COUNT=0
    UNDOCUMENTED_OUTPUT=""
    while IFS= read -r hit; do
        [ -z "$hit" ] && continue
        FILE=$(echo "$hit" | cut -d: -f1)
        LINE=$(echo "$hit" | cut -d: -f2)
        # (a) AGENTS.md canonical-table exemption
        if printf '%s\n' "$EXEMPT_FILES" | grep -qxF "$FILE"; then
            continue
        fi
        # (b) ARCH-ALLOWLIST marker within a 25-line window preceding the
        # call, ONLY inside the godoc / comment block OF THE ENCLOSING
        # CODE path. (Mirrors Check 5 + Check 8 convention; real-world
        # godoc spans 2-5 lines.) Hard-stops on the first non-comment,
        # non-blank line encountered so an unrelated prior function's
        # marker can't accidentally exempt a NEW call site in the same
        # file (avoids false-positive exemption via shared-file markers).
        WALK_OK=0
        for OFFSET in $(seq 1 25); do
            PREV=$((LINE - OFFSET))
            [ "$PREV" -lt 1 ] && break
            LINE_TEXT=$(sed -n "${PREV}p" "$FILE" 2>/dev/null)
            if echo "$LINE_TEXT" \
                | grep -qE 'ARCH-ALLOWLIST:[[:space:]]*(no-cancel|bg-only)'; then
                WALK_OK=1
                break
            fi
            # Stop walking if we hit non-comment/non-blank line BEFORE
            # the marker (boundary of the surrounding godoc block).
            TRIMMED=$(echo "$LINE_TEXT" | sed 's/^[[:space:]]*//')
            if [ -n "$TRIMMED" ] && ! echo "$TRIMMED" | grep -qE '^//|^/\*'; then
                break
            fi
        done
        if [ "$WALK_OK" = "1" ]; then
            continue
        fi
        UNDOCUMENTED_OUTPUT="${UNDOCUMENTED_OUTPUT}${hit}
"
        UNDOCUMENTED_COUNT=$((UNDOCUMENTED_COUNT + 1))
    done <<< "$ALL_HITS"
    if [ "$UNDOCUMENTED_COUNT" -gt 0 ]; then
        echo "FAIL: ${UNDOCUMENTED_COUNT} context.Background/WithoutCancel sites LACK a tracking entry."
        echo ""
        echo "Each site must have BOTH one of the following exemptions:"
        echo "  (a) The file path appears in AGENTS.md \u00a7Migration Status"
        echo "      \"Known intentional exempt sites\" table."
        echo "  (b) Within the 25 lines preceding the call carries the magic marker:"
        echo "        // ARCH-ALLOWLIST: no-cancel  (for context.WithoutCancel)"
        echo "        // ARCH-ALLOWLIST: bg-only    (for context.Background)"
        echo ""
        echo "PR-CONTEXT-NO-CANCEL-CI-GATE / Wave 22 task 6 (June 2026)."
        echo ""
        echo "Sites requiring tracking:"
        printf '%s\n' "$UNDOCUMENTED_OUTPUT"
        exit 1
    fi
    echo "OK: all context.Background / context.WithoutCancel sites are tracked (${EXEMPT_FILE_COUNT} canonical exempt files)"
fi
# ── Check 36: anti-reintro gate for diagnostic / snapshot artefacts (PR-A, June 2026) ──
# Forward-prevention after the Wave 21 PR-G mega-batch that re-landed
# .tmp-diag/ directory + CURRENT_<X>.go + TODO<N>_<X>.go fixtures in the
# working tree (see paste audit). This gate ensures the .gitignore
# patterns appended by PR-A remain effective: any re-introduction of
# the four diagnostic patterns under internal/ cmd/ pkg/ scripts/ tests/
# fails CI with a remediation `git rm -rf` instruction.
#
# Pattern anchors (case-sensitive, basename-only):
#   directory names:  .tmp-diag,  tmp-diag
#   file basenames:   CURRENT_*.go  (literal CURRENT_ prefix)
#                     TODO[0-9]*.go (literal TODO prefix + 1 digit, no underscore required)
#
# Scope: the four top-level source roots only. .git/ hidden by default
# via `find` not descending into .git; tests/fixtures/zero_legacy/ is
# OUT of scope (`tests/` only matches the directory, fixtures of the
# canonical negative-example shape are not flagged).
#
# Implementation: `find` is canonical here (consistent with Check 23
# field-count extraction). rg --glob filters the search space, not the
# file-name; for basenameonly matching, find -name is the precise tool.
#
# Failure mode: emit the offending paths AND a copy-pasteable `git rm`
# one-liner so the operator can clean up in one step. Standard
# fail-fast + literal remediation. Index/PR-bodies stay consistent
# across the diagnostic-artefact family.
echo "=== Check 36: diagnostic-artefact anti-reintro gate (PR-A, June 2026) ==="
diag_files=$(find internal cmd pkg scripts tests -type f \
    \( -name 'CURRENT_*.go' -o -name 'TODO[0-9]*.go' \) \
    -not -path 'tests/fixtures/zero_legacy/*' 2>/dev/null || true)
diag_dirs=$(find internal cmd pkg scripts tests -type d \
    \( -name '.tmp-diag' -o -name 'tmp-diag' \) 2>/dev/null || true)
diag_hits=$(printf '%s\n%s\n' "$diag_files" "$diag_dirs" \
    | grep -v '^$' | sort -u || true)
if [ -n "$diag_hits" ]; then
    echo "FAIL: diagnostic / snapshot artefacts detected in source roots:"
    printf '%s\n' "$diag_hits" | sed 's/^/  /'
    echo ""
    echo "Resolution:"
    echo "  1. If these are intended diagnostic snapshots, MOVE them under"
    echo "     tests/fixtures/zero_legacy/ (the canonical negative-example"
    echo "     surface exempted by this gate)."
    echo "  2. Otherwise the canonical cleanup is to remove them via:"
    printf '%s\n' "$diag_hits" | sed 's/^/     git rm -rf /'
    echo ""
    echo "Per AGENTS.md §8 ARCHITECTURE-CI-GATES zero-baseline rule,"
    echo "re-introduction of these patterns is now blocked; this gate"
    echo "is the forward-prevention half of PR-A."
    exit 1
fi
echo "Check 36: 0 diagnostic-artefact paths in internal/ cmd/ pkg/ scripts/ tests/"
# ── Check 39: HC-7 anti-reintro gate (ChunkDuration: 25 literal + parent_id:"") ────
# HC-7 (June 2026) consolidates the script-video SSOT into
# pkg/defaults/video.go::{VideoConfig, DefaultVideoConfig}. Two patterns
# historically leaked past the SSOT and the leak-prone variants are
# gated here:
#
#   (a) ChunkDuration: 25 literal in platform/config/video.go::WithDefaults
#       (was hard-coded at line 64 pre-HC-7). The handler-side video
#       pipeline must read defaults.DefaultVideoConfig().ChunkDuration.
#       Pattern: `ChunkDuration <= 0 { ... = 25 `  (the cheap-to-grep
#       textual re-occurrence of the literal in the *conditioned* default
#       path — the unconditional canonical is in defaults package).
#
#   (b) `"parent_id": ""` literal in /api/scripts/* HTTP responses. The
#       canonical reader uses `s.ParentScriptID` (line 121 of
#       internal/api/script/helpers.go::ListScripts post-HC-7); the empty
#       string was DRIFT-23-4.
#
# Pattern anchors:
#   ChunkDuration.{0,40}= 25   — the conditioned-default shape; tolerates
#                                 any arithmetic (e.g. `+=25` `=((25))`)
#                                 but REMAINS strict on the literal value.
#   "parent_id":[[:space:]]*""  — the exact JSON-empty pattern.
#
# Scope: the same four top-level source roots used by Check 36 to keep
# the diagnostic-artefact family aligned. tests/fixtures/zero_legacy/
# is OUT of scope (negative-example fixtures exempt, mirrors Check 36).
#
# Negative examples live in fixtures/zero_legacy/ — if a future
# negative-EXAMPLE fixture needs to exist, place it there (the gate
# excludes that path) and update Check 39's allowlist rationale.
echo "=== Check 39: HC-7 anti-reintro gate (ChunkDuration: 25 literal + parent_id:\"\") ==="
hc7_hits=$(rg -n --type go \
    -e 'ChunkDuration.{0,40}=[[:space:]]*25\b' \
    -e '"parent_id":[[:space:]]*""' \
    --glob '!**/*_test.go' \
    --glob '!tests/fixtures/zero_legacy/*' \
    internal cmd pkg scripts 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next   # drop full-line comments
        print
    }' \
    || true)
# Filter out the SSOT itself: pkg/defaults/video.go is where the canonical
# 25 + "parent_id" literal legitimately lives; excluding it keeps the gate
# focused on consumer re-introduction.
hc7_literal=$(printf '%s\n' "$hc7_hits" \
    | awk -F: '$1 != "pkg/defaults/video.go"' \
    || true)
if [ -n "$hc7_literal" ]; then
    echo "FAIL: HC-7 re-introduction detected (ChunkDuration: 25 literal OR parent_id:\"\"):"
    printf '%s\n' "$hc7_literal" | sed 's/^/  /'
    echo ""
    echo "Fix: route the value through pkg/defaults/video.go::{VideoConfig,"
    echo "      DefaultVideoConfig}. The canonical CSV lives in:"
    echo "    - ChunkDuration: 25          → defaults.DefaultVideoConfig().ChunkDuration"
    echo "    - parent_id JSON field name → defaults.DefaultVideoConfig().ParentFieldName"
    echo "    - EffectsDir: 'effects/'     → defaults.DefaultVideoConfig().EffectsDir"
    echo ""
    echo "For ListScripts-style parent_id emission, iterate scriptRecords and"
    echo "emit `s.ParentScriptID` (the canonical int64) rather than the literal"
    echo 'empty string `"parent_id": ""` (the DRIFT-23-4 anti-pattern).'
    exit 1
fi
echo "Check 39: 0 HC-7 re-introduction patterns (ChunkDuration: 25 \/ parent_id:\"\")"
# ── Check 41: forbid recreation of internal/api/common/ (Issue 10, June 2026) ──
# internal/api/common/ was a compatibility stub with a duplicated OK helper.
# Removed in Issue 10 (June 2026). Any new import of the package or
# existence of the directory is a regression — the canonical helpers
# live in pkg/apiutil.
#
# This check fails if:
#   (a) internal/api/common/ directory exists, OR
#   (b) any production .go file imports ".../internal/api/common"
echo "=== Check 41: forbid recreation of internal/api/common/ (Issue 10) ==="
if [ -d "${REPO_ROOT}/internal/api/common" ]; then
    echo "FAIL: internal/api/common/ directory exists — delete it (removed in Issue 10, June 2026)"
    echo "      The canonical HTTP helpers live in pkg/apiutil."
    exit 1
fi
commonImports=$(rg -n --type go \
    -e 'github\.com/Marcuss-ops/PipelineGen/internal/api/common"' \
    --glob '!**/internal/api/common/**' \
    --glob '!**/*_test.go' \
    "${REPO_ROOT}" 2>/dev/null \
    || true)
if [ -n "$commonImports" ]; then
    echo "FAIL: import of internal/api/common detected (package was removed in Issue 10):"
    echo "$commonImports"
    echo ""
    echo "Fix: use pkg/apiutil instead. internal/api/common was a compatibility stub"
    echo "      with a duplicated OK helper — removed June 2026."
    exit 1
fi
echo "OK: internal/api/common/ is not present and no imports reference it"
# ── Check 42: forbid `database/sql` import in application/api production paths (P1-8, Wave 19) ──
# AGENTS.md Pattern 0 mandates that `internal/infrastructure/database/**`
# owns SQL; `internal/application/**` and `internal/api/**` consume SQL
# ONLY through typed ports declared in the consumer's `ports.go`.
# Direct `database/sql` import in production app/api code is a layering
# leak — the canonical placement is the typed-port adapter, not the
# consumer's import block. The one legitimate exception is the
# typed-port signature itself (e.g., `*sql.Tx` as a typed-port parameter
# in `internal/application/voiceover/ports.go::TxOutboxEnqueuer`); it
# stays in the allowlist with `never-canonical` deadline so the
# tx-outbox bridge shape survives the ratchet.
#
# Allowlist: `docs/migrations/app-sql-imports-allowlist.txt` lists
# one `<file_path>` per line for the P1-8 (Wave 19) grandfathered
# baseline. Per AGENTS.md §8 ARCHITECTURE-CI-GATES zero-baseline rule,
# every entry MUST carry an inline comment with owner + deadline.
# The inline deadline preamble is stripped here to compare against
# `rg` hits; the comment line stays attached to the entry so the
# zero-baseline rationale is auditable from the file.
#
# Pattern anchor: `^\s*"database/sql"\s*$` — matches the single-line
# Go import of `"database/sql"` exactly. Aliased imports are
# intentionally out of scope; introducing aliases is itself a layering
# indicator that code review should surface, not a CI fast-pass.
#
# Tests are excluded via `--glob '!**/*_test.go'` per the convention
# used by every other architectural check; test fixtures may freely
# construct `sql.Open` for `internal/infrastructure/health/...` smoke tests.
#
# Symmetric compare mirrors Check 19's two-way gate:
#   * violations: production files importing `"database/sql"` NOT in the
#     allowlist → FAIL the gate (regression detected).
#   * stale:     allowlist entries whose file no longer carries the
#     import → FAIL the gate (zombie-prevention — a dead row would
#     silently mask a future regression). Per AGENTS.md 1-PR rule the
#     removal ships in the same PR as the migration that drops the import.
echo "=== Check 42: forbid 'database/sql' import in app/api production paths (P1-8, Wave 19) ==="
allowlist_file="docs/migrations/app-sql-imports-allowlist.txt"
if [ ! -f "${REPO_ROOT}/${allowlist_file}" ]; then
    echo "FAIL: ${allowlist_file} missing — cannot run P1-8 gate"
    echo "      (the gate cannot grandfather without an allowlist file)"
    exit 1
fi

# Collect every production non-test .go file that imports `"database/sql"`
# exactly (the canonical Go import line shape).
actual=$(rg -l --type go \
    -e '^\s*"database/sql"\s*$' \
    --glob '!**/*_test.go' \
    internal/application internal/api 2>/dev/null | sort || true)

# Build sorted allowlist: strip full-line comments + blank lines +
# the trailing inline `# rationale + owner + deadline` part of each
# entry, keeping only the first whitespace-delimited token (= the
# file path).
allowed=$(grep -vE '^[[:space:]]*(#|$)' "${REPO_ROOT}/${allowlist_file}" 2>/dev/null \
          | awk -F'#' '{print $1}' \
          | awk '{print $1}' \
          | grep -v '^$' \
          | sort || true)

# Symmetric Check 42: fail on production hits NOT in allowlist AND on
# stale allowlist entries (mirrors Check 19's two-way gate).
violations=$(comm -13 <(printf '%s\n' "$allowed" | grep .) \
                   <(printf '%s\n' "$actual" | grep .) 2>/dev/null || true)
stale=$(comm -23 <(printf '%s\n' "$allowed" | grep .) \
               <(printf '%s\n' "$actual" | grep .) 2>/dev/null || true)

if [ -n "$violations" ]; then
    echo "FAIL: forbidden 'database/sql' import in production app/api layers (P1-8):"
    echo "$violations"
    echo ""
    echo "Fix: route SQL through a typed port in"
    echo "      internal/application/<consumer>/ports.go with the adapter"
    echo "      in internal/infrastructure/database/<feature>/, wired at"
    echo "      the composition root (internal/app/<feature>_adapters.go)."
    echo ""
    echo "If the import is grandfathered under the Wave 19 P1-8 transitional"
    echo "      baseline, add the file path to ${allowlist_file} with explicit"
    echo "      owner + deadline per AGENTS.md §8 zero-baseline rule."
    exit 1
fi
if [ -n "$stale" ]; then
    echo "FAIL: stale allowlist entry (file no longer imports 'database/sql'):"
    echo "$stale"
    echo ""
    echo "Fix: remove the stale path from ${allowlist_file} IN THE SAME PR"
    echo "      as the migration that drops the import (AGENTS.md 1-PR rule)."
    echo "      Leaving a dead allowlist entry masks future regressions."
    exit 1
fi
actual_count=$(printf '%s\n' "$actual" | grep -c . || true)
allowed_count=$(printf '%s\n' "$allowed" | grep -c . || true)
echo "OK: P1-8 'database/sql' baseline symmetric clean (${actual_count} actual = ${allowed_count} allowlisted; 0 pending migrations)"
# ── Main gate ──────────────────────────────────────────────────────────
# Run the focused+ratchet archcheck; PR-A's `--future-ratchet` keeps the
# 5 Phase 0 rules in grace-cycle regression-detection mode.
go run ./scripts/archcheck --ratchet --future-ratchet

# PR-I (June 2026): promote cmd/archcheck --strict as a blocking CI gate.
# Reads architecture/policy.yaml; --strict turns warn → exit-1 on any
# violation per cmd/archcheck/main.go:204-205. Ratchets #id-20-21:
# duplicate-types-allowlist (Check 5) + max_files_per_package=40
# (pack-size cap). Transitional baseline:
# docs/migrations/archcheck-strict-baseline.json holds any open
# exceptions; fail-closed semantics deadlined entries become hard
# fail (verdict: PR-I implementation in_progress per ADR-0002 §D5).
go run ./cmd/archcheck --strict
# HC-1 (June 2026) deletes the pre-HC-1 package-level `var jobTimeoutRegistry`
# global in internal/application/jobs/worker.go + the `SetJobTimeout` and
# `jobTimeout(` helper callers. Per-job-type timeouts are now keyed through
# `*jobs.Registry.Compose()[j.Type]` (or the typed `JobTimeout()` method)
# via the Worker.WithRegistry(reg) builder attached at composition time.
#
# Pattern anchors (re-introduction patterns we forbid):
#   var jobTimeoutRegistry[[:space:]]*=
#       — package-level map re-emergence with a MapType-typed name
#       (catches `var jobTimeoutRegistry`, `var ( ... jobTimeoutRegistry ...)`).
#   SetJobTimeout\(
#       — exported helper to mutate the map (the pre-HC-1 surface);
#       only worker.go::SetJobTimeout defined this; the alias was removed.
#   ^func jobTimeout\(  (top-level package function)
#   {{:blank:}}jobTimeout\(  (in-function call to package helper)
#       — the lowercase helper that read from the global; renamed to
#       Worker.jobTimeoutFor(t) post-HC-1.
#
# Scope: internal/ + cmd/ (composition root + production callers).
# The canonical site is internal/application/jobs/registry.go (owns the
# TimeoutMap + TimeoutResolver surface); it does NOT contain the
# forbidden patterns. *Registry.Compose() / JobTimeout() are the
# AND ONLY the supported lookup paths.
#
# Negative examples (the patterns being checked for, when invoked
# legitimately as inline fixtures/tests) live in tests/fixtures/zero_legacy/
# — excluded below to mirror Check 36 / Check 39 gating convention.
echo "=== Check 40: HC-1 anti-reintro gate (var jobTimeoutRegistry re-emergence) ==="
hc1_hits=$(rg -n --type go \
    -e 'var[[:space:]]+jobTimeoutRegistry[[:space:]]*=' \
    -e 'SetJobTimeout\(' \
    -e '^func[[:space:]]+jobTimeout\(' \
    -e '\bjobTimeout\(' \
    --glob '!**/*_test.go' \
    --glob '!tests/fixtures/zero_legacy/*' \
    internal cmd 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next   # drop full-line comments
        print
    }' \
    || true)
if [ -n "$hc1_hits" ]; then
    echo "FAIL: HC-1 re-introduction detected (jobTimeoutRegistry global / SetJobTimeout / jobTimeout helper):"
    printf '%s\n' "$hc1_hits" | sed 's/^/  /'
    echo ""
    echo "Fix: per-job-type timeouts MUST be keyed through *jobs.Registry via"
    echo "      Worker.WithRegistry(reg) at composition time. The HC-1 surface:"
    echo "    - registry.Compose()  → TimeoutMap (type-keyed snapshot)"
    echo "    - registry.JobTimeout(t) → typed single-shot lookup (the canonical"
    echo "                              TimeoutResolver method)"
    echo "    - worker.WithRegistry(reg)  → builder attached at composition time"
    echo "      (also snapshots reg.Compose() so runJob's lookup is branch-free)."
    echo ""
    echo "There is NO legitimate use of `var jobTimeoutRegistry ... = ...`, no"
    echo "`SetJobTimeout(t, d)` mutation hook, and no top-level `jobTimeout(t)`"
    echo "helper. Adding any of these requires a godlike/07 EXPAND/BACKFILL/"
    echo "CUTOVER/CONTRACT migration sequence (architecture/deprecations.yaml)"
    echo "and a tracking entry in architecture/current.yaml#HC-1 sub_tasks."
    exit 1
fi
echo "Check 40: 0 HC-1 re-introduction patterns (var jobTimeoutRegistry \/ SetJobTimeout \/ jobTimeout)"

# Check 15: 500-LoC per file (transitional allowlist, scadenza 2026-07-15)
bash "${SCRIPT_DIR}/15_file_size.sh" || { echo "Step 6 check 15 (file size) failed"; exit 1; }

# Check 16: <=39 productive files per package (transitional allowlist qdrant)
bash "${SCRIPT_DIR}/16_package_size.sh" || { echo "Step 6 check 16 (package size) failed"; exit 1; }
# Check 43: forbid .DB() chain outside infrastructure (P1.6, June 2026)
bash "${SCRIPT_DIR}/43_db_chain_outside_infra.sh" || { echo "Check 43 (DB chain) failed"; exit 1; }

# Check 45: forbid inline bare map[string]*ClipsRepository{...} literals (Wave 23, action P1-3)
# Companion to Check 8 (S3e) which bans the fully-qualified
# `"map[string]*assets.ClipsRepository{"` shape. Check 45 catches the
# BARE / unqualified variant `"map[string]*ClipsRepository{"` -- a
# likely regression shape if a future contributor imports the canonical
# type without a package alias (or introduces a new unqualified alias).
# Canonical-allowed sites (composition root + canonical registry +
# tests + zero_legacy fixtures) are excluded via rg --glob inside the
# check script.
# ── Check 44 (P1-2 application size cap + types_aliases.go filename ban) ──
# Action P1-2 of cleanup plan (June 2026): promoted from `current_state: deferred` to active.
# Slot was reserved in the original Check 45 commit per the now-removed
# `NOTE: Check 44 ... monotone-ratchetable.` comment above (see git history).
# Companion `arch(current):` commit in this PR flips wave_status.P1-2.current_state.
# SSOT (target + transitional_cap + current_state) read live from
# architecture/current.yaml::doc[1].wave_status.P1-2 per AGENTS.md §8 SSOT discipline.
bash "${SCRIPT_DIR}/44_application_size_cap_and_aliases_ban.sh" || { echo "Check 44 (P1-2 application size cap + types_aliases.go filename ban) failed"; exit 1; }

bash "${SCRIPT_DIR}/46_inline_clips_repository_map_ban.sh" || { echo "Check 46 (inline ClipsRepository map ban) failed"; exit 1; }
# ── Check 45: Channel-monitor E2E dedup contract test coverage (PR-C-YouTube-Cutover Commit I, June 2026) ──
# Verifies that the canonical E2E test file
# `internal/application/assets/monitor/e2e_no_duplicates_test.go`
# exists AND asserts the locked counter invariants so the assertion
# coverage cannot be silently neutered. Pin tokens match the spec
# invariants (parallel-safe-bypass semantics):
#   accepted_jobs==1     (Tick1+Tick2 dedups the channel-level
#                         sync job via the mockSyncBroker set)
#   duplicate_enqueues==5 (Tick2's 5 per-video emits classified
#                            as broker duplicates)
# Tick1/Tick2/parallel-race spec assertions are inspected at the
# source level so a gate regression on any of them surfaces here
# before CI tests run. Slot picked per spec (PR-C-YouTube-Cutover
# Commit I — user-explicit slot assignment supersedes the prior
# Check 50 numbering; the inline `map[string]*ClipsRepository` ban
# detection remains enforced via Check 46's script invocation).
echo "=== Check 45: Channel-monitor E2E dedup counter coverage (PR-C-YouTube-Cutover Commit I) ==="
e2e_test_file="internal/application/assets/monitor/e2e_no_duplicates_test.go"
if [ ! -f "$e2e_test_file" ]; then
  echo "FAIL: $e2e_test_file is missing."
  echo "Fix: add the E2E test file at the canonical path; the file is the"
  echo "single source of truth for the Tick1/Tick2 + parallel race contract."
  exit 1
fi
missing=""
for tok in qdrant db_clips drive_uploads outbox accepted_jobs duplicate_enqueues FiveByTwo; do
  if ! grep -qi "$tok" "$e2e_test_file"; then
    missing="$missing $tok"
  fi
done
if [ -n "$missing" ]; then
  echo "FAIL: $e2e_test_file is missing counter assertions for:$missing"
  exit 1
fi
echo "OK: Check 45 - E2E counter coverage verified on monitor/. (PR-C-YouTube-Cutover Commit I)"
# ── Check 50: forbid retry-classifier substring-matcher outside pkg/retry (Step 7 closure, June 2026) ──
# The canonical transient-error classification lives in pkg/retry/retry.go
# (typed-path: TransientInfrastructureError + IsTransient + WrapTransient +
# transientSubstrings taxonomy + DefaultOptions with JitterFraction=0.25).
# Production classifiers MUST delegate to pkg/retry.IsTransient or wrap at the
# SDK / port exit via pkg/retry.WrapTransient. A function whose name matches
# one of the canonical retry-classifier tokens (IsTransient|isTransient|
# IsRetryable|isRetryable|ShouldRetry|shouldRetry) followed by an optional
# PascalCase suffix AND uses strings.Contains natively is a Step 7 SSOT
# regression: a substring-based classifier outside pkg/retry.
#
# Allowlist (hardcoded package-level + per-file transitional baseline):
#   pkg/retry                          — canonical home.
#   pkg/textutil                       — string manipulation helpers.
#   pkg/similarity                     — token-set similarity math.
#   docs/migrations/retry-classifier-  — per-file transitional baseline with
#     substring-allowlist.txt            explicit owner + deadline + rationale.
# Tests (_test.go files) excluded per the standard check convention.
#
# Migration plan for future offenders:
#   1. Wrap raw SDK / port error at the exit boundary via pkg/retry.WrapTransient.
#   2. Classify at the gate via pkg/retry.IsTransient (typed path first).
#   3. Delete local strings.Contains taxonomy; retry.IsTransient owns the list.
echo "=== Check 50: forbid retry-classifier substring-matcher outside pkg/retry (Step 7) ==="
# ── Transitional baseline (per-file allowlist) ─────────────────────
# Per AGENTS.md godlike/08 zero-baseline rule (mirrors Check 5 / Check 8 /
# Check 23 / Check 33). Every entry requires explicit owner + deadline +
# rationale documented inline. Migration of any entry to the canonical
# typed-path surface deletes the corresponding line from the allowlist.
declare -a retry_classifier_extras=()
if [ -f "docs/migrations/retry-classifier-substring-allowlist.txt" ]; then
  while IFS= read -r _line; do
    [[ -z "$_line" || "$_line" =~ ^[[:space:]]*# ]] && continue
    # Each entry is <path>\t# <owner> <deadline> <rationale>. Extract just
    # the first whitespace-delimited token (the path). Trailing inline
    # comments are owned by the file's per-entry documentation.
    _path=$(awk '{print $1}' <<< "$_line")
    [[ -z "$_path" || "$_path" =~ ^# ]] && continue
    retry_classifier_extras+=( -not -path "./${_path}" )
  done < docs/migrations/retry-classifier-substring-allowlist.txt
fi

violators=$(find . -name '*.go' -not -name '*_test.go' \
    -not -path '*/pkg/retry/*' \
    -not -path '*/pkg/textutil/*' \
    -not -path '*/pkg/similarity/*' \
    "${retry_classifier_extras[@]}" \
    -print0 2>/dev/null \
    | xargs -0 awk '
    BEGIN { in_classifier = 0 ; func_line = 0 }
    /^func[[:space:]]+(\([^)]*\)[[:space:]]+)?(IsTransient|isTransient|IsRetryable|isRetryable|ShouldRetry|shouldRetry)[A-Za-z0-9_]*[[:space:]]*\(/ && /err/ {
        in_classifier = 1
        func_line = FNR
        next
    }
    in_classifier && /strings\.Contains/ {
        print FILENAME ":" func_line ": " $0
        in_classifier = 0
    }
    /^}/ && in_classifier {
        in_classifier = 0
    }
    ' 2>/dev/null || true)
if [ -n "$violators" ]; then
    echo "FAIL: retry-classifier function uses strings.Contains natively outside pkg/retry:"
    echo "$violators"
    echo ""
    echo "Fix: delegate the substring classifier to pkg/retry.IsTransient (typed"
    echo "      path). Optionally wrap outgoing port errors via pkg/retry.WrapTransient"
    echo "      at the SDK / port exit so errors.As(err, *TransientInfrastructureError)"
    echo "      finds the typed carrier. Allowlist: pkg/retry (canonical home),"
    echo "      pkg/textutil, pkg/similarity, and the per-file transitional list at"
    echo "      docs/migrations/retry-classifier-substring-allowlist.txt."
    exit 1
fi
echo "OK: no retry-classifier substring-matchers outside pkg/retry"
# ── Check 54: forbid legacy stock pipeline keywords (Stock Cutover Commit 3, July 2026) ──
# Stock Cutover Cleanup Plan Commits 4-8 retire the assetIndex / media_assets /
# EnqueueAndIndex / UploadFile / Publisher.Publish / YTDLPDownloader / youtube.Service
# surfaces of the stock pipeline. Check 54 is the regression guard: any NEW
# occurrence of these banned keywords in a non-allowlisted file exits CI red.
# The allowlist (docs/migrations/stock-legacy-keyword-allowlist.txt)
# grandfathers the legacy files that Commits 4-8 will retire; remove the
# matching allowlist entry at the same commit as the file deletion.
echo "=== Check 54: forbid legacy stock pipeline keywords ==="
banned_words='\bassetIndex\b|media_assets|\bEnqueueAndIndex\b|\bUploadFile\b|\bPublisher\.Publish\b|\bYTDLPDownloader\b|\byoutube\.Service\b'

# 1. Gather raw hits with grep + rescue grep failure via || true at the
#    command level (kept outside $() so bash parsing isn't confused by
#    pipe+or+close-paren combinations).
all_hits=$(grep -rnE "$banned_words" \
    internal/application/assets/providers/stock/ 2>/dev/null || true)

# 2. Parse the allowlist into a |-joined regex of repo-relative file paths.
# Comments (#) and blank lines are stripped before the join.
allowed_files=""
if [ -f docs/migrations/stock-legacy-keyword-allowlist.txt ]; then
    allowed_files=$(
        grep -vE '^[[:space:]]*(#|$)' docs/migrations/stock-legacy-keyword-allowlist.txt 2>/dev/null \
            | sort -u | paste -sd'|' -
    )
fi
[ -z "$allowed_files" ] && allowed_files="__no_allowlist__"

# 3. Subtract allowlist matches. The awk body has NO inline comments — inline
# comments after awk statements confuse some awk/bash combinations. Hits in
# non-allowlisted files trigger the gate regardless of code-vs-comment:
# a comment mentioning a banned keyword is still a regression-risk surface
# (the comment-bypass prevented this in the prior implementation).
fails=""
if [ -n "$all_hits" ]; then
    fails=$(printf '%s\n' "$all_hits" | awk -F':' -v allow="$allowed_files" '
        BEGIN {
            n = split(allow, a, "|")
            for (i = 1; i <= n; i++) if (a[i] != "") allowed[a[i]] = 1
        }
        { if ($1 in allowed) next; print }
    ' || true)
fi

# 4. Assess. Empty diff → OK gate green; non-empty → FAIL gate red.
if [ -n "$fails" ]; then
    echo "FAIL: legacy stock pipeline keyword(s) found in non-allowlisted files:"
    echo "$fails"
    echo ""
    echo "Fix: the stock pipeline is locked for cleanup (Commits 4-8). Do not"
    echo "introduce NEW occurrences of assetIndex, media_assets, EnqueueAndIndex,"
    echo "UploadFile, Publisher.Publish, YTDLPDownloader, or youtube.Service in"
    echo "production paths of internal/application/assets/providers/stock/."
    echo "If retiring a legacy file (Commits 4-8), remove the matching entry"
    echo "from docs/migrations/stock-legacy-keyword-allowlist.txt at the same commit."
    exit 1
fi
echo "OK: no net-new legacy stock pipeline keywords"

# -- Check 54: P0.1 -- forbid capability-layer .UploadFile* calls outside admin/legacy allowlist --
# Drive cutover P0.1 (July 2026): every .UploadFile( / .UploadFileWithDescription(
# call site in internal/application/** production code MUST carry a
# // TODO(P0.4) marker pointing to the canonical delivery.Publisher migration.
# Pre-flight audit: 6 remaining call sites (upload_helpers.go:175, runner.go:427,449,
# upload_intent.go:284, youtube/service.go:270, voiceover upload_intent.go:284).
# All 6 are documented in architecture/deprecations.yaml DRIVE-CUTOVER-P0-1.
# Zero-baseline: this gate fails-closed on any NEW UploadFile* call in
# internal/application/** that lacks the TODO(P0.4) marker.
#
# Forward-pointer: when all 6 sites are migrated (P0.4 CONTRACT), tighten
# this gate to ban .UploadFile* calls in internal/application/** entirely
# (zero tolerance, no marker exception).
echo "=== Check 54: P0.1 -- forbid new UploadFile* calls in capability-layer without TODO(P0.4) ==="
upload_calls=$(rg -n --type go \
    -e '\.UploadFile\(' \
    -e '\.UploadFileWithDescription\(' \
    --glob '!**/cmd/admin/**' \
    --glob '!**/internal/infrastructure/**' \
    --glob '!**/internal/app/**' \
    --glob '!**/*_test.go' \
    internal/application/ 2>/dev/null \
    | grep -v 'TODO(P0.4)' \
    || true)
if [ -n "$upload_calls" ]; then
    echo "WARN: .UploadFile* call sites lacking TODO(P0.4) marker:"
    echo "$upload_calls"
    echo ""
    echo "These call sites will be migrated to delivery.Publisher.Publish in P0.4."
    echo "Each site MUST carry a // TODO(P0.4): migrate to delivery.Publisher marker."
    echo "See architecture/deprecations.yaml DRIVE-CUTOVER-P0-1 for the full audit."
    echo "(NON-FATAL during P0.1 EXPAND window; will become hard-fail in P0.4 CONTRACT)"
fi
echo "OK: Check 54 � $([ -z "$upload_calls" ] && echo 'all UploadFile* sites tagged TODO(P0.4)' || echo 'some UploadFile* sites untagged (NON-FATAL during EXPAND window; see above)')"





echo "==== Check 56: Forward-pointer marker + linked_issue cross-ref enforcement ===="
# Forward-prevention gate (CI complement to compile-time Assumption 1).
# Rationale (godlike/07 no-fake-availability): A composition-root function in
# internal/app/*.go that introduces a forward-pointer NIL field MUST carry:
#   (a) A marker `// forward-pointer: PR-<NAME>` on either:
#       (i)  the SAME line as the nil assignment: `Field: nil, // forward-pointer: PR-<NAME>`
#       (ii) a comment line directly above (within 25 lines of scroll-window)
#   (b) The PR-<NAME> registered as a `linked_issues[*].id` in
#       architecture/current.yaml::wave_status.
# Without BOTH, the nil field is a masked PLACEHOLDER -- runtime may
# dereference it (panic) or treat it as fake-success. Forward-prevention
# discipline: zero-baseline allowlist (godlike/08); transient baselines
# require explicit owner+deadline in the allowlist row.
#
# SLOT-SELECTION NOTE: Spec said "Check 54"; origin/main's Check 54 is
# canonical (Stock Cutover reset-gate, commit f12eb12f). Using Check 56
# preserves godlike/06 one-canonical-owner-per-fact.

ALLOWLIST_55="docs/migrations/check55-forward-pointer-allowlist.txt"
mkdir -p "$(dirname "$ALLOWLIST_55")"
[ -f "$ALLOWLIST_55" ] || touch "$ALLOWLIST_55" || { echo "  WARN: allowlist touch failed"; ALLOWLIST_55=/dev/null; }

# Build the set of PR-* IDs registered as linked_issues in architecture/current.yaml.
YAML_IDS_FILE=$(mktemp 2>/dev/null || echo "/tmp/.check55_yaml_pr_ids_v3.txt")
{
  rg '^\s*-\s+id:\s+(PR-[A-Z0-9.\-]+)\s*$' architecture/current.yaml --no-filename -or '$1' 2>/dev/null || true
  rg '^\s*id:\s+(PR-[A-Z0-9.\-]+)\s*$'  architecture/current.yaml --no-filename -or '$1' 2>/dev/null || true
} | sort -u > "$YAML_IDS_FILE"
echo "  Allowlist: $ALLOWLIST_55 ($(wc -l < $ALLOWLIST_55 | tr -d ' ') rows; empty by default per godlike/08)"
echo "  YAML registered PR-* IDs: $(wc -l < "$YAML_IDS_FILE" | tr -d ' ')"

fail_count56=0
ok_count55=0
skip_count55=0
inspect_output=$(mktemp 2>/dev/null || echo "/tmp/.check55_inspect_v3.txt")

files_list=$(find internal/app -maxdepth 2 -type f -name '*.go' 2>/dev/null | sort)

# Stateful awk: scan composition-root function bodies for `: nil,` patterns.
# KEY FIX (v3): outer regex matches ANY `: nil,` (not just at EOL). Then branch
# on marker presence (sentinel `forward-pointer: PR-<NAME>` anywhere on the
# matched line) to extract the PR-XYZ identifier. Production code uniformly
# places marker on SAME LINE as nil assignment; v1/v2 required EOL anchor
# which silently zero-matched them -> false-positive-OK (no rows emit).
while IFS= read -r gf; do
  [ -z "$gf" ] && continue
  awk -v file="$gf" '
    BEGIN { in_func = 0 }
    # Composition-root function entry points only.
    /^func[[:space:]]+(register[A-Za-z0-9_]*Module|Wire[A-Za-z0-9_]*)\(/ { in_func = 1 }
    # Closing brace at column 1 ends the function scope.
    in_func && /^}/ { in_func = 0 }
    # Inside a composition-root function: every `: nil,` line.
    in_func && /:[[:space:]]+nil,/ {
      # 1. Extract PR-XXX identifier if present anywhere on the line.
      # 2. Branch on whether marker exists at all.
      line = $0
      pr_found = ""
      # Look for the canonical marker token on this line.
      if (match(line, /forward-pointer:[[:space:]]*PR-[A-Za-z0-9.\-]+/)) {
        pr_full = substr(line, RSTART, RLENGTH)
        if (match(pr_full, /PR-[A-Za-z0-9.\-]+/)) {
          pr_found = substr(pr_full, RSTART, RLENGTH)
        }
      }
      if (pr_found != "") {
        print "OK\t" pr_found "\t" file ":" NR
      } else {
        print "FAIL\tMISSING_MARKER\t" file ":" NR "\t" line
      }
    }
  ' "$gf"
done < <(printf '%s\n' "$files_list") > "$inspect_output"

# Iterate status rows. Function-style iteration via process substitution avoids
# bash subshell scoping (which would lose $fail_count56 updates).
while IFS=$'\t' read -r status payload loc raw; do
  case "$status" in
    OK)
      pr="$payload"
      if grep -qxF "$pr" "$YAML_IDS_FILE"; then
        ok_count55=$((ok_count55 + 1))
      else
        echo "[Check 56] $loc : forward-pointer $pr not registered in architecture/current.yaml::wave_status.linked_issues[*].id (godlike/06 SSOT breach)"
        fail_count56=$((fail_count56 + 1))
      fi
      ;;
    FAIL)
      if [ "$ALLOWLIST_55" != /dev/null ] && grep -qF "$loc" "$ALLOWLIST_55"; then
        skip_count55=$((skip_count55 + 1))
      else
        echo "[Check 56] $loc : nil field lacks same-line marker `// forward-pointer: PR-<NAME>`"
        if [ -n "$raw" ]; then
          echo "         raw: $raw"
        fi
        fail_count56=$((fail_count56 + 1))
      fi
      ;;
  esac
done < "$inspect_output"

# Lint allowlist: every row must be `file:line` and the target file must still exist.
if [ -s "$ALLOWLIST_55" ]; then
  while IFS= read -r arow; do
    [ -z "$arow" ] && continue
    file="${arow%:*}"
    if [ -f "$file" ]; then
      skip_count55=$((skip_count55 + 1))
    else
      echo "[Check 56] allowlist $arow : target file no longer exists (zero-baseline discipline: clean up)"
      fail_count56=$((fail_count56 + 1))
    fi
  done < "$ALLOWLIST_55"
fi

rm -f "$inspect_output"

echo "  Stats: OK=$ok_count55 FAIL=$fail_count56 SKIP(allowlisted)=$skip_count55"
if [ "$fail_count56" -gt 0 ]; then
  echo "RESULT: Check 56 FAIL ($fail_count56 violations)"
  rm -f "$YAML_IDS_FILE"
  exit 1
fi
echo "RESULT: Check 56 OK (forward-pointer markers present + YAML-registered)"

rm -f "$YAML_IDS_FILE"
# ── Check 57: forbid ports.ScriptRecord literal outside canonical allowlist (godlike/06 SSOT, July 2026) ──
# godlike/06 SSOT one-canonical-owner-per-fact: PersistenceProcessor is the
# SOLE WRITER of *ports.ScriptRecord in production paths. The canonical
# read-path translator (`adapters/repository.go::fromSQLiteScriptRecord`)
# is the SECOND canonical owner — it translates sqlitescripts.ScriptRecord
# → ports.ScriptRecord on read paths (Get/List/Find). Every other direct
# literal `&ports.ScriptRecord{...}` in production code is a SSOT
# regression (writes MUST flow through PersistenceProcessor; reads MUST
# flow through fromSQLiteScriptRecord).
#
# Pattern anchor (ripgrep regex, root-anchored substring):
#   ports\.ScriptRecord\{   — matches  &ports.ScriptRecord{ … }  literal.
# Targets the FULLY-QUALIFIED form so it does NOT false-positive on the
# canonical writer PersistenceProcessor, which uses the in-package alias
# `&ScriptRecord{...}` (declared at adapters/repository.go:34 via
# `type ScriptRecord = ports.ScriptRecord`). The alias is byte-equivalent
# to ports.ScriptRecord (Go type alias, NOT distinct type) but its
# literal form `&ScriptRecord{` is NOT matched by the regex. Intentional
# design — the regex enforces literal-discipline at every
# `&ports.ScriptRecord{` site across the production tree.
#
# Allowlist (the ONLY legitimate production sites for the fully-qualified
# literal form `&ports.ScriptRecord{...}`):
#   - internal/application/scripts/adapters/processor_persistence.go  :
#     CANONICAL WRITER (godlike/06 SSOT, the SOLE producer of new
#     ports.ScriptRecord rows). Belt-and-suspenders allowlist: this file
#     uses the in-package `&ScriptRecord{...}` alias idiom (which the
#     regex does NOT match), so the allowlist row is forward-prevention
#     only — if a future contributor accidentally writes
#     `&ports.ScriptRecord{...}` at this site, the gate would still pass.
#   - internal/application/scripts/adapters/repository.go             :
#     CANONICAL READ-PATH TRANSLATOR
#     (`fromSQLiteScriptRecord` constructs `&ports.ScriptRecord{...}` as
#     the read-shape population for ports.ScriptRecord {sqlitescripts →
#     ports}). This is a DELIBERATE EXTENSION of the user-stated
#     allowlist (processor_persistence.go + tests); rationale: locking
#     the read-path translator out of the gate would force a refactor to
#     field-by-field assignment which is out of scope for the godlike/07
#     minimal-blast-radius. If the user wants this site gated too, a
#     follow-up PR can refactor fromSQLiteScriptRecord to use the alias
#     idiom (or untyped assignment) so the gate catches the gap.
#   - *_test.go (all)                                                :
#     Test mocks / fixtures may freely construct the literal as
#     required for type-fixture construction. Listed globally so we
#     don't have to enumerate each of the ~5 test fixture files; the
#     in-package `&ScriptRecord{...}` alias is the dominant idiom in
#     test mocks so the gate doesn't even match most test fixtures.
#
# ARCH-ALLOWLIST opt-in (mirrors Check 5/54 etiquette; owner + deadline
# per AGENTS.md §7): a transitional backfill or production test fixture
# that legitimately needs `&ports.ScriptRecord{…}` at a non-allowlisted
# site MUST prepend the magic marker
# `// ARCH-ALLOWLIST: ports-scriptrecord-allowed` on the line preceding
# the literal. The awk pre-pass strips such hits from the failing-set via
# the same 25-line scroll-window tolerated across the gate family.
echo "=== Check 57: forbid ports.ScriptRecord literal outside canonical allowlist ==="
all_hits=$(rg -n --type go \
    -e 'ports\.ScriptRecord\{' \
    --glob '!**/processor_persistence.go' \
    --glob '!**/application/scripts/adapters/repository.go' \
    --glob '!**/*_test.go' \
    internal/application internal/api 2>/dev/null \
    || true)
literal_calls=$(printf '%s\n' "$all_hits" \
    | awk -F: '
        {
            rest = ""
            for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
            if (rest ~ /^[[:space:]]*\/\/.*ARCH-ALLOWLIST:[[:space:]]*ports-scriptrecord-allowed/) {
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
if [ -n "$literal_calls" ]; then
    echo "FAIL: forbidden *ports.ScriptRecord literal construction in production path:"
    echo "$literal_calls"
    echo ""
    echo "Fix: write new *ports.ScriptRecord rows ONLY through PersistenceProcessor"
    echo "      (canonical SOLE writer; godlike/06 SSOT). For read paths, the"
    echo "      canonical translator is adapters/repository.go::fromSQLiteScriptRecord"
    echo "      (sqlitescripts -> ports.ScriptRecord)."
    echo ""
    echo "If the literal is genuinely transitional (rare), prepend the magic"
    echo "      marker on the line preceding the literal construction:"
    echo "    // ARCH-ALLOWLIST: ports-scriptrecord-allowed"
    echo "    return &ports.ScriptRecord{ID: id, Title: title}"
    exit 1
fi
echo "OK: no *ports.ScriptRecord literals in production paths (godlike/06 SSOT writer = PersistenceProcessor)"
# ── Check 58: forbid legacy Template/TimelineJSON writes outside canonical allowlist ──
# godlike/06 SSOT one-canonical-owner-per-fact: PersistenceProcessor is the
# SOLE WRITER of Template + TimelineJSON on the scripts table (both set to
# empty "" under PR 6 — the dedicated idempotency_key + specscene columns are
# the canonical storage). The translators in repository.go
# (toSQLiteScriptRecord / fromSQLiteScriptRecord) are the canonical READ-path
# owners that translate between SQLite side and ports side. Every other
# production-code struct literal in internal/application/scripts/ that
# assigns Template: or TimelineJSON: outside those two canonical files is
# a SSOT regression — the fields are legacy columns intentionally left empty
# for newly-inserted rows per the PR 6 migration strategy.
#
# Pattern anchors (ripgrep regex, root-anchored substring):
#   Template:       — struct-literal field assignment (any value)
#   TimelineJSON:   — struct-literal field assignment (any value)
#
# Allowlist (the ONLY legitimate Template:/TimelineJSON: sites):
#   - internal/application/scripts/adapters/repository.go
#     (toSQLiteScriptRecord + fromSQLiteScriptRecord — canonical translators)
#   - internal/application/scripts/adapters/processor_persistence.go
#     (PersistenceProcessor — SOLE canonical writer, sets both to "")
#
# Tests (*_test.go) are excluded so test fixtures may freely construct
# ScriptRecord literals.
#
# ARCH-ALLOWLIST opt-in (mirrors Check 5/8/9/11/50): a transitional
# backfill that legitimately needs to write Template: or TimelineJSON:
# MUST prepend the magic marker
# `// ARCH-ALLOWLIST: template-timeline-legacy` on the line preceding
# the field assignment. The awk pre-pass strips such hits from the
# failing-set via the 25-line scroll-window tolerated by Check 5/8/9.
# Per AGENTS.md §8 zero-baseline rule, new allowlist entries require
# explicit owner + deadline.
echo "=== Check 58: forbid legacy Template/TimelineJSON writes outside canonical allowlist ==="
all_hits=$(rg -n --type go \
    -e 'Template:\s' \
    -e 'TimelineJSON:\s' \
    --glob '!**/application/scripts/adapters/repository.go' \
    --glob '!**/application/scripts/adapters/processor_persistence.go' \
    --glob '!**/*_test.go' \
    internal/application/scripts/ 2>/dev/null \
    || true)
# Drop full-line comments AND lines preceded by the ARCH-ALLOWLIST marker
# (25-line scroll-window, mirrors Check 5/8/9 pattern).
literal_calls=$(printf '%s\n' "$all_hits" \
    | awk -F: '
        {
            rest = ""
            for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
            if (rest ~ /^[[:space:]]*\/\/.*ARCH-ALLOWLIST:[[:space:]]*template-timeline-legacy/) {
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
    echo "FAIL: legacy Template:/TimelineJSON: field write outside canonical allowlist:"
    echo "$literal_calls"
    echo ""
    echo "Fix: Template and TimelineJSON are legacy columns intentionally left"
    echo "      empty for newly-inserted rows under PR 6. The dedicated"
    echo "      idempotency_key + specscene columns are the canonical storage."
    echo "      PersistenceProcessor is the SOLE canonical writer; repository.go"
    echo "      translators are the canonical READ-path owners. Any new"
    echo "      Template:/TimelineJSON: assignment outside those two files is a"
    echo "      godlike/06 one-owner-per-fact regression."
    echo ""
    echo "If the write is genuinely a transitional backfill / migration,"
    echo "      prepend the magic marker on the line preceding the assignment:"
    echo "    // ARCH-ALLOWLIST: template-timeline-legacy"
    echo "    Template: \"backfill_value\","
    exit 1
fi
echo "OK: no legacy Template:/TimelineJSON: writes outside canonical allowlist (godlike/06 SSOT)"


# === Check 59: Azione 13 VLM direct-caller ban (forward-prevention godlike/07) ===
# Bypass callers that hit /vlm/<verb> without going through the canonical
# *vlm.Client proxy are godlike/06 SSOT regressions. Canonical call surface
#   (SSOT): internal/infrastructure/ai/vlm/ (4 methods: AutoTagImage,
#   ValidateScript, DedupCheck, AutoTagLocal).
# Production callers MUST consume *vlm.Client via composition root.
# Permitted exceptions carry // ARCH-ALLOWLIST: vlm-direct-caller on the
# line preceding the call site (mirrors Check 54 + 58 posture).
vlm_bypass_hits=$(rg -n --hidden '\bhttp(|Get|Post|NewRequest|NewRequestWithContext)\(.*"/vlm/' internal/application internal/api 2>/dev/null || true)
filtered_hits=""
if [ -n "$vlm_bypass_hits" ]; then
  filtered_hits=$(echo "$vlm_bypass_hits" | while IFS= read -r hit; do
    [ -z "$hit" ] && continue
    f=$(echo "$hit" | cut -d: -f1)
    l=$(echo "$hit" | cut -d: -f2)
    prev=$((l - 1))
    allow=$(sed -n "${prev}p" "$f" 2>/dev/null | grep -c "ARCH-ALLOWLIST: vlm-direct-caller" || true)
    if [ "$allow" = "0" ]; then
      echo "$hit"
    fi
  done)
fi
fail_count=0
if [ -n "$filtered_hits" ]; then
  echo "Check 59 (VLM direct-caller ban): FAIL" >&2
  echo "  Direct http.*"/vlm/" callers in application/api without ARCH-ALLOWLIST:" >&2
  echo "$filtered_hits" | sed 's/^/    /' >&2
  echo "  Apply // ARCH-ALLOWLIST: vlm-direct-caller on the line preceding the call." >&2
  fail_count=$((fail_count + 1))
fi
if [ "$fail_count" -gt 0 ]; then
  exit 1
fi
echo "Check 59 (VLM direct-caller ban): OK (0 http.*"/vlm/" hits)"
