#!/usr/bin/env bash
# =============================================================================
# verify_siglip_sidecar.sh — operator-side verifier for the Python embedding
# sidecar's SigLIP endpoints. Posts to /embed_visual_from_image and
# /embed_visual_from_text and asserts the canonical SSOT envelope:
# model / model_version / dimensions=768 / |embedding|=768.
#
# godlike/06 SSOT: defaults match the live values Python sidecar emits
# (scripts/services/embedding_server/visual.py + visual.IndexSchema cross-ref).
#   - siglip-so400m-patch14-384 (canonical short form, accepts vendor-prefix)
#   - model_version = 2026-06-26-v1 (the live sidecar's actual emit)
#   - dimensions = 768 (canonical SigLIP so400m output)
#   - |embedding| = 768 (assertion of the array length)
#
# godlike/07 NO-FAKE-AVAILABILITY: any non-200 status, missing field, or
# dim/model mismatch ⇒ typed exit code (2,3,4,5,6). No silent fallback.
#
# Usage:
#   bash scripts/operations/verify_siglip_sidecar.sh \
#       --image-path /path/to/foo.png --text-query "stock footage"
#   bash scripts/operations/verify_siglip_sidecar.sh \
#       --url http://127.0.0.1:8001 --image-path /tmp/x.png --json
#   bash scripts/operations/verify_siglip_sidecar.sh --text-only
#   bash scripts/operations/verify_siglip_sidecar.sh \
#       --image-path /tmp/a.png \
#       --batch-count 3 \
#       --batch-image-paths "/tmp/a.png,/tmp/b.png,/tmp/c.png"
#   bash scripts/operations/verify_siglip_sidecar.sh --help
#
# Required tools: curl, jq.
#
# Exit codes (canonical for pager alerts):
#   0  Both endpoints PASS all 4 assertions.
#   1  Sidecar unreachable (HTTP /health non-200 or curl refused).
#   2  Endpoint returned non-200 (502/503/404 ⇒ sidecar drift; SKIP_SIGLIP=0 sets it).
#   3  Dimensions mismatch.
#   4  Model identity mismatch.
#   5  Model_version mismatch.
#   6  Embedding length mismatch.
#   7  Bad CLI usage (missing required arg, jq missing, --image-path
#      missing + --text-only not set, etc.).
#
# Companion runbook: docs/operations/verify-siglip-sidecar.md.
# =============================================================================

set -euo pipefail
# SIGPIPE-friendly trap — pagers piping to head/grep see the verifier's
# intended exit code (0..7), not 128+13=141 from the pipe-break signal.
trap '' PIPE

URL=""
IMAGE_PATH=""
TEXT_QUERY="hello world"
EXPECTED_MODEL="siglip-so400m-patch14-384"
EXPECTED_MODEL_VERSION="2026-06-26-v1"
EXPECTED_DIMENSION=768
TIMEOUT=30
TEXT_ONLY=0
JSON_MODE=0
BATCH_COUNT=0
BATCH_IMAGE_PATHS=""
PASS_COUNT=0
FAIL_COUNT=0
declare -a FAIL_LINES
IMG_RC=""
TEXT_RC=""
BATCH_RC=""

# ── colour helpers (NO_COLOR respected) ─────────────────────────────────────
if [ -t 1 ] && [ "${NO_COLOR:-0}" != "1" ]; then
  C_GREEN=$(printf '\033[32m')
  C_RED=$(printf '\033[31m')
  C_YELLOW=$(printf '\033[33m')
  C_CYAN=$(printf '\033[36m')
  C_DIM=$(printf '\033[2m')
  C_BOLD=$(printf '\033[1m')
  C_RESET=$(printf '\033[0m')
else
  C_GREEN=; C_RED=; C_YELLOW=; C_CYAN=; C_DIM=; C_BOLD=; C_RESET=
fi

log_pass() { printf "  ${C_GREEN}✓ PASS${C_RESET}  %s\n" "$1"; PASS_COUNT=$((PASS_COUNT + 1)); }
log_fail() { printf "  ${C_RED}✗ FAIL${C_RESET}  %s\n" "$1"; FAIL_COUNT=$((FAIL_COUNT + 1)); FAIL_LINES+=("$1"); }
log_info() { printf "  ${C_DIM}·${C_RESET}        %s\n" "$1"; }
section()  { printf "\n${C_BOLD}${C_CYAN}── %s ──${C_RESET}\n" "$1"; }

usage() { sed -n '2,28p' "$0"; }

