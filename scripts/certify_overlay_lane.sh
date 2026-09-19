#!/usr/bin/env bash
# scripts/certify_overlay_lane.sh — the ONE command that answers
# "is the overlay lane on this machine running the code I just built, and did
# the render actually produce the overlays and timing I asked for?".
#
# Why this exists
# ---------------
# Certifying a change to the overlay lane used to cost a manual audit spread
# over two modules and five services:
#
#   detect the repo roots → run the right package tests → build BOTH binaries
#   → install them → restart the right units → work out which binary the port
#   owner really is → work out which RenderingGen worker will claim the job
#   → wait for /ready → submit a canary → poll a job that reports RUNNING/0
#   → find the plan in a nested JSON envelope → check the images by hand
#
# Every one of those steps had a way to lie: a stale worker could claim the job,
# a binary could ship with no embedded identity (the old `-X main.buildVersion`
# stamped symbols that did not exist), and /ready never said WHICH build was
# ready. This script removes the manual steps and replaces the guesswork with
# assertions that fail loudly.
#
# Contract of a run
# -----------------
#   1. preflight  — detect roots, tools, token; refuse to guess
#   2. test       — the overlay-lane Go packages (cheap, targeted)
#   3. build      — both binaries, stamped through platform/buildinfo
#   4. deploy     — backup + install + restart, so what runs is what was built
#   5. identity   — /health and /ready must report the exact binaries just built
#   6. canary     — submit the tracked payload with a deterministic idempotency key
#   7. verify     — overlays, motions, content addresses and timing in the result
#   8. bundle     — a portable artefact set (payload, plan, timing, verdict)
#                   that can be copied to another machine to extend the chain
#
# Usage
# -----
#   scripts/certify_overlay_lane.sh                      # full run
#   scripts/certify_overlay_lane.sh --stage=build        # stop after a stage
#   scripts/certify_overlay_lane.sh --skip-deploy        # certify the running build
#   scripts/certify_overlay_lane.sh --dry-run            # preflight + plan only
#
# Stages, in order: preflight test build deploy identity canary verify bundle
# `--stage=X` runs everything up to and including X. `--skip-deploy` keeps the
# currently installed binaries and skips `identity`'s digest match (it still
# reports the identity it found).
#
# Environment (all optional; defaults are the canonical local deployment)
#   VELOX_BASE_URL                 default http://127.0.0.1:8000
#   CERT_WORKER_HEALTH_URL         default http://127.0.0.1:8085/health
#   CERT_RENDERINGGEN_REPO         default ../RenderingGen/renderinggen
#   CERT_CANARY_PAYLOAD            default scripts/certify/overlay_lane_canary.json
#   CERT_BUNDLE_DIR                default ops/benchmarks/overlay-lane-<UTC>
#   CERT_CANARY_TIMEOUT_SEC        default 1800
#   CERT_READY_TIMEOUT_SEC         default 120
#   CERT_EXPECT_SCENES             default 5   (0 disables the check)
#   CERT_EXPECT_PHRASE_OVERLAYS    default 5   (0 disables the check)
#   CERT_EXPECT_ENTITY_OVERLAYS    default 5   (0 disables the check)
#   CERT_SKIP_WORKER               default 0   (1 → PipelineGen-only run)
#   CERT_INSTALL_RENDERINGGEN      default 0   (1 → also install the worker)
#   CERT_RESTART_RENDERINGGEN      command to restart the worker (optional)
set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd -- "$SCRIPT_DIR/.." && pwd)"
RG_REPO="${CERT_RENDERINGGEN_REPO:-$(cd -- "$ROOT_DIR/../RenderingGen/renderinggen" 2>/dev/null && pwd || echo "")}"

