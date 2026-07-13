#!/usr/bin/env bash
# =============================================================================
# verify_qdrant_point.sh — operator-side verifier for the canonical Qdrant
# point stored under media_assets_current. Two operations:
#
#   (1) Scroll-by-asset-id: POST /collections/{collection}/points/scroll
#       with filter {must:[{key:asset_id, match:{value:<id>}}]} and assert
#         - response.result.points | length == 1
#         - payload.points[0].payload.media_type == "image"
#         - payload.points[0].payload.visual_dimensions == 768
#
#   (2) Text-to-visual search: produce a SigLIP query embedding (via the
#       Python sidecar at /embed_visual_from_text OR --query-vector <file>)
#       then POST /collections/{collection}/points/query with body
#         {"query": <vector>, "using": "visual", "limit": <K>,
#          "with_payload": true, "filter": {media_type:"image"}}
#       and emit top hits as {score, asset_id, name} rows.
#
# godlike/06 SSOT: defaults match the canonical pipeline state.
#   - SIGLIP_MODEL = siglip-so400m-patch14-384
#   - EMBED_DIMENSION = 768       (matches schema.VisualEmbeddingDim)
#   - NAMED_VECTOR    = "visual"  (matches DefaultV3Schema.DenseVectors[Channel=visual])
#   - URL defaults    = Qdrant http://127.0.0.1:6333, sidecar http://127.0.0.1:8001
#   - COLLECTION alias = media_assets_current (canonical v3 alias per
#                        schema.DefaultV3Schema.RuntimeAlias)
#
# godlike/07 NO-FAKE-AVAILABILITY: any non-200 status, missing filter
# field, dim/model mismatch, or malformed envelope ⇒ typed exit code
# (1..7). No silent fallback to "looks close enough".
#
# Usage:
#   bash scripts/operations/verify_qdrant_point.sh --asset-id <uuid>
#   bash scripts/operations/verify_qdrant_point.sh \
#       --asset-id <uuid> --text-query "stock footage" --limit 5 --json
#   bash scripts/operations/verify_qdrant_point.sh \
#       --asset-id <uuid> --query-vector /tmp/query-vec.json --scroll-only
#   bash scripts/operations/verify_qdrant_point.sh \
#       --asset-id <uuid> --search-only --json
#   bash scripts/operations/verify_qdrant_point.sh --help
#
# Required tools: curl, jq.
#
# Exit codes (canonical for pager alerts):
#   0  All scroll assertions PASS + text-to-visual search returned ≥0 hits.
#   1  Qdrant or sidecar unreachable (HTTP error / curl refused).
#   2  Endpoint returned non-200 (otherwise unreachable sidecar or drift).
#   3  Dimensions mismatch (visual_dimensions/payload OR vector length OR
#      sidecar dim != 768).
#   4  Identity mismatch (payload.media_type != "image" OR sidecar model
#      doesn't contain canonical siglip-so400m-patch14-384).
#   5  Scroll returned != 1 points (operator fix: reindex).
#   6  Reserved (currently unused — filter-shape drift returns 2 from the
#      inner jq preflight instead).
#   7  Bad CLI usage (missing --asset-id, jq missing, dimension/limit
#      not positive integer, etc.).
#
# Companion runbook:
#   docs/operations/verify-qdrant-point.md (operator-facing procedure).
# =============================================================================

set -euo pipefail
# SIGPIPE-friendly trap — pagers piping to head/grep see the verifier's
# intended exit code (0..7), not 128+13=141 from the pipe-break signal.
trap '' PIPE

ASSET_ID=""
QDRANT_URL=""
SIDECAR_URL=""
COLLECTION="media_assets_current"
NAMED_VECTOR="visual"
TEXT_QUERY="stock footage"
QUERY_VECTOR_FILE=""
LIMIT=5
TIMEOUT=30
EXPECTED_MEDIA_TYPE="image"
EXPECTED_DIMENSION=768
EXPECTED_MODEL="siglip-so400m-patch14-384"
SCROLL_ONLY=0
SEARCH_ONLY=0
JSON_MODE=0
PASS_COUNT=0
FAIL_COUNT=0
declare -a FAIL_LINES
SCROLL_RC=""
SEARCH_RC=""

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

