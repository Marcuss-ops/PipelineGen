#!/usr/bin/env bash
# scripts/batch_index_drive_clips.sh — batch wrapper for index-drive-clip (Sprint 2.2+).
#
# Reuses cmd/admin/index-drive-clip verbatim for each manifest, so:
#   - DisallowUnknownFields strict validation still runs per manifest.
#   - resolveClipDuration fail-closed semantics still apply.
#   - Per-manifest --allow-declared-duration flag is supported.
#
# Behaviour:
#   - Default is DRY-RUN + CONTINUE-ON-ERROR (non-strict): validates every
#     manifest under the dir (matching --pattern, excluding _TEMPLATE.json
#     and _TEMPLATE.md) and prints what WOULD be run. Exit 0 always.
#   - --apply: actually invokes the index-drive-clip command for each
#     manifest. Continues to the next on per-manifest failure.
#   - --strict (with --apply): exits non-zero at the first failure.
#   - --build forces a fresh `go build` of ./cmd/admin → ./bin/admin.
#     Without --build the wrapper uses the cached binary if it exists;
#     it does NOT auto-detect source staleness (no mtime check). Operators
#     editing cmd/admin/*.go must pass --build to see their changes.
#   - Per-clip evidence: stdout+stderr of each apply run is captured into
#     out/batch-index/<slug>.log so a multi-clip run leaves a paper trail.
#
# Usage:
#   scripts/batch_index_drive_clips.sh \
#       --manifests-dir cmd/admin/manifests \
#       [--pattern "*.json"] \
#       [--build] [--apply] [--strict] \
#       [--allow-declared-duration] [--drive-file-id OVERRIDE] \
#       [--bin PATH]
#
# godlike/07 minimum-blast-radius: only reads cmd/admin/manifests/ and the
# existing admin binary contract. Zero changes to Go source. Idempotent:
# re-running after partial success is safe (EnqueueAndIndex is idempotent
# on asset_id=drive_file_id).

# GNU date extension `%3N` (milliseconds) — Linux only. macOS date does NOT
# support this; the project is Linux so this is acceptable. If porting to
# macOS, swap to `python3 -c 'import time;print(int(time.time()*1000))'`.

set -euo pipefail

# ----------------------------------------------------------------------------
# Required tool precheck — fail loud BEFORE any state mutation if a
# dependency is missing (godlike/07 NO-FAKE-AVAILABILITY).
# ----------------------------------------------------------------------------
command -v jq >/dev/null 2>&1 || {
  printf '[%s] ERROR: jq is required for manifest syntax validation (apt-get install jq)\n' "$(date -u '+%H:%M:%S')" >&2
  exit 2
}
command -v go >/dev/null 2>&1 || {
  printf '[%s] ERROR: go is required to (re)build the admin binary\n' "$(date -u '+%H:%M:%S')" >&2
  exit 2
}

# ----------------------------------------------------------------------------
# Defaults — godlike/07 mirror of cmd/admin/drive_reconcile.go (dry-run
# default, --apply opt-in).
# ----------------------------------------------------------------------------
MANIFESTS_DIR="cmd/admin/manifests"
PATTERN="*.json"
APPLY=false
STRICT=false
BUILD=false
ALLOW_DECLARED_DURATION=false
DRIVE_FILE_ID_OVERRIDE=""
BIN="./bin/admin"

# ----------------------------------------------------------------------------
# Helpers
# ----------------------------------------------------------------------------
log()  { printf '[%s] %s\n' "$(date -u '+%H:%M:%S')" "$*"; }
err()  { printf '[%s] ERROR: %s\n' "$(date -u '+%H:%M:%S')" "$*" >&2; }