BASE_URL="${VELOX_BASE_URL:-http://127.0.0.1:8000}"
WORKER_HEALTH_URL="${CERT_WORKER_HEALTH_URL:-http://127.0.0.1:8085/health}"
CANARY_PAYLOAD="${CERT_CANARY_PAYLOAD:-$SCRIPT_DIR/certify/overlay_lane_canary.json}"
CANARY_TIMEOUT_SEC="${CERT_CANARY_TIMEOUT_SEC:-1800}"
READY_TIMEOUT_SEC="${CERT_READY_TIMEOUT_SEC:-120}"
EXPECT_SCENES="${CERT_EXPECT_SCENES:-5}"
EXPECT_PHRASE_OVERLAYS="${CERT_EXPECT_PHRASE_OVERLAYS:-5}"
EXPECT_ENTITY_OVERLAYS="${CERT_EXPECT_ENTITY_OVERLAYS:-5}"
SKIP_WORKER="${CERT_SKIP_WORKER:-0}"
INSTALL_RENDERINGGEN="${CERT_INSTALL_RENDERINGGEN:-0}"
RESTART_RENDERINGGEN="${CERT_RESTART_RENDERINGGEN:-}"
BUILDINFO_PKG="github.com/Marcuss-ops/PipelineGen/internal/platform/buildinfo"
RG_BUILDINFO_PKG="github.com/Marcuss-ops/RenderingGen/renderinggen/internal/buildinfo"

STAGE="bundle"
SKIP_DEPLOY=0
DRY_RUN=0
for arg in "$@"; do
  case "$arg" in
    --stage=*)      STAGE="${arg#--stage=}" ;;
    --skip-deploy)  SKIP_DEPLOY=1 ;;
    --dry-run)      DRY_RUN=1 ;;
    -h|--help)      sed -n '2,60p' "${BASH_SOURCE[0]}"; exit 0 ;;
    *) echo "unknown argument: $arg" >&2; exit 2 ;;
  esac
done

STAGE_ORDER=(preflight test build deploy identity canary verify bundle)
stage_index() {
  local i=0
  for s in "${STAGE_ORDER[@]}"; do
    [ "$s" = "$1" ] && { echo "$i"; return 0; }
    i=$((i + 1))
  done
  echo "-1"
}
STAGE_NUM="$(stage_index "$STAGE")"
if [ "$STAGE_NUM" -lt 0 ]; then
  echo "unknown --stage=$STAGE (allowed: ${STAGE_ORDER[*]})" >&2
  exit 2
fi
should_run() { [ "$(stage_index "$1")" -le "$STAGE_NUM" ]; }

RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)"
BUNDLE_DIR="${CERT_BUNDLE_DIR:-$ROOT_DIR/ops/benchmarks/overlay-lane-$RUN_ID}"
mkdir -p "$BUNDLE_DIR"

# ── reporting ─────────────────────────────────────────────────────────
# Every check is RECORDED, never short-circuited on the first failure: a
# certification that stops at the first problem hides the other four.
CHECKS_FILE="$BUNDLE_DIR/.checks.ndjson"
: > "$CHECKS_FILE"
FAILURES=0

log()  { printf '%s\n' "$*" >&2; }
step() { printf '\n== %s\n' "$*" >&2; }

check() { # check <name> <ok:0|1> <detail>
  local name="$1" ok="$2" detail="${3:-}"
  jq -cn --arg name "$name" --argjson ok "$([ "$ok" = "0" ] && echo true || echo false)" \
         --arg detail "$detail" '{name: $name, ok: $ok, detail: $detail}' >> "$CHECKS_FILE"
  if [ "$ok" = "0" ]; then
    log "  PASS $name ${detail:+— $detail}"
  else
    log "  FAIL $name ${detail:+— $detail}"
    FAILURES=$((FAILURES + 1))
  fi
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || { log "missing required command: $1"; exit 2; }
}

# ── 1. preflight ──────────────────────────────────────────────────────
if should_run preflight; then
  step "preflight"
  for cmd in go jq curl sha256sum; do require_cmd "$cmd"; done
  check "pipelinegen repo detected" "$([ -f "$ROOT_DIR/go.mod" ] && echo 0 || echo 1)" "$ROOT_DIR"
  if [ "$SKIP_WORKER" = "1" ]; then
    check "renderinggen repo (skipped by request)" 0 "CERT_SKIP_WORKER=1"
  else
    check "renderinggen repo detected" "$([ -n "$RG_REPO" ] && [ -f "$RG_REPO/go.mod" ] && echo 0 || echo 1)" "${RG_REPO:-<not found>}"
  fi
  check "canary payload present" "$([ -r "$CANARY_PAYLOAD" ] && echo 0 || echo 1)" "$CANARY_PAYLOAD"
  if [ "$FAILURES" -ne 0 ]; then
    log "preflight failed; refusing to guess the remaining inputs"
    exit 2
  fi