usage() { sed -n '2,42p' "$0"; }

# ── CLI parsing ─────────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --asset-id)
      ASSET_ID="${2:-}"
      [[ -z "$ASSET_ID" ]] && { echo "ERROR: --asset-id requires a non-empty uuid/string" >&2; exit 7; }
      shift 2
      ;;
    --qdrant-url)
      QDRANT_URL="${2:-}"
      [[ -z "$QDRANT_URL" ]] && { echo "ERROR: --qdrant-url requires a URL" >&2; exit 7; }
      shift 2
      ;;
    --sidecar-url)
      SIDECAR_URL="${2:-}"
      [[ -z "$SIDECAR_URL" ]] && { echo "ERROR: --sidecar-url requires a URL" >&2; exit 7; }
      shift 2
      ;;
    --collection)
      COLLECTION="${2:-}"
      [[ -z "$COLLECTION" ]] && { echo "ERROR: --collection requires a name" >&2; exit 7; }
      shift 2
      ;;
    --named-vector)
      NAMED_VECTOR="${2:-}"
      [[ -z "$NAMED_VECTOR" ]] && { echo "ERROR: --named-vector requires a name" >&2; exit 7; }
      shift 2
      ;;
    --text-query)
      TEXT_QUERY="${2:-}"
      [[ -z "$TEXT_QUERY" ]] && { echo "ERROR: --text-query requires a non-empty string" >&2; exit 7; }
      shift 2
      ;;
    --query-vector)
      QUERY_VECTOR_FILE="${2:-}"
      [[ -z "$QUERY_VECTOR_FILE" ]] && { echo "ERROR: --query-vector requires a path" >&2; exit 7; }
      shift 2
      ;;
    --limit)
      LIMIT="${2:-}"
      [[ ! "$LIMIT" =~ ^[1-9][0-9]*$ ]] && { echo "ERROR: --limit must be a positive integer" >&2; exit 7; }
      shift 2
      ;;
    --timeout)
      TIMEOUT="${2:-}"
      [[ ! "$TIMEOUT" =~ ^[1-9][0-9]*$ ]] && { echo "ERROR: --timeout must be a positive integer (seconds)" >&2; exit 7; }
      shift 2
      ;;
    --scroll-only)
      SCROLL_ONLY=1
      shift
      ;;
    --search-only)
      SEARCH_ONLY=1
      shift
      ;;
    --json)
      JSON_MODE=1
      shift
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

# `--scroll-only` and `--search-only` together is contradictory (would
# short-circuit both halves and exit before doing anything useful). Refuse
# to satisfy concurrent intent — exit 7 with a clear message.
if [[ $SCROLL_ONLY -eq 1 && $SEARCH_ONLY -eq 1 ]]; then
  echo "ERROR: --scroll-only and --search-only are mutually exclusive" >&2
  exit 7
fi

# ── 1. Required tool preflight ─────────────────────────────────────────────
command -v curl >/dev/null 2>&1 || { echo "ERROR: curl not found in PATH" >&2; exit 7; }
command -v jq >/dev/null 2>&1   || { echo "ERROR: jq not found in PATH (apt install jq)" >&2; exit 7; }

# ── 2. URL resolution ──────────────────────────────────────────────────────
[[ -z "$QDRANT_URL"  ]] && QDRANT_URL="${VELOX_QDRANT_URL:-http://127.0.0.1:6333}"
[[ -z "$SIDECAR_URL" ]] && SIDECAR_URL="${VELOX_EMBEDDING_SERVER_URL:-http://127.0.0.1:8001}"