# ── CLI parsing ─────────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --url)
      URL="${2:-}"
      [[ -z "$URL" ]] && { echo "ERROR: --url requires a URL" >&2; exit 7; }
      shift 2
      ;;
    --image-path)
      IMAGE_PATH="${2:-}"
      [[ -z "$IMAGE_PATH" ]] && { echo "ERROR: --image-path requires a path" >&2; exit 7; }
      shift 2
      ;;
    --text-query)
      TEXT_QUERY="${2:-}"
      [[ -z "$TEXT_QUERY" ]] && { echo "ERROR: --text-query requires a non-empty string" >&2; exit 7; }
      shift 2
      ;;
    --model)
      EXPECTED_MODEL="${2:-}"
      [[ -z "$EXPECTED_MODEL" ]] && { echo "ERROR: --model requires a non-empty string" >&2; exit 7; }
      shift 2
      ;;
    --model-version)
      EXPECTED_MODEL_VERSION="${2:-}"
      [[ -z "$EXPECTED_MODEL_VERSION" ]] && { echo "ERROR: --model-version requires a non-empty string" >&2; exit 7; }
      shift 2
      ;;
    --dimension)
      EXPECTED_DIMENSION="${2:-}"
      [[ ! "$EXPECTED_DIMENSION" =~ ^[1-9][0-9]*$ ]] && { echo "ERROR: --dimension must be a positive integer" >&2; exit 7; }
      shift 2
      ;;
    --timeout)
      TIMEOUT="${2:-}"
      [[ ! "$TIMEOUT" =~ ^[1-9][0-9]*$ ]] && { echo "ERROR: --timeout must be a positive integer (seconds)" >&2; exit 7; }
      shift 2
      ;;
    --text-only)
      TEXT_ONLY=1
      shift
      ;;
    --json)
      JSON_MODE=1
      shift
      ;;
    --batch-count)
      BATCH_COUNT="${2:-}"
      [[ ! "$BATCH_COUNT" =~ ^[0-9]+$ ]] && { echo "ERROR: --batch-count must be a non-negative integer" >&2; exit 7; }
      shift 2
      ;;
    --batch-image-paths)
      BATCH_IMAGE_PATHS="${2:-}"
      [[ -z "$BATCH_IMAGE_PATHS" ]] && { echo "ERROR: --batch-image-paths requires a comma-separated path list" >&2; exit 7; }
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    --*)
      echo "ERROR: unknown flag: $1" >&2
      usage >&2
      exit 7
      ;;
    *)
      echo "ERROR: extra positional argument: $1" >&2
      usage >&2
      exit 7
      ;;
  esac
done

# ── 1. Required tool preflight ─────────────────────────────────────────────
command -v curl >/dev/null 2>&1 || { echo "ERROR: curl not found in PATH" >&2; exit 7; }
command -v jq >/dev/null 2>&1   || { echo "ERROR: jq not found in PATH (apt install jq)" >&2; exit 7; }

# ── 2. Resolve sidecar URL ─────────────────────────────────────────────────
[[ -z "$URL" ]] && URL="${VELOX_EMBEDDING_SERVER_URL:-http://127.0.0.1:8001}"

if [[ $JSON_MODE -eq 0 ]]; then
  printf "${C_BOLD}${C_CYAN}━━━ Verify SigLIP sidecar ${C_BOLD}%s${C_RESET}\n" "$URL"
  printf "${C_DIM}expected  : model='%s' model_version='%s' dim=%d timeout=%ds text_only=%d${C_RESET}\n" \
         "$EXPECTED_MODEL" "$EXPECTED_MODEL_VERSION" "$EXPECTED_DIMENSION" "$TIMEOUT" "$TEXT_ONLY"
fi

# emit_json TEMPLATE --arg NAME VALUE [--argjson NAME VALUE ...]
#   TEMPLATE is the FIRST positional arg; --arg / --argjson options follow.
#   jq --arg/--argjson NAME VALUE pairs are parsed eagerly regardless of
#   argv position; variables are visible to the filter at evaluation time.
emit_json() {
  local template="$1"; shift
  jq -nc "$template" "$@"
}

# ── 3. /health probe (status-code only — no body diagnostic) ──────────────
HEALTH_HTTP=$(curl -s -o /dev/null -w '%{http_code}' --max-time "$TIMEOUT" "$URL/health" 2>/dev/null || echo "000")
if [[ "$HEALTH_HTTP" != "200" ]]; then
  if [[ $JSON_MODE -eq 1 ]]; then
    emit_json '{url:$u, status:"UNREACHABLE", health_http:$h, exit:1}' \
      --arg u "$URL" --argjson h "$HEALTH_HTTP"
  else
    echo >&2
    echo "ERROR: sidecar unreachable at $URL (GET /health returned HTTP $HEALTH_HTTP)" >&2
    echo "       (re)start: SKIP_SIGLIP=0 bash scripts/start_embedding_server.sh" >&2
  fi
  exit 1