usage() {
  cat <<USAGE
Usage: $0 [options]

  --manifests-dir DIR   dir containing index clip manifests (default: cmd/admin/manifests)
  --pattern GLOB        glob to select manifests (default: *.json)
  --build               force build of ./cmd/admin → \$BIN before running
  --apply               actually run index-drive-clip (default: dry-run)
  --strict              with --apply, exit non-zero on first per-manifest failure
  --allow-declared-duration
                        pass --allow-declared-duration to each index-drive-clip call
  --drive-file-id ID    override drive_file_id for every manifest in this run
  --bin PATH            admin binary path (default: ./bin/admin)
  -h | --help           show this help

Default behaviour: dry-run + continue-on-error. Exit 0 unless --strict + --apply is set.
USAGE
}

# ----------------------------------------------------------------------------
# Args
# ----------------------------------------------------------------------------
while [[ $# -gt 0 ]]; do
  case "$1" in
    --manifests-dir)            MANIFESTS_DIR="${2:-}"; shift 2 ;;
    --pattern)                  PATTERN="${2:-}"; shift 2 ;;
    --build)                    BUILD=true; shift ;;
    --apply)                    APPLY=true; shift ;;
    --strict)                   STRICT=true; shift ;;
    --allow-declared-duration)  ALLOW_DECLARED_DURATION=true; shift ;;
    --drive-file-id)            DRIVE_FILE_ID_OVERRIDE="${2:-}"; shift 2 ;;
    --bin)                      BIN="${2:-}"; shift 2 ;;
    -h|--help)                  usage; exit 0 ;;
    *)                          err "unknown flag: $1"; usage; exit 2 ;;
  esac
done

# ----------------------------------------------------------------------------
# Sanity
# ----------------------------------------------------------------------------
if [[ ! -d "$MANIFESTS_DIR" ]]; then
  err "--manifests-dir not found: $MANIFESTS_DIR"; exit 2
fi
if [[ "$STRICT" == true && "$APPLY" != true ]]; then
  err "--strict requires --apply"; exit 2
fi

# ----------------------------------------------------------------------------
# Collect manifests (always skip the template).
# ----------------------------------------------------------------------------
mapfile -t MANIFESTS < <(
  find "$MANIFESTS_DIR" -maxdepth 1 -type f -name "$PATTERN" \
    ! -name '_TEMPLATE.json' \
    | sort
)