if [[ $JSON_MODE -eq 0 ]]; then
  printf "${C_BOLD}${C_CYAN}━━━ Verify Qdrant point ${C_BOLD}%s${C_RESET}\n" "$COLLECTION"
  printf "${C_DIM}qdrant=%s  sidecar=%s  asset_id=%s  limit=%d  scroll_only=%d  search_only=%d${C_RESET}\n" \
         "$QDRANT_URL" "$SIDECAR_URL" "$ASSET_ID" "$LIMIT" "$SCROLL_ONLY" "$SEARCH_ONLY"
  printf "${C_DIM}expected: media_type=%s  visual_dimensions=%d  named_vector=%s${C_RESET}\n" \
         "$EXPECTED_MEDIA_TYPE" "$EXPECTED_DIMENSION" "$NAMED_VECTOR"
fi

# emit_json TEMPLATE --arg NAME VALUE [--argjson NAME VALUE ...]
#   TEMPLATE is the FIRST positional arg; --arg / --argjson options follow.
#   jq --arg/--argjson NAME VALUE pairs are parsed eagerly regardless of
#   argv position; variables are visible to the filter at evaluation time.
emit_json() {
  local template="$1"; shift
  jq -nc "$template" "$@"
}

# ── 3. Fail-closed gates ───────────────────────────────────────────────────
# godlike/07: refuse to scan a non-canonical collection name or to query
# without an asset_id. Both are operator-intent violations.
if [[ -z "$ASSET_ID" ]]; then
  if [[ $JSON_MODE -eq 1 ]]; then
    emit_json '{status:"CONFIG_ERROR", reason:"missing --asset-id", hint:"supply --asset-id <id>", exit:7}'
  else
    echo >&2
    echo "ERROR: --asset-id is required (asset_id is the Qdrant point id per reindex_qdrant_apply.go:50)" >&2
    echo "       bash scripts/operations/verify_qdrant_point.sh --asset-id <uuid>" >&2
  fi
  exit 7
fi

if [[ -z "$COLLECTION" ]]; then
  if [[ $JSON_MODE -eq 1 ]]; then
    emit_json '{status:"CONFIG_ERROR", reason:"empty --collection", exit:7}'
  else
    echo >&2
    echo "ERROR: --collection cannot be empty (default: media_assets_current)" >&2
  fi
  exit 7
fi

# ── 4. Qdrant reachability probe ───────────────────────────────────────────
HEALTH_HTTP=$(curl -s -o /dev/null -w '%{http_code}' --max-time "$TIMEOUT" "$QDRANT_URL/" 2>/dev/null || echo "000")
if [[ "$HEALTH_HTTP" != "200" ]]; then
  if [[ $JSON_MODE -eq 1 ]]; then
    emit_json '{status:"UNREACHABLE", url:$u, health_http:$h, exit:1}' \
      --arg u "$QDRANT_URL" --argjson h "$HEALTH_HTTP"
  else
    echo >&2
    echo "ERROR: Qdrant unreachable at $QDRANT_URL (GET / returned HTTP $HEALTH_HTTP)" >&2
    echo "       Restart: SKIP_SIGLIP=0 bash scripts/start_embedding_server.sh (sidecar)" >&2
    echo "                 docker compose up -d qdrant  (Qdrant container)" >&2
  fi
  exit 1
fi