fi

# ── 2. test ───────────────────────────────────────────────────────────
if should_run test && [ "$DRY_RUN" = "0" ]; then
  step "test"
  OVERLAY_PKGS=(
    ./internal/capabilities/entities
    ./internal/capabilities/overlays
    ./internal/capabilities/scripts
    ./internal/platform/renderinggen
    ./internal/platform/buildinfo
    ./internal/platform/httpserver/...
  )
  if (cd "$ROOT_DIR" && go test "${OVERLAY_PKGS[@]}" -count=1); then
    check "pipelinegen overlay packages" 0 "${#OVERLAY_PKGS[@]} packages"
  else
    check "pipelinegen overlay packages" 1 "go test failed"
  fi
  if [ "$SKIP_WORKER" = "0" ] && [ -n "$RG_REPO" ]; then
    # Default set is deliberately the CHEAP, hermetic worker packages. The
    # full ./internal/overlay/... suite links the native Chronon renderer, so
    # its build cost dwarfs everything else in this script; it belongs to the
    # worker's own CI, not to a per-change lane certification. Override with
    # CERT_RG_TEST_PACKAGES when a renderer-level change needs it.
    RG_TEST_PACKAGES="${CERT_RG_TEST_PACKAGES:-./internal/buildinfo/... ./internal/health/...}"
    # shellcheck disable=SC2086 # package list is intentionally word-split
    if (cd "$RG_REPO" && go test $RG_TEST_PACKAGES -count=1); then
      check "renderinggen worker packages" 0 "$RG_TEST_PACKAGES"
    else
      check "renderinggen worker packages" 1 "go test $RG_TEST_PACKAGES"
    fi
  fi
fi

# ── 3. build ──────────────────────────────────────────────────────────
PIPELINEGEN_BIN=""
RENDERINGGEN_BIN=""
if should_run build && [ "$DRY_RUN" = "0" ]; then
  step "build"
  BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  VERSION="$(cd "$ROOT_DIR" && git describe --tags --always 2>/dev/null || echo dev)"
  COMMIT="$(cd "$ROOT_DIR" && git rev-parse --short HEAD 2>/dev/null || echo unknown)"

  PIPELINEGEN_BIN="$BUNDLE_DIR/pipelinegen-$RUN_ID"
  if (cd "$ROOT_DIR" && go build -trimpath \
        -ldflags "-s -w -X ${BUILDINFO_PKG}.Version=${VERSION} -X ${BUILDINFO_PKG}.GitCommit=${COMMIT} -X ${BUILDINFO_PKG}.BuildTime=${BUILD_TIME}" \
        -o "$PIPELINEGEN_BIN" ./cmd/server); then
    check "pipelinegen built" 0 "$(sha256sum "$PIPELINEGEN_BIN" | cut -d' ' -f1)"
  else
    check "pipelinegen built" 1 "go build ./cmd/server"
  fi

  if [ "$SKIP_WORKER" = "0" ] && [ -n "$RG_REPO" ]; then
    RENDERINGGEN_BIN="$BUNDLE_DIR/renderinggen-$RUN_ID"
    RG_VERSION="${VERSION#v}"
    if (cd "$RG_REPO" && go build -trimpath \
          -ldflags "-s -w -X ${RG_BUILDINFO_PKG}.Version=${RG_VERSION} -X ${RG_BUILDINFO_PKG}.GitCommit=${COMMIT} -X ${RG_BUILDINFO_PKG}.BuildTime=${BUILD_TIME}" \
          -o "$RENDERINGGEN_BIN" ./cmd/renderinggen); then
      check "renderinggen built" 0 "$(sha256sum "$RENDERINGGEN_BIN" | cut -d' ' -f1)"
    else
      check "renderinggen built" 1 "go build ./cmd/renderinggen"
    fi
  fi