fi

# ── 4. Fail-closed image-path gate ──────────────────────────────────────────
if [[ -z "$IMAGE_PATH" && $TEXT_ONLY -eq 0 ]]; then
  if [[ $JSON_MODE -eq 1 ]]; then
    emit_json '{url:$u, status:"CONFIG_ERROR", reason:"missing --image-path", hint:"supply --image-path <PATH> or pass --text-only", exit:7}' \
      --arg u "$URL"
  else
    echo >&2
    echo "ERROR: cannot verify /embed_visual_from_image — no --image-path supplied." >&2
    echo "       Pass --image-path <PATH> to verify image-encoder, or" >&2
    echo "       pass --text-only to verify text-encoder only (godlike/07 fail-closed)." >&2
  fi
  exit 7
fi

# Preflight: --image-path must exist on disk (godlike/07 — refuse to POST a
# known-impossible operation). Distinguishable from sidecar drift exit 2.
if [[ -n "$IMAGE_PATH" && ! -e "$IMAGE_PATH" ]]; then
  if [[ $JSON_MODE -eq 1 ]]; then
    emit_json '{url:$u, status:"CONFIG_ERROR", reason:"--image-path does not exist on disk", path:$p, exit:7}' \
      --arg u "$URL" --arg p "$IMAGE_PATH"
  else
    echo >&2
    echo "ERROR: --image-path does not exist on disk: $IMAGE_PATH" >&2
    echo "       Provide an existing PNG/JPEG path." >&2
  fi
  exit 7
fi

# ── 5. assert_response_env ─────────────────────────────────────────────────
# validate_response_env <label> <tmp_json> <http_code> <expected_dim>
#   Emits 4 SSOT assertions; returns 0 on full PASS, otherwise one of:
#   2 (non-200 / preflight-fail), 3 (dim), 4 (model), 5 (model_version), 6 (len).
validate_response_env() {
  local label="$1" tmp_json="$2" http_code="$3" expected_dim="$4"

  if [[ "$http_code" != "200" ]]; then
    log_fail "$label: HTTP $http_code (non-200 — endpoint not live or model not loaded)"
    return 2
  fi

  # Preflight: .embedding must be an array (otherwise length extraction errors).
  if ! jq -e ".embedding | type == \"array\"" "$tmp_json" >/dev/null 2>&1; then
    local emb_type
    emb_type=$(jq -r '.embedding | type' "$tmp_json" 2>/dev/null || echo "<jq-error>")
    log_fail "$label: envelope drift — .embedding is not an array (got '$emb_type')"
    return 2
  fi

  local obs_error obs_model obs_model_version obs_dimensions obs_emb_len

  obs_error=$(jq -r '.error // ""' "$tmp_json" 2>/dev/null || echo "")
  [[ -n "$obs_error" ]] && { log_fail "$label: sidecar returned error envelope: $obs_error"; return 2; }

  obs_model=$(jq -r '.model        // "<missing>"' "$tmp_json" 2>/dev/null || echo "<jq-error>")
  obs_model_version=$(jq -r '.model_version // "<missing>"' "$tmp_json" 2>/dev/null || echo "<jq-error>")
  obs_dimensions=$(jq -r '.dimensions   // "<missing>"' "$tmp_json" 2>/dev/null || echo "<jq-error>")
  obs_emb_len=$(jq -r '(.embedding   | length) // "<missing>"' "$tmp_json" 2>/dev/null || echo "<jq-error>")

  # Assertion A — model identity (substring match accepts vendor-prefix forms).
  if [[ "$obs_model" == *"$EXPECTED_MODEL"* ]]; then
    log_pass "$label: model='$obs_model' contains canonical '$EXPECTED_MODEL'"
  else
    log_fail "$label: model='$obs_model' does NOT contain canonical '$EXPECTED_MODEL'"
    return 4
  fi

  # Assertion B — model_version (literal string match, fail-fast).
  if [[ "$obs_model_version" == "$EXPECTED_MODEL_VERSION" ]]; then
    log_pass "$label: model_version='$obs_model_version' (canonical)"
  else
    log_fail "$label: model_version='$obs_model_version' ≠ expected '$EXPECTED_MODEL_VERSION'"
    return 5
  fi

  # Assertion C — dimensions field.
  if [[ "$obs_dimensions" == "$expected_dim" ]]; then
    log_pass "$label: dimensions=$obs_dimensions (canonical)"
  else
    log_fail "$label: dimensions=$obs_dimensions ≠ expected $expected_dim"
    return 3
  fi

  # Assertion D — embedding vector length.
  if [[ "$obs_emb_len" == "$expected_dim" ]]; then
    log_pass "$label: |embedding|=$obs_emb_len (matches canonical dim)"
  else
    log_fail "$label: |embedding|=$obs_emb_len ≠ expected $expected_dim"
    return 6
  fi

  return 0
}