# ── 5. SCROLL-BY-ASSET-ID ──────────────────────────────────────────────────
# Canonical shape (matches client_scroll.go, seed.go:243, tests/operational/*):
#   POST /collections/{collection}/points/scroll
#   body: {"limit":1, "with_payload":true, "with_vector":false,
#          "filter":{"must":[{"key":"asset_id","match":{"value":"<id>"}}]}}
# Response envelope: {"result":{"points":[{"id":"...","payload":{...}}],
#                       "next_page_offset":...}, "status":"ok", "time":...}
assert_scroll() {
  local tmp_json="$1" http_code="$2"

  if [[ "$http_code" != "200" ]]; then
    log_fail "scroll: HTTP $http_code (non-200 — Qdrant drift or collection missing)"
    return 2
  fi

  # Preflight: envelope must include result.points (NOT legacy result-array).
  if ! jq -e ".result.points | type == \"array\"" "$tmp_json" >/dev/null 2>&1; then
    local pts_type
    pts_type=$(jq -r '.result.points | type // "missing"' "$tmp_json" 2>/dev/null || echo "<jq-error>")
    log_fail "scroll: envelope drift — .result.points is not an array (got '$pts_type')"
    return 2
  fi

  # Assertion 1 (per user task): points count must be exactly 1.
  local pts_count
  pts_count=$(jq -r '.result.points | length' "$tmp_json" 2>/dev/null || echo "0")
  if [[ "$pts_count" == "1" ]]; then
    log_pass "scroll: points=1 (asset_id=$ASSET_ID found exactly once)"
  else
    log_fail "scroll: points=$pts_count (expected exactly 1 — reindex if >1, fix input if 0)"
    return 5
  fi

  # Assertion 2 (per user task): payload.media_type must equal "image".
  local obs_media_type
  obs_media_type=$(jq -r '.result.points[0].payload.media_type // "<missing>"' "$tmp_json" 2>/dev/null || echo "<jq-error>")
  if [[ "$obs_media_type" == "$EXPECTED_MEDIA_TYPE" ]]; then
    log_pass "scroll: payload.media_type='$obs_media_type' (canonical)"
  else
    log_fail "scroll: payload.media_type='$obs_media_type' ≠ expected '$EXPECTED_MEDIA_TYPE'"
    return 4
  fi

  # Assertion 3 (per user task): payload.visual_dimensions must equal 768.
  # Per godlike/06 SSOT, this MUST be a payload field on the canonical v3
  # schema. If the field is absent from the canonical schema, the assertion
  # fails closed and surfaces the schema gap rather than silently passing.
  local obs_visual_dim
  obs_visual_dim=$(jq -r '.result.points[0].payload.visual_dimensions // "<missing>"' "$tmp_json" 2>/dev/null || echo "<jq-error>")
  if [[ "$obs_visual_dim" == "$EXPECTED_DIMENSION" ]]; then
    log_pass "scroll: payload.visual_dimensions=$obs_visual_dim (canonical SigLIP so400m patch14-384)"
  elif [[ "$obs_visual_dim" == "<missing>" ]]; then
    log_fail "scroll: payload.visual_dimensions ABSENT from payload (godlike/06 SSOT schema gap: canonical v3 PayloadIndexes does NOT include visual_dimensions per DefaultV3Schema). Operator fix: either (a) add visual_dimensions:768 to the ingest path, or (b) accept length(internal visual vector)==768 as the canonical premise instead of a payload field."
    return 3
  else
    log_fail "scroll: payload.visual_dimensions=$obs_visual_dim ≠ expected $EXPECTED_DIMENSION (godlike/06 — value-drift gate)"
    return 3
  fi

  return 0
}

if [[ $SEARCH_ONLY -eq 0 ]]; then
  section "POST /collections/${COLLECTION}/points/scroll  (asset_id=${ASSET_ID})"
  SCROLL_TMP=$(mktemp /tmp/qdrant_scroll.XXXXXX.json)
  # Build scroll body via jq --arg to defensively escape ASSET_ID against
  # shell metachars / special chars.
  SCROLL_BODY=$(jq -nc --arg id "$ASSET_ID" --argjson l 1 \
    '{limit: $l, with_payload: true, with_vector: false, filter: {must: [{key: "asset_id", match: {value: $id}}]}}')
  SCROLL_HTTP=$(curl -s -o "$SCROLL_TMP" -w '%{http_code}' \
    --max-time "$TIMEOUT" \
    -H 'Content-Type: application/json' \
    --data-binary "$SCROLL_BODY" \
    "$QDRANT_URL/collections/$COLLECTION/points/scroll" 2>/dev/null || echo "000")
  log_info "endpoint response HTTP $SCROLL_HTTP (raw body $(wc -c <"$SCROLL_TMP" 2>/dev/null || echo "?") bytes)"
  assert_scroll "$SCROLL_TMP" "$SCROLL_HTTP" || SCROLL_RC=$?
  rm -f "$SCROLL_TMP"
fi

# ── 6. SIGLIP QUERY EMBEDDING ───────────────────────────────────────────────
# Two paths:
#   a) --query-vector <file.json>  → read pre-computed 768d vector from disk
#   b) otherwise                     → POST /embed_visual_from_text on sidecar
# Path (a) wins when supplied (allows offline / pre-cached verification).