if [[ ${#MANIFESTS[@]} -eq 0 ]]; then
  err "no manifests matched in $MANIFESTS_DIR (pattern: $PATTERN)"
  exit 1
fi

log "found ${#MANIFESTS[@]} manifest(s) in $MANIFESTS_DIR"

# ----------------------------------------------------------------------------
# JSON syntactic pre-check (the strict decoder is the real gate, but this
# gives the operator a fast fail on typos before we touch Drive).
# ----------------------------------------------------------------------------
for m in "${MANIFESTS[@]}"; do
  if ! jq -e . "$m" >/dev/null 2>&1; then
    err "manifest is not valid JSON: $m"
    exit 1
  fi
done

# ----------------------------------------------------------------------------
# Build (once) — emits ./bin/admin; required for godlike/07 minimum-blast-radius.
# ----------------------------------------------------------------------------
if [[ "$APPLY" == true || "$BUILD" == true ]]; then
  if [[ ! -x "$BIN" || "$BUILD" == true ]]; then
    log "building $BIN from ./cmd/admin"
    mkdir -p "$(dirname "$BIN")"
    if ! go build -o "$BIN" ./cmd/admin; then
      err "go build failed"; exit 1
    fi
  fi
  if [[ ! -x "$BIN" ]]; then
    err "binary not found after build: $BIN"; exit 1
  fi
fi

# ----------------------------------------------------------------------------
# Per-manifest run.
# ----------------------------------------------------------------------------
indexed=0
failed=0
elapsed_total_ms=0

echo
echo "=== Batch Drive Indexer ==="
echo "  manifests_dir    : $MANIFESTS_DIR"
echo "  pattern          : $PATTERN"
echo "  mode             : $([[ $APPLY == true ]] && echo APPLY || echo DRY-RUN)"
echo "  strict           : $STRICT"
echo "  binary           : $BIN"
echo "  allow_declared   : $ALLOW_DECLARED_DURATION"
echo "  drive_id_override: ${DRIVE_FILE_ID_OVERRIDE:-<from manifest>}"
echo

for m in "${MANIFESTS[@]}"; do
  start_ms=$(date +%s%3N)
  printf '— [%s]\n' "$m"

  if [[ $APPLY != true ]]; then
    # Dry-run: emit the resolved command (helps operator eyeball the run).
    cmd=("$BIN" index-drive-clip --manifest "$m")
    [[ -n "$DRIVE_FILE_ID_OVERRIDE" ]] && cmd+=(--drive-file-id "$DRIVE_FILE_ID_OVERRIDE")
    [[ $ALLOW_DECLARED_DURATION == true ]] && cmd+=(--allow-declared-duration)
    printf '   would run: %q\n' "${cmd[*]}"
    end_ms=$(date +%s%3N)
    elapsed_ms=$((end_ms - start_ms))
    elapsed_total_ms=$((elapsed_total_ms + elapsed_ms))
    continue
  fi

  # Apply.
  cmd=("$BIN" index-drive-clip --manifest "$m")
  [[ -n "$DRIVE_FILE_ID_OVERRIDE" ]] && cmd+=(--drive-file-id "$DRIVE_FILE_ID_OVERRIDE")
  [[ $ALLOW_DECLARED_DURATION == true ]] && cmd+=(--allow-declared-duration)

  # Per-clip evidence: redirect stdout+stderr to a per-manifest log file.
  # Slug = basename of manifest minus ".json". This leaves a forensic trail
  # so an operator can grep after a multi-clip run.
  log_dir="out/batch-index"
  mkdir -p "$log_dir"
  slug=$(basename "$m" .json)
  log_file="$log_dir/${slug}.log"

  if "${cmd[@]}" >"$log_file" 2>&1; then
    rc=0
  else
    rc=$?
  fi
  end_ms=$(date +%s%3N)
  elapsed_ms=$((end_ms - start_ms))
  elapsed_total_ms=$((elapsed_total_ms + elapsed_ms))

  if [[ $rc -eq 0 ]]; then
    indexed=$((indexed + 1))
    printf '   ✅ OK (%d ms) log=%s\n' "$elapsed_ms" "$log_file"
  else
    failed=$((failed + 1))
    printf '   ❌ FAIL rc=%d (%d ms) log=%s\n' "$rc" "$elapsed_ms" "$log_file"
    # Echo the last 20 lines of the log to make diagnosis immediate without
    # opening the file. Failures are rare and surface in the operator console.
    echo '     --- last 20 lines of log ---'
    tail -n 20 "$log_file" | sed 's/^/     /'
    echo '     --- end log ---'
    if [[ $STRICT == true ]]; then
      err "strict mode: stopping at first failure (indexed=$indexed, failed=$failed)"
      exit 1
    fi
  fi
done

echo
echo "=== Summary ==="
echo "  manifests : ${#MANIFESTS[@]}"
echo "  indexed   : $indexed"
echo "  failed    : $failed"
echo "  elapsed   : $((elapsed_total_ms / 1000)).$((elapsed_total_ms % 1000))s"
if [[ $APPLY == true ]]; then
  echo "  per-clip  : out/batch-index/<slug>.log"
fi
if [[ $failed -gt 0 ]]; then
  echo "  next steps: fix the failing manifest(s) above and re-run with --apply."
  echo "  re-run safety: index-drive-clip is idempotent on asset_id=drive_file_id;"
  echo "                previously indexed clips stay indexed, only failures retry."
else
  echo "  re-run safety: a no-op re-run is safe (idempotent)."
fi

# Non-strict mode always exits 0 so partial successes don't mask a clean re-run.
if [[ $STRICT == true && $failed -gt 0 ]]; then
  exit 1
fi
exit 0
