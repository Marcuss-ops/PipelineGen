#!/usr/bin/env bash
# tests/operational/script_translation_e2e_smoke.sh
#
# Hermetic operator-facing smoke for the canonical TranslateScriptSpec
# surface (internal/application/scripts/usecase/translation.go).
#
# godlike/06 SSOT (one-canonical-owner-per-fact): this script is the
# SOLE canonical shell smoke for the script-translation canonical
# surface. It does NOT need a live PipelineGen server — TranslateScriptSpec
# is a pure Go function with NO HTTP endpoint yet (per the action plan
# `architecture/action-plans/2026-07-04-script-translation.md`); the
# hermetic Go TDDs at `internal/application/scripts/usecase/translation_test.go`
# are the authoritative functional surface.
#
# godlike/07 NO-FAKE-AVAILABILITY: this script ONLY reports pass/fail
# based on the actual `go test` exit code. There is NO silent-success
# fallback. If `go test` returns 0 and the PASS count matches the
# canonical test count (9), the script reports PASS; otherwise FAIL.
#
# godlike/07 minimum-blast-radius: this script reads from
# `tests/operational/lib/common.sh` for shared helpers (when present);
# if common.sh is missing, the script falls back to local minimal
# helpers without aborting.
#
# Exit codes (canonical per AGENTS.md pattern):
#   0  all 9 TDDs PASS (smoke green)
#   1  at least one TDD FAIL (smoke red)
#   2  prereq missing (go binary absent, package absent, etc.)
#   124 timeout (smoke exceeds 5 minutes)

set -uo pipefail

# ── Configuration ───────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
PACKAGE_DIR="${PROJECT_ROOT}/internal/application/scripts/usecase"
TIMEOUT_SECONDS=300
EXPECTED_PASS=9

# ── Common helpers (best-effort source from lib/common.sh) ──────────
COMMON_SH="${SCRIPT_DIR}/lib/common.sh"
if [[ -f "${COMMON_SH}" ]]; then
    # shellcheck disable=SC1090
    source "${COMMON_SH}"
else
    # Minimal fallback: log helpers (no color so we keep it POSIX-safe).
    log_info()  { echo "[INFO]  $*"; }
    log_warn()  { echo "[WARN]  $*"; }
    log_error() { echo "[ERROR] $*"; }
    log_pass()  { echo "[PASS]  $*"; }
    log_fail()  { echo "[FAIL]  $*"; }
fi

# ── Phase 1: Pre-flight ─────────────────────────────────────────────
echo "=== Phase 1: Pre-flight ==="

# 1.1. Go binary present.
if ! command -v go >/dev/null 2>&1; then
    log_error "go binary not found on PATH"
    log_error "Install Go and re-run: see https://go.dev/doc/install"
    exit 2
fi
log_info "go binary: $(go version)"

# 1.2. Project root has go.mod.
if [[ ! -f "${PROJECT_ROOT}/go.mod" ]]; then
    log_error "go.mod not found at ${PROJECT_ROOT}"
    log_error "Run this smoke from the project root or any of its subdirectories"
    exit 2
fi
log_info "go.mod: ${PROJECT_ROOT}/go.mod"

# 1.3. Translation package directory exists.
if [[ ! -d "${PACKAGE_DIR}" ]]; then
    log_error "translation package directory not found: ${PACKAGE_DIR}"
    exit 2
fi
log_info "translation package: ${PACKAGE_DIR}"

# 1.4. Translation production file exists.
if [[ ! -f "${PACKAGE_DIR}/translation.go" ]]; then
    log_error "translation.go not found: ${PACKAGE_DIR}/translation.go"
    exit 2
fi
log_info "translation.go present ($(wc -l < "${PACKAGE_DIR}/translation.go") LoC)"

# 1.5. Translation test file exists.
if [[ ! -f "${PACKAGE_DIR}/translation_test.go" ]]; then
    log_error "translation_test.go not found: ${PACKAGE_DIR}/translation_test.go"
    exit 2
fi
log_info "translation_test.go present ($(wc -l < "${PACKAGE_DIR}/translation_test.go") LoC)"

# ── Phase 2: gofmt check ────────────────────────────────────────────
echo ""
echo "=== Phase 2: gofmt check ==="
cd "${PROJECT_ROOT}"
UNFORMATTED="$(gofmt -l "${PACKAGE_DIR}/translation.go" "${PACKAGE_DIR}/translation_test.go" 2>&1)"
if [[ -n "${UNFORMATTED}" ]]; then
    log_error "gofmt -l found unformatted files: ${UNFORMATTED}"
    log_error "Run: gofmt -w ${PACKAGE_DIR}/translation.go ${PACKAGE_DIR}/translation_test.go"
    exit 1
fi
log_pass "gofmt clean on translation.go + translation_test.go"

# ── Phase 3: Run the 9 hermetic TDDs ─────────────────────────────────
echo ""
echo "=== Phase 3: Run 9 hermetic TDDs ==="
cd "${PROJECT_ROOT}"