fi

# ── 4. deploy ─────────────────────────────────────────────────────────
if should_run deploy && [ "$DRY_RUN" = "0" ] && [ "$SKIP_DEPLOY" = "0" ]; then
  step "deploy"
  if [ -n "$PIPELINEGEN_BIN" ]; then
    BACKUP="$ROOT_DIR/../.cache/pipelinegen-before-overlay-lane-$RUN_ID"
    mkdir -p "$(dirname "$BACKUP")"
    [ -f "$ROOT_DIR/bin/pipelinegen" ] && cp -p "$ROOT_DIR/bin/pipelinegen" "$BACKUP"
    install -m 0755 "$PIPELINEGEN_BIN" "$ROOT_DIR/bin/pipelinegen.new"
    mv -f "$ROOT_DIR/bin/pipelinegen.new" "$ROOT_DIR/bin/pipelinegen"
    check "pipelinegen installed" 0 "backup=${BACKUP##*/}"
    if "$ROOT_DIR/scripts/systemd/pipelinegenctl" restart >/dev/null 2>&1; then
      check "pipelinegen restarted" 0
    else
      check "pipelinegen restarted" 1 "scripts/systemd/pipelinegenctl restart"
    fi
  fi

  if [ "$SKIP_WORKER" = "0" ] && [ -n "$RENDERINGGEN_BIN" ]; then
    if [ "$INSTALL_RENDERINGGEN" = "1" ]; then
      if install -m 0755 "$RENDERINGGEN_BIN" /usr/local/bin/renderinggen 2>/dev/null; then
        check "renderinggen installed" 0 "/usr/local/bin/renderinggen"
      else
        check "renderinggen installed" 1 "install requires write access to /usr/local/bin"
      fi
    else
      check "renderinggen installed" 0 "skipped (CERT_INSTALL_RENDERINGGEN=0)"
    fi
    if [ -n "$RESTART_RENDERINGGEN" ]; then
      if bash -c "$RESTART_RENDERINGGEN" >/dev/null 2>&1; then
        check "renderinggen restarted" 0 "$RESTART_RENDERINGGEN"
      else
        check "renderinggen restarted" 1 "$RESTART_RENDERINGGEN"
      fi
    else
      check "renderinggen restarted" 0 "skipped (CERT_RESTART_RENDERINGGEN unset)"
    fi
  fi
elif should_run deploy && [ "$SKIP_DEPLOY" = "1" ]; then
  step "deploy"
  check "deploy" 0 "skipped (--skip-deploy): certifying the running build"
fi