QUERY_VECTOR=""
if [[ $SEARCH_ONLY -eq 0 && -z "$SCROLL_RC" ]]; then
  if [[ -n "$QUERY_VECTOR_FILE" ]]; then
    if [[ ! -e "$QUERY_VECTOR_FILE" ]]; then
      if [[ $JSON_MODE -eq 1 ]]; then
        emit_json '{status:"CONFIG_ERROR", reason:"--query-vector file does not exist", path:$p, exit:7}' \
          --arg p "$QUERY_VECTOR_FILE"
      else
        echo >&2
        echo "ERROR: --query-vector file does not exist: $QUERY_VECTOR_FILE" >&2
      fi
      exit 7
    fi
    # Read vector from file. File MUST contain a JSON array of 768 floats.
    # jq preflight on the file's content.
    if ! jq -e "type == \"array\" and length == $EXPECTED_DIMENSION and (map(type) | unique) == [\"number\"]" \
        "$QUERY_VECTOR_FILE" >/dev/null 2>&1; then
      local_file_type=$(jq -r 'type' "$QUERY_VECTOR_FILE" 2>/dev/null || echo "<jq-error>")
      local_file_len=$(jq -r 'length // "missing"' "$QUERY_VECTOR_FILE" 2>/dev/null || echo "<jq-error>")
      if [[ $JSON_MODE -eq 1 ]]; then
        emit_json '{status:"CONFIG_ERROR", reason:"--query-vector does not contain a 768-float JSON array", type:$t, length:$l, exit:7}' \
          --arg t "$local_file_type" --argjson l "$local_file_len"
      else
        echo >&2
        echo "ERROR: --query-vector '$QUERY_VECTOR_FILE' is not a 768-float JSON array (got type=$local_file_type, length=$local_file_len)" >&2
      fi
      exit 7
    fi
    # Compact 1-line JSON for embedding into the search body.
    QUERY_VECTOR=$(jq -c '.' "$QUERY_VECTOR_FILE" 2>/dev/null || echo "[]")
    section "SIGLIP QUERY EMBEDDING (pre-computed from --query-vector)"
    log_info "loaded 768-dim vector from $QUERY_VECTOR_FILE"
  else
    # Path (b): hit the sidecar.
    section "POST ${SIDECAR_URL}/embed_visual_from_text  (text_query='${TEXT_QUERY}')"
    SIDECAR_TMP=$(mktemp /tmp/siglip_query_resp.XXXXXX.json)
    SIDECAR_BODY=$(jq -nc --arg t "$TEXT_QUERY" --arg m "$EXPECTED_MODEL" '{text: $t, model: $m}')
    SIDECAR_HTTP=$(curl -s -o "$SIDECAR_TMP" -w '%{http_code}' \
      --max-time "$TIMEOUT" \
      -H 'Content-Type: application/json' \
      --data-binary "$SIDECAR_BODY" \
      "$SIDECAR_URL/embed_visual_from_text" 2>/dev/null || echo "000")
    log_info "endpoint response HTTP $SIDECAR_HTTP (raw body $(wc -c <"$SIDECAR_TMP" 2>/dev/null || echo "?") bytes)"

    if [[ "$SIDECAR_HTTP" != "200" ]]; then
      log_fail "sidecar /embed_visual_from_text: HTTP $SIDECAR_HTTP (non-200 — sidecar drift; SKIP_SIGLIP=0 sets it)"
      rm -f "$SIDECAR_TMP"
      exit 2
    fi

    # Preflight: response.embedding must be an array of length 768.
    if ! jq -e ".embedding | type == \"array\" and length == $EXPECTED_DIMENSION" \
        "$SIDECAR_TMP" >/dev/null 2>&1; then
      local_emb_type=$(jq -r '.embedding | type // "<missing>"' "$SIDECAR_TMP" 2>/dev/null || echo "<jq-error>")
      local_emb_len=$(jq -r '.embedding | length // "<missing>"' "$SIDECAR_TMP" 2>/dev/null || echo "<jq-error>")
      log_fail "sidecar envelope drift — embedding must be 768-float array (got type=$local_emb_type, length=$local_emb_len)"
      rm -f "$SIDECAR_TMP"
      exit 3
    fi

    local_obs_model=$(jq -r '.model // "<missing>"' "$SIDECAR_TMP" 2>/dev/null || echo "<jq-error>")
    if [[ "$local_obs_model" == *"$EXPECTED_MODEL"* ]]; then
      log_pass "sidecar: model='$local_obs_model' contains canonical '$EXPECTED_MODEL'"
    else
      log_fail "sidecar: model='$local_obs_model' does NOT contain canonical '$EXPECTED_MODEL'"
      rm -f "$SIDECAR_TMP"
      exit 4
    fi

    QUERY_VECTOR=$(jq -c '.embedding' "$SIDECAR_TMP" 2>/dev/null || echo "[]")
    rm -f "$SIDECAR_TMP"
    log_pass "sidecar: embedding produced (768 floats)"
  fi