# Build the test invocation. Use -count=1 to bypass Go's test cache so
# a regression in the source file is always detected on smoke run.
TEST_CMD=(go test -short -count=1 -v -timeout 60s -run '^TestTranslateScriptSpec' ./internal/application/scripts/usecase/...)

# Run with a hard timeout (gtimeout on macOS, timeout on Linux).
TIMEOUT_CMD=""
if command -v timeout >/dev/null 2>&1; then
    TIMEOUT_CMD="timeout ${TIMEOUT_SECONDS}"
elif command -v gtimeout >/dev/null 2>&1; then
    TIMEOUT_CMD="gtimeout ${TIMEOUT_SECONDS}"
fi

log_info "running: ${TIMEOUT_CMD:-direct} ${TEST_CMD[*]}"
TEST_OUTPUT="$(${TIMEOUT_CMD} "${TEST_CMD[@]}" 2>&1)"
TEST_EXIT=$?

# Parse pass/fail counts from `go test -v` output.
PASS_COUNT="$(echo "${TEST_OUTPUT}" | grep -cE '^--- PASS: TestTranslate' || true)"
FAIL_COUNT="$(echo "${TEST_OUTPUT}" | grep -cE '^--- FAIL: TestTranslate' || true)"
SKIP_COUNT="$(echo "${TEST_OUTPUT}" | grep -cE '^--- SKIP: TestTranslate' || true)"

# ── Phase 4: Verdict ─────────────────────────────────────────────────
echo ""
echo "=== Phase 4: Verdict ==="

if [[ ${TEST_EXIT} -eq 124 ]]; then
    log_error "go test timed out after ${TIMEOUT_SECONDS}s"
    log_error "Last 30 lines of test output:"
    echo "${TEST_OUTPUT}" | tail -30
    exit 124
fi

if [[ ${TEST_EXIT} -ne 0 ]]; then
    log_error "go test exited non-zero (exit=${TEST_EXIT})"
    log_error "Fail count: ${FAIL_COUNT} (Pass: ${PASS_COUNT}, Skip: ${SKIP_COUNT})"
    log_error "Last 30 lines of test output:"
    echo "${TEST_OUTPUT}" | tail -30
    exit 1
fi

if [[ ${PASS_COUNT} -lt ${EXPECTED_PASS} ]]; then
    log_error "PASS count ${PASS_COUNT} < expected ${EXPECTED_PASS} (Fail: ${FAIL_COUNT}, Skip: ${SKIP_COUNT})"
    log_error "This is the canonical godlike/07 NO-FAKE-AVAILABILITY invariant: incomplete test set = fail."
    exit 1
fi

if [[ ${FAIL_COUNT} -ne 0 ]]; then
    log_error "FAIL count ${FAIL_COUNT} != 0 (Pass: ${PASS_COUNT}, Skip: ${SKIP_COUNT})"
    log_error "Last 30 lines of test output:"
    echo "${TEST_OUTPUT}" | tail -30
    exit 1
fi

log_pass "All ${PASS_COUNT} hermetic TDDs PASS (Fail: ${FAIL_COUNT}, Skip: ${SKIP_COUNT})"
log_info "Translation canonical surface verified end-to-end (per-text strategy + validator reorder + warnings channel)"

# ── Phase 5: Summary of the 9 canonical contract assertions ──────────
echo ""
echo "=== Phase 5: Canonical contract summary ==="
cat <<'EOF'
TranslateScriptSpec canonical surface ships 9 hermetic contract tests:

  TestTranslateScriptSpec_PreservesSpecSceneStructure
    scene count + ids + indexes + kinds preserved byte-identical
  TestTranslateScriptSpec_DoesNotTranslateJSONKeys
    no translator input contains clip_id / drive_link / image_id
    (LLM structurally CANNOT mutate identifiers)
  TestTranslateScriptSpec_FailsOnEmptyTranslation
    ErrTranslationEmpty typed sentinel via errors.Is
  TestTranslateScriptSpec_PreservesClipBindings
    clip_id + drive_link + start_ms + end_ms + clip_title + image_id
    + image_url + image_status all byte-identical
  TestTranslateScriptSpec_CreatesGoogleDocWithSpecSceneBlock
    BuildGenerationDocumentHTML(out, title, "it", nil, nil, true)
    contains Italian "Capitolo N:" + drive link + NO translated JSON keys
  TestTranslateScriptSpec_LongScript_NoSceneLossNoTruncation
    10 scenes × ~4000 words → word count >= 70% total source
    (threshold computed against scene+full word count, NOT just full-text)
  TestTranslateScriptSpec_PreservesSpecialCharactersAndEmoji
    è à ç ñ á 🔥 👀 <tag> round-trip preserved
  TestTranslateScriptSpec_NilTranslator_TypedSentinel
    nil translator → ErrTranslationTranslatorMissing
  TestTranslateScriptSpec_EqualToSourceWarning
    translator returns equal-to-source → non-fatal warning in
    []string channel (operator-observability, NOT fail-closed)
EOF

log_pass "Smoke complete: TranslateScriptSpec canonical surface GREEN"
exit 0