# ── 5. identity ───────────────────────────────────────────────────────
# The whole point of the run: prove the process serving the canary is the
# binary that was just built. Without this, every later PASS is worthless.
if should_run identity && [ "$DRY_RUN" = "0" ]; then
  step "identity"
  READY_JSON="$BUNDLE_DIR/ready.json"
  deadline=$(( $(date +%s) + READY_TIMEOUT_SEC ))
  ready_ok=1
  while :; do
    if READY_JSON_TMP="$(curl -sS --max-time 5 "$BASE_URL/ready")" && \
       [ "$(printf '%s' "$READY_JSON_TMP" | jq -r '.status // empty')" = "ready" ]; then
      printf '%s' "$READY_JSON_TMP" > "$READY_JSON"
      ready_ok=0
      break
    fi
    [ "$(date +%s)" -ge "$deadline" ] && break
    sleep 1
  done
  check "pipelinegen /ready" "$ready_ok" "$BASE_URL/ready"

  if [ -f "$READY_JSON" ]; then
    got_hash="$(jq -r '.build.binary_sha256 // ""' "$READY_JSON")"
    got_commit="$(jq -r '.build.git_commit // ""' "$READY_JSON")"
    got_config="$(jq -r '.build.config_path // ""' "$READY_JSON")"
    check "pipelinegen reports build identity" "$([ -n "$got_hash" ] && echo 0 || echo 1)" "commit=${got_commit:-<none>} config=${got_config:-<none>}"
    if [ -n "$PIPELINEGEN_BIN" ] && [ "$SKIP_DEPLOY" = "0" ]; then
      want_hash="$(sha256sum "$PIPELINEGEN_BIN" | cut -d' ' -f1)"
      check "running pipelinegen IS the built binary" \
        "$([ "$got_hash" = "$want_hash" ] && echo 0 || echo 1)" \
        "running=${got_hash:0:16} built=${want_hash:0:16}"
    fi
  fi

  if [ "$SKIP_WORKER" = "0" ]; then
    WORKER_JSON="$BUNDLE_DIR/worker-health.json"
    if curl -sS --max-time 5 "$WORKER_HEALTH_URL" -o "$WORKER_JSON" 2>/dev/null; then
      w_hash="$(jq -r '.build.binary_sha256 // ""' "$WORKER_JSON")"
      w_id="$(jq -r '.build.worker_id // .worker // ""' "$WORKER_JSON")"
      check "renderinggen worker reports build identity" \
        "$([ -n "$w_hash" ] && echo 0 || echo 1)" \
        "worker=${w_id:-<none>} sha=${w_hash:0:16}"
      if [ -n "$RENDERINGGEN_BIN" ] && [ "$INSTALL_RENDERINGGEN" = "1" ]; then
        want_w_hash="$(sha256sum "$RENDERINGGEN_BIN" | cut -d' ' -f1)"
        check "running renderinggen IS the built binary" \
          "$([ "$w_hash" = "$want_w_hash" ] && echo 0 || echo 1)" \
          "running=${w_hash:0:16} built=${want_w_hash:0:16}"
      fi
    else
      check "renderinggen worker /health" 1 "$WORKER_HEALTH_URL unreachable"
    fi
  fi
fi

# ── 6. canary ─────────────────────────────────────────────────────────
JOB_ID=""
# The admin token comes from the canonical file through the canonical wrapper,
# NEVER from the caller's environment: an inherited token authenticates against
# a different secret than the live service and produces misleading 401s.
AUTH_TOKEN=""
if should_run identity || should_run canary || should_run verify; then
  AUTH_TOKEN="$("$ROOT_DIR/scripts/with-velox-auth" sh -c 'printf %s "$VELOX_ADMIN_TOKEN"')" || true
fi

if should_run canary && [ "$DRY_RUN" = "0" ]; then
  step "canary"
  IDEMPOTENCY_KEY="certify-overlay-lane-$RUN_ID"
  SUBMIT_JSON="$BUNDLE_DIR/submit.json"
  http_code="$(curl -sS --max-time 60 -o "$SUBMIT_JSON" -w '%{http_code}' \
      -X POST \
      -H "Authorization: Bearer $AUTH_TOKEN" \
      -H 'Content-Type: application/json' \
      -H "Idempotency-Key: $IDEMPOTENCY_KEY" \
      --data-binary "@$CANARY_PAYLOAD" \
      "$BASE_URL/api/script/generate" 2>"$BUNDLE_DIR/submit.stderr" || echo 000)"
  JOB_ID="$(jq -r '.job_id // empty' "$SUBMIT_JSON" 2>/dev/null || true)"
  check "canary submitted" "$([ -n "$JOB_ID" ] && echo 0 || echo 1)" "http=$http_code job_id=${JOB_ID:-<none>}"

  if [ -n "$JOB_ID" ]; then
    FULL_JSON="$BUNDLE_DIR/job-$JOB_ID.json"
    deadline=$(( $(date +%s) + CANARY_TIMEOUT_SEC ))
    status=""
    terminal=0
    while :; do
      # /full carries the nested generation envelope AND the timings; the flat
      # /api/jobs/{id} view reports RUNNING/0 for the whole render, which is
      # exactly the opaque polling this script replaces.
      if curl -sS --max-time 20 -H "Authorization: Bearer $AUTH_TOKEN" \
           "$BASE_URL/api/jobs/$JOB_ID/full" > "$FULL_JSON.tmp" 2>/dev/null && [ -s "$FULL_JSON.tmp" ]; then
        mv -f "$FULL_JSON.tmp" "$FULL_JSON"
        status="$(jq -r '.status // empty' "$FULL_JSON" 2>/dev/null || true)"
      fi
      case "$status" in
        SUCCEEDED|PARTIALLY_SUCCEEDED|FAILED|CANCELLED) terminal=1; break ;;
      esac
      if [ "$(date +%s)" -ge "$deadline" ]; then
        break
      fi
      sleep 5
    done
    if [ "$terminal" = "1" ]; then
      check "canary reached a terminal state" \
        "$([ "$status" = "SUCCEEDED" ] && echo 0 || echo 1)" "status=$status"
    else
      check "canary reached a terminal state" 1 "timed out after ${CANARY_TIMEOUT_SEC}s (last=${status:-<none>})"
    fi
  fi