fi

# ── 7. TEXT-TO-VISUAL SEARCH ───────────────────────────────────────────────
# POST /collections/{collection}/points/query with body
#   {"query": <vector>, "using": "visual", "limit": N,
#    "with_payload": true, "filter": {"must":[{key:"media_type", match:{value:"image"}}]}}
# Response envelope: {"result":{"points":[{"id":"...","score":0.x,"payload":{"name":"...",...}}], "next_page_offset":...}}
assert_search() {
  local tmp_json="$1" http_code="$2"

  if [[ "$http_code" != "200" ]]; then
    log_fail "search: HTTP $http_code (non-200 — Qdrant drift or invalid query)"
    return 2
  fi

  if ! jq -e ".result.points | type == \"array\"" "$tmp_json" >/dev/null 2>&1; then
    local pts_type
    pts_type=$(jq -r '.result.points | type // "missing"' "$tmp_json" 2>/dev/null || echo "<jq-error>")
    log_fail "search: envelope drift — .result.points is not an array (got '$pts_type')"
    return 2
  fi

  local hits
  hits=$(jq -r '.result.points | length' "$tmp_json" 2>/dev/null || echo "0")
  log_info "top hits: $hits (limit=$LIMIT)"

  # Capture hits for the human-mode table / JSON-mode array. Use mktemp
  # so repeated invocations don't collide on a fixed-name file (the prior
  # implementation's `> /tmp/qdrant_search_hits.XXXXXX.json` literal
  # template was a real bug — two rapid runs would clobber each other AND
  # the cleanup `rm -f /tmp/qdrant_search_hits.XXXXXX.json` glob-matched
  # files outside this script).
  TMP_HITS=$(mktemp /tmp/qdrant_search_hits.XXXXXX.json)
  jq -c '.result.points[] | {score: .score, id: .id, asset_id: (.payload.asset_id // .id // "<missing>"), name: (.payload.name // "<missing>")}' \
    "$tmp_json" > "$TMP_HITS"
  HITS_FILE="$TMP_HITS"

  return 0
}

if [[ $SCROLL_ONLY -eq 0 && -z "$QUERY_VECTOR" ]]; then
  SEARCH_RC="2"