# ── 6. POST /embed_visual_from_image ───────────────────────────────────────
if [[ -n "$IMAGE_PATH" ]]; then
  section "POST /embed_visual_from_image"
  IMG_TMP=$(mktemp /tmp/siglip_img_resp.XXXXXX.json)
  IMG_JSON=$(jq -nc --arg p "$IMAGE_PATH" '{image_path: $p}')
  IMG_HTTP=$(curl -s -o "$IMG_TMP" -w '%{http_code}' \
    --max-time "$TIMEOUT" \
    -H 'Content-Type: application/json' \
    --data-binary "$IMG_JSON" \
    "$URL/embed_visual_from_image" 2>/dev/null || echo "000")
  log_info "image_path=$IMAGE_PATH"
  log_info "endpoint response HTTP $IMG_HTTP (raw body $(wc -c <"$IMG_TMP" 2>/dev/null || echo "?") bytes)"
  validate_response_env "embed_visual_from_image" "$IMG_TMP" "$IMG_HTTP" "$EXPECTED_DIMENSION" || IMG_RC=$?
  rm -f "$IMG_TMP"
else
  IMG_RC=""
fi

# ── 7. POST /embed_visual_from_text ────────────────────────────────────────
section "POST /embed_visual_from_text"
TEXT_TMP=$(mktemp /tmp/siglip_text_resp.XXXXXX.json)
TEXT_JSON=$(jq -nc --arg t "$TEXT_QUERY" --arg m "$EXPECTED_MODEL" '{text: $t, model: $m}')
TEXT_HTTP=$(curl -s -o "$TEXT_TMP" -w '%{http_code}' \
  --max-time "$TIMEOUT" \
  -H 'Content-Type: application/json' \
  --data-binary "$TEXT_JSON" \
  "$URL/embed_visual_from_text" 2>/dev/null || echo "000")
log_info "text_query='$TEXT_QUERY'"
log_info "endpoint response HTTP $TEXT_HTTP (raw body $(wc -c <"$TEXT_TMP" 2>/dev/null || echo "?") bytes)"
validate_response_env "embed_visual_from_text" "$TEXT_TMP" "$TEXT_HTTP" "$EXPECTED_DIMENSION" || TEXT_RC=$?
rm -f "$TEXT_TMP"

# ── 8. Summary + exit code ──────────────────────────────────────────────────
section "summary"
if [[ $JSON_MODE -eq 1 ]]; then
  emit_json '{url:$u, pass:$p, fail:$f, status:(if $f == 0 then "OK" else "FAIL" end)}' \
    --arg u "$URL" --argjson p "$PASS_COUNT" --argjson f "$FAIL_COUNT"