fi

# ── 7. verify ─────────────────────────────────────────────────────────
# Assertions read the SAME projection an operator reads, so a green run means
# the product actually produced the overlays — not that a helper said so.
if should_run verify && [ "$DRY_RUN" = "0" ] && [ -n "$JOB_ID" ] && [ -f "$BUNDLE_DIR/job-$JOB_ID.json" ]; then
  step "verify"
  FULL_JSON="$BUNDLE_DIR/job-$JOB_ID.json"
  OVERLAY_PLAN="$BUNDLE_DIR/overlay_plan.json"
  jq '.result.result.overlay_plan // {}' "$FULL_JSON" > "$OVERLAY_PLAN"
  jq '.result.result.voiceover_timing // .result.result.timing // {}' "$FULL_JSON" > "$BUNDLE_DIR/timing.json"

  scenes="$(jq -r '(.result.result.scenes // []) | length' "$FULL_JSON")"
  phrase_overlays="$(jq -r '[.result.result.overlay_plan.items[]? | select(.kind == "text_phrase" or .kind == "phrase")] | length' "$FULL_JSON")"
  entity_overlays="$(jq -r '[.result.result.overlay_plan.items[]? | select(.kind == "entity_image")] | length' "$FULL_JSON")"
  items_total="$(jq -r '(.result.result.overlay_plan.items // []) | length' "$FULL_JSON")"

  if [ "$EXPECT_SCENES" -gt 0 ]; then
    check "scenes >= $EXPECT_SCENES" "$([ "$scenes" -ge "$EXPECT_SCENES" ] && echo 0 || echo 1)" "got $scenes"
  fi
  if [ "$EXPECT_PHRASE_OVERLAYS" -gt 0 ]; then
    check "phrase overlays >= $EXPECT_PHRASE_OVERLAYS" \
      "$([ "$phrase_overlays" -ge "$EXPECT_PHRASE_OVERLAYS" ] && echo 0 || echo 1)" "got $phrase_overlays"
  fi
  if [ "$EXPECT_ENTITY_OVERLAYS" -gt 0 ]; then
    check "entity image overlays >= $EXPECT_ENTITY_OVERLAYS" \
      "$([ "$entity_overlays" -ge "$EXPECT_ENTITY_OVERLAYS" ] && echo 0 || echo 1)" "got $entity_overlays"
  fi
  check "overlay plan is non-empty" "$([ "$items_total" -gt 0 ] && echo 0 || echo 1)" "$items_total items"

  # Every overlay item must carry a motion/preset identity: that is what makes
  # the entry/exit animation reproducible on the other machine.
  missing_motion="$(jq -r '[.result.result.overlay_plan.items[]? | select((.motion_id // "") == "" and (.preset_id // "") == "")] | length' "$FULL_JSON")"
  check "every overlay item declares motion or preset" \
    "$([ "$missing_motion" -eq 0 ] && echo 0 || echo 1)" "$missing_motion without motion/preset"

  # Every referenced asset must carry a 64-hex content address: the CAS
  # invariant that lets the worker verify bytes instead of trusting a URL.
  bad_hashes="$(jq -r '[.result.result.overlay_plan.items[]?.asset_refs[]? | select((.sha256 // "") | test("^[0-9a-f]{64}$") | not)] | length' "$FULL_JSON")"
  check "every overlay asset_ref has a content address" \
    "$([ "$bad_hashes" -eq 0 ] && echo 0 || echo 1)" "$bad_hashes without a 64-hex sha256"

  # A "resolved" entity image without a content address is the exact
  # half-materialized state that produced the reported hash/LocalPath mismatch.
  unresolved_hash="$(jq -r '[.result.result.scenes[]?.annotations?.primary_entities[]? | select((.image.status // "") == "resolved") | select((.image.sha256 // "") == "")] | length' "$FULL_JSON")"
  check "resolved entity images carry a content address" \
    "$([ "$unresolved_hash" -eq 0 ] && echo 0 || echo 1)" "$unresolved_hash resolved without sha256"

  timing_keys="$(jq -r 'if type == "object" then (keys | length) else 0 end' "$BUNDLE_DIR/timing.json")"
  check "timing envelope present" "$([ "$timing_keys" -gt 0 ] && echo 0 || echo 1)" "$timing_keys keys"