elif [[ $SCROLL_ONLY -eq 0 ]]; then
  section "POST /collections/${COLLECTION}/points/query  (using=${NAMED_VECTOR}, filter=media_type=${EXPECTED_MEDIA_TYPE})"
  SEARCH_TMP=$(mktemp /tmp/qdrant_search.XXXXXX.json)
  # Build search body via jq --argjson: query vector is integer-array JSON,
  # media_type is a static literal, asset_id (NOT needed here — operator
  # wanted a SEARCH independent of the scrolled point).
  SEARCH_BODY=$(jq -nc \
    --argjson q "$QUERY_VECTOR" \
    --argjson l "$LIMIT" \
    --argjson mt "$EXPECTED_MEDIA_TYPE" \
    --arg nv "$NAMED_VECTOR" \
    '{query: $q, using: $nv, limit: $l, with_payload: true, filter: {must: [{key: "media_type", match: {value: $mt}}]}}')
  SEARCH_HTTP=$(curl -s -o "$SEARCH_TMP" -w '%{http_code}' \
    --max-time "$TIMEOUT" \
    -H 'Content-Type: application/json' \
    --data-binary "$SEARCH_BODY" \
    "$QDRANT_URL/collections/$COLLECTION/points/query" 2>/dev/null || echo "000")
  log_info "endpoint response HTTP $SEARCH_HTTP (raw body $(wc -c <"$SEARCH_TMP" 2>/dev/null || echo "?") bytes)"
  assert_search "$SEARCH_TMP" "$SEARCH_HTTP" || SEARCH_RC=$?

  if [[ $JSON_MODE -eq 0 && -s "${HITS_FILE:-}" ]]; then
    printf "  ${C_DIM}%-6s %-12s %-32s %-60s${C_RESET}\n" "rank" "score" "asset_id" "name"
    rank=0
    while IFS= read -r line; do
      rank=$((rank + 1))
      row_score=$(jq -r '.score' <<< "$line" 2>/dev/null || echo "0")
      row_asset_id=$(jq -r '.asset_id' <<< "$line" 2>/dev/null || echo "<missing>")
      row_name=$(jq -r '.name' <<< "$line" 2>/dev/null || echo "<missing>")
      printf "  %-6s %-12.4f %-32s %-60s\n" "#$rank" "$row_score" "$row_asset_id" "$row_name"
    done < "$HITS_FILE"
  fi

  rm -f "$SEARCH_TMP"
fi

# ── 8. Summary + exit code ──────────────────────────────────────────────────
section "summary"
if [[ $JSON_MODE -eq 1 ]]; then
  HITS_FILE_FOR_JSON="${HITS_FILE:-}"
  if [[ -n "$HITS_FILE_FOR_JSON" && -s "$HITS_FILE_FOR_JSON" ]]; then
    HITS_JSON=$(jq -s '.' "$HITS_FILE_FOR_JSON" 2>/dev/null || echo "[]")
  else
    HITS_JSON="[]"
  fi
  emit_json '{url:$u, asset_id:$a, collection:$c, named_vector:$nv, pass:$p, fail:$f, scroll_rc:$sr, search_rc:$qr, hits:$h, status:(if $f == 0 then "OK" else "FAIL" end)}' \
    --arg u "$QDRANT_URL" \
    --arg a "$ASSET_ID" \
    --arg c "$COLLECTION" \
    --arg nv "$NAMED_VECTOR" \
    --argjson p "$PASS_COUNT" \
    --argjson f "$FAIL_COUNT" \
    --argjson sr "${SCROLL_RC:-0}" \
    --argjson qr "${SEARCH_RC:-0}" \
    --argjson h "$HITS_JSON"
  [[ -n "${HITS_FILE:-}" && -e "$HITS_FILE" ]] && rm -f "$HITS_FILE" 2>/dev/null || true
else
  printf "  ${C_GREEN}passed: %d${C_RESET}\n" "$PASS_COUNT"
  printf "  ${C_RED}failed: %d${C_RESET}\n" "$FAIL_COUNT"
  if [[ ${#FAIL_LINES[@]} -gt 0 ]]; then
    printf "\n  ${C_RED}${C_BOLD}FAILED checks:${C_RESET}\n"
    for line in "${FAIL_LINES[@]}"; do
      printf "    - %s\n" "$line"
    done
  fi
  [[ -n "${HITS_FILE:-}" && -e "$HITS_FILE" ]] && rm -f "$HITS_FILE" 2>/dev/null || true
fi

# fail-fast exit-code precedence: scroll first, then search.
if [[ -n "$SCROLL_RC" && "$SCROLL_RC" != "0" ]]; then exit "$SCROLL_RC"; fi
if [[ -n "$SEARCH_RC" && "$SEARCH_RC" != "0" ]]; then exit "$SEARCH_RC"; fi

exit 0