else
  printf "  ${C_GREEN}passed: %d${C_RESET}\n" "$PASS_COUNT"
  printf "  ${C_RED}failed: %d${C_RESET}\n" "$FAIL_COUNT"
  if [[ ${#FAIL_LINES[@]} -gt 0 ]]; then
    printf "\n  ${C_RED}${C_BOLD}FAILED checks:${C_RESET}\n"
    for line in "${FAIL_LINES[@]}"; do
      printf "    - %s\n" "$line"
    done
  fi
fi

# ── 9. POST /embed_visual_from_images (optional batch happy path) ──────
# Fires only when BOTH --batch-count > 0 AND --batch-image-paths is supplied.
# Failures map onto the existing 0..7 exit-code family; no new codes. The
# batch section runs AFTER the per-image section so a broken per-image
# envelope still fails closed before the batch is consumed.
if [[ $BATCH_COUNT -gt 0 && -n "$BATCH_IMAGE_PATHS" ]]; then
  section "POST /embed_visual_from_images (batch_count=$BATCH_COUNT)"
  BATCH_PATHS_JSON=$(jq -nc \
    --arg csv "$BATCH_IMAGE_PATHS" \
    '($csv | split(",")) | {image_paths: .}')
  BATCH_TMP=$(mktemp /tmp/siglip_batch_resp.XXXXXX.json)
  BATCH_HTTP=$(curl -s -o "$BATCH_TMP" -w '%{http_code}' \
    --max-time "$TIMEOUT" \
    -H 'Content-Type: application/json' \
    --data-binary "$BATCH_PATHS_JSON" \
    "$URL/embed_visual_from_images" 2>/dev/null || echo "000")
  log_info "image_paths csv (count=$(echo "$BATCH_IMAGE_PATHS" | tr ',' '\n' | wc -l)) batch_count=$BATCH_COUNT"
  log_info "endpoint response HTTP $BATCH_HTTP (raw body $(wc -c <"$BATCH_TMP" 2>/dev/null || echo "?") bytes)"

  if [[ "$BATCH_HTTP" != "200" ]]; then
    log_fail "embed_visual_from_images: HTTP $BATCH_HTTP (non-200 — endpoint not live or model not loaded)"
    BATCH_RC=2
  elif ! jq -e '.embeddings | type == "array"' "$BATCH_TMP" >/dev/null 2>&1; then
    log_fail "embed_visual_from_images: envelope drift — .embeddings is not an array"
    BATCH_RC=2
  else
    OBS_COUNT=$(jq -r '.count // 0' "$BATCH_TMP" 2>/dev/null || echo 0)
    if [[ "$OBS_COUNT" == "$BATCH_COUNT" ]]; then
      log_pass "embed_visual_from_images: count=$OBS_COUNT matches expected $BATCH_COUNT"
    else
      log_fail "embed_visual_from_images: count=$OBS_COUNT ≠ expected $BATCH_COUNT"
      BATCH_RC=2
    fi

    OBS_DIM=$(jq -r '.dimensions // "<missing>"' "$BATCH_TMP" 2>/dev/null || echo "<jq-error>")
    if [[ "$OBS_DIM" == "$EXPECTED_DIMENSION" ]]; then
      log_pass "embed_visual_from_images: dimensions=$OBS_DIM (canonical)"
    else
      log_fail "embed_visual_from_images: dimensions=$OBS_DIM ≠ expected $EXPECTED_DIMENSION"
      BATCH_RC=3
    fi

    OBS_MODEL=$(jq -r '.model // "<missing>"' "$BATCH_TMP" 2>/dev/null || echo "<jq-error>")
    if [[ "$OBS_MODEL" == *"$EXPECTED_MODEL"* ]]; then
      log_pass "embed_visual_from_images: model='$OBS_MODEL' contains canonical '$EXPECTED_MODEL'"
    else
      log_fail "embed_visual_from_images: model='$OBS_MODEL' does NOT contain canonical '$EXPECTED_MODEL'"
      BATCH_RC=4
    fi

    OBS_MODEL_VERSION=$(jq -r '.model_version // "<missing>"' "$BATCH_TMP" 2>/dev/null || echo "<jq-error>")
    if [[ "$OBS_MODEL_VERSION" == "$EXPECTED_MODEL_VERSION" ]]; then
      log_pass "embed_visual_from_images: model_version='$OBS_MODEL_VERSION' (canonical)"
    else
      log_fail "embed_visual_from_images: model_version='$OBS_MODEL_VERSION' ≠ expected '$EXPECTED_MODEL_VERSION'"
      BATCH_RC=5
    fi

    VEC_LEN_MISMATCH=$(jq -r --argjson d "$EXPECTED_DIMENSION" \
      '(.embeddings | map(select(length != $d)) | length)' \
      "$BATCH_TMP" 2>/dev/null || echo 0)
    if [[ "$VEC_LEN_MISMATCH" == "0" ]]; then
      log_pass "embed_visual_from_images: all $OBS_COUNT vectors have |embedding|=$EXPECTED_DIMENSION"
    else
      log_fail "embed_visual_from_images: $VEC_LEN_MISMATCH vectors have |embedding| ≠ $EXPECTED_DIMENSION"
      BATCH_RC=6
    fi

    # Ordering invariant: response[i] corresponds to request image_paths[i].
    # We can't byte-compare the float vectors against any oracle here, but we
    # CAN confirm that every embedding index is reachable (count check above
    # already covers off-by-one). Ordering is implicit in the indexed JSON
    # encoding (FastAPI preserves list order by construction).
    log_info "ordering: response.embeddings[i] corresponds to request.image_paths[i] (JSON list order preserved)"
  fi
  rm -f "$BATCH_TMP"
fi

# fail-fast exit-code precedence: image-endpoint first, then batch, then text.
if [[ -n "$IMG_RC" ]]; then exit "$IMG_RC"; fi
if [[ -n "$BATCH_RC" ]]; then exit "$BATCH_RC"; fi
if [[ -n "$TEXT_RC" ]]; then exit "$TEXT_RC"; fi
exit 0