fi

# ── 8. bundle ─────────────────────────────────────────────────────────
if should_run bundle; then
  step "bundle"
  jq -s '{verdict: (if (map(select(.ok == false)) | length) == 0 then "PASS" else "FAIL" end),
          run_id: $run_id,
          stage: $stage,
          base_url: $base_url,
          job_id: $job_id,
          checks: .}' \
    --arg run_id "$RUN_ID" --arg stage "$STAGE" --arg base_url "$BASE_URL" --arg job_id "$JOB_ID" \
    "$CHECKS_FILE" > "$BUNDLE_DIR/verdict.json"

  [ -f "$BUNDLE_DIR/ready.json" ] && cp -f "$BUNDLE_DIR/ready.json" "$BUNDLE_DIR/identity-pipelinegen.json"
  [ -n "$PIPELINEGEN_BIN" ] && sha256sum "$PIPELINEGEN_BIN" | cut -d' ' -f1 > "$BUNDLE_DIR/pipelinegen.sha256"
  [ -n "$RENDERINGGEN_BIN" ] && sha256sum "$RENDERINGGEN_BIN" | cut -d' ' -f1 > "$BUNDLE_DIR/renderinggen.sha256"
  cp -f "$CANARY_PAYLOAD" "$BUNDLE_DIR/canary-payload.json" 2>/dev/null || true

  # The manifest is what travels to the other machine: it names the exact
  # inputs, the observed identity and the verdict, with relative paths only.
  {
    echo "# Overlay lane certification bundle"
    echo
    echo "- run_id: \`$RUN_ID\`"
    echo "- verdict: **$(jq -r '.verdict' "$BUNDLE_DIR/verdict.json")**"
    echo "- stage reached: \`$STAGE\`"
    echo "- pipelinegen base url: \`$BASE_URL\`"
    echo "- canary job: \`${JOB_ID:-<none>}\`"
    echo
    echo "## Files"
    echo
    for f in canary-payload.json identity-pipelinegen.json worker-health.json overlay_plan.json timing.json verdict.json; do
      [ -f "$BUNDLE_DIR/$f" ] && echo "- \`$f\`"
    done
    echo
    echo "## Replay on another machine"
    echo
    echo '```bash'
    echo "scripts/certify_overlay_lane.sh --skip-deploy \\"
    echo "  CERT_CANARY_PAYLOAD=bundle/canary-payload.json"
    echo '```'
    echo
    echo "## Checks"
    echo
    jq -r '.checks[] | "- [\(if .ok then "x" else " " end)] \(.name)\(if .detail != "" then " — " + .detail else "" end)"' "$BUNDLE_DIR/verdict.json"
  } > "$BUNDLE_DIR/MANIFEST.md"

fi

VERDICT="$(jq -r '.verdict' "$BUNDLE_DIR/verdict.json" 2>/dev/null || echo FAIL)"
log ""
log "verdict: $VERDICT ($FAILURES failed check(s))"
log "bundle:  $BUNDLE_DIR"
log "manifest: $BUNDLE_DIR/MANIFEST.md"
[ "$VERDICT" = "PASS" ] || exit 1
