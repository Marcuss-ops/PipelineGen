#!/usr/bin/env bash
# voiceover_translated_drive_real_smoke.sh — 3-language voiceover end-to-end
# Drive verification (it-IT + en-US + pt-BR, project=yt-test-voiceover-drive).
# Per child SUCCEEDED: 4 Drive API assertions (id match / size>0 / parents
# contains {project}/{language} folder / webViewLink non-empty) = 12 total.
# Re-bashable via per-run TAG_PREFIX + REQ_ID. Run with --help for full
# env-var / exit-code docs. Fail signature: PR-VOICEOVER-DRIVE-DRIFT-FORWARD-POINTER.
# Co-authored-by: PipelineGen Agent <agent@pipelinegen.local>

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"

# Project-specific binaries (lib/common.sh already smoke_require'd jq)
smoke_require sqlite3 curl

# Help text (--help → full godoc, --dry → would-be probe plan)
if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    sed -n '2,12p' "$0"
    printf '\nFor full env-var / exit-code / signature docs see the source godoc.\n'
    exit 0
fi

# Dry-run mode
if [[ "$DRY_RUN" == "1" ]]; then
    smoke_echo_safe "DRY RUN — would probe:"
    printf '  GET  http://%s/health  (Go server up check)\n' "$SMOKE_API_BASE"
    printf '  READ %s  (Google OAuth access_token)\n' "${SMOKE_DRIVE_TOKEN_FILE:-<repo>/token.json}"
    printf '  GET  https://www.googleapis.com/drive/v3/about?fields=user  (preflight 401 probe)\n'
    printf '  POST http://%s/api/media/voiceover/generate  (3 items: it-IT+en-US+pt-BR, project=yt-test-voiceover-drive)\n' "$SMOKE_API_BASE"
    printf '  poll http://%s/api/jobs/<id>/full  (terminal)\n' "$SMOKE_API_BASE"
    printf '  for each child (3): sqlite3 + Drive API GET + 4 assertions (id/size/parents/webViewLink)\n'
    exit 0
fi

# ── Configuration ──────────────────────────────────────────────────
SMOKE_DB="${SMOKE_DB:?SMOKE_DB must be explicitly set to an isolated or approved database}"
SMOKE_DRIVE_TOKEN_FILE="${SMOKE_DRIVE_TOKEN_FILE:-${REPO_ROOT:-$(pwd)}/token.json}"
VELOX_DRIVE_VOICEOVER_ROOT="${VELOX_DRIVE_VOICEOVER_ROOT:-}"
DRIVE_API_BASE="https://www.googleapis.com"
TAG_PREFIX="vo_drive_smoke_$(date +%s)_$$"
RUN_ID="$(date -u +%Y-%m-%dT%H-%M-%SZ)"
REQ_ID="${TAG_PREFIX}_3lang"
PROJECT_ID="yt-test-voiceover-drive"

declare -a FAILURES=()
fail() { FAILURES+=("$1"); }

# Strict sqlite query (mirrors fase_b_clip_pipeline_smoke.sh::sqlite_q)
sqlite_q() {
    local out
    if ! out=$(sqlite3 -separator '|' "$SMOKE_DB" "$1" 2>/tmp/smoke_sqlite_err); then
        echo >&2 "DB query failed: sqlite3 exit non-zero with stderr:"
        cat >&2 /tmp/smoke_sqlite_err
        rm -f /tmp/smoke_sqlite_err
        exit 1
    fi
    rm -f /tmp/smoke_sqlite_err
    printf '%s' "$out"
}

# ── Setup guards ──────────────────────────────────────────────────
if [[ ! -f "$SMOKE_DB" ]]; then
    printf '%ssetup error: SMOKE_DB=%s does not exist%s\n' \
        "$RED" "$SMOKE_DB" "$RESET" >&2
    exit 2
fi
if [[ ! -f "$SMOKE_DRIVE_TOKEN_FILE" ]]; then
    printf '%ssetup error: SMOKE_DRIVE_TOKEN_FILE=%s not found — run `python3 scripts/generate_drive_token.py` first%s\n' \
        "$RED" "$SMOKE_DRIVE_TOKEN_FILE" "$RESET" >&2
    exit 2
fi
if [[ -z "$VELOX_DRIVE_VOICEOVER_ROOT" ]]; then
    printf '%ssetup error: VELOX_DRIVE_VOICEOVER_ROOT env var unset (the test needs a real Drive root folder_id so delivery.Publisher can resolve the {project}/{language}/ subdirs)%s\n' \
        "$RED" "$RESET" >&2
    exit 2
fi

# ── Preflight 1: Go server up (GET /health) ────────────────────
precheck_go_server_up() {
    smoke_log_section "Preflight 1: Go server up (GET /health)"
    local code
    code=$(smoke_curl GET "/health")
    if [[ ! "$code" =~ ^2[0-9][0-9]$ ]]; then
        fail "precheck_go_server_up_http_${code}"
        printf '%sFAIL: GET /health returned HTTP %s (expected 2xx)%s\n' \
            "$RED" "$code" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: GET /health → HTTP %s%s\n' "$GREEN" "$code" "$RESET"
    return 0
}

# ── Preflight 2: token.json + access_token present ──────────────
# The Google OAuth flow writes token.json with a top-level "access_token"
# field (per google-auth-oauthlib standard format). Empty file or missing
# field is a setup error: the smoke cannot proceed without a real token
# to call Drive API.
precheck_token_file() {
    smoke_log_section "Preflight 2: Google OAuth access_token (token.json)"
    local token
    if ! token=$(jq -r '.access_token // empty' "$SMOKE_DRIVE_TOKEN_FILE" 2>/dev/null); then
        fail "precheck_token_file_jq_parse"
        printf '%sFAIL: token.json at %s is not valid JSON%s\n' \
            "$RED" "$SMOKE_DRIVE_TOKEN_FILE" "$RESET" >&2
        return 1
    fi
    if [[ -z "$token" ]]; then
        fail "precheck_token_file_empty_access_token"
        printf '%sFAIL: token.json has no access_token field — re-run `python3 scripts/generate_drive_token.py`%s\n' \
            "$RED" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: token.json present, access_token length=%d%s\n' \
        "$GREEN" "${#token}" "$RESET"
    # Save token to scratch for the per-child loop (avoid re-parsing 3x)
    printf '%s' "$token" > "$WORK_DIR/drive_token"
    return 0
}

# ── Preflight 3: Drive API preflight 401 probe ──────────────────
# A valid token against the actual Drive API endpoint surfaces a 200 OK
# (with user info). A stale token returns 401. This preflight prevents
# the smoke from burning the full 180s poll loop on a stale token that
# would surface only at the per-child Drive API call.
precheck_drive_api_responsive() {
    smoke_log_section "Preflight 3: Drive API token validation (GET /drive/v3/about)"
    local token
    token=$(cat "$WORK_DIR/drive_token")
    local code
    code=$(curl -s --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" \
        -o /tmp/smoke_drive_about -w '%{http_code}' \
        -H "Authorization: Bearer $token" \
        "$DRIVE_API_BASE/drive/v3/about?fields=user" 2>/tmp/smoke_drive_about_err)
    if [[ "$code" == "200" ]]; then
        printf '  %sOK: Drive API responsive (token valid)%s\n' "$GREEN" "$RESET"
        return 0
    fi
    if [[ "$code" == "401" ]]; then
        fail "precheck_drive_api_401_stale_token"
        printf '%sFAIL: Drive API returned 401 — token expired or revoked; re-run `python3 scripts/generate_drive_token.py`%s\n' \
            "$RED" "$RESET" >&2
        return 1
    fi
    # Network errors etc. — surface as setup error
    fail "precheck_drive_api_unexpected_${code}"
    printf '%sFAIL: Drive API preflight returned HTTP %s (expected 200)%s\n' \
        "$RED" "$code" "$RESET" >&2
    return 1
}

# ── POST 3-language batch ───────────────────────────────────────
# 3 items: it-IT (DiegoNeural), en-US (GuyNeural), pt-BR (AntonioNeural).
# Filenames stamped with the per-run TAG_PREFIX to guarantee re-bash
# idempotency (no broker dedup collision across runs).
# project=yt-test-voiceover-drive threads through ToCommand() →
# GenerateVoiceoversCommand.Project → fanout loop → delivery.Publisher.Publish
# for the {project}/{language}/ subdir layout per the ThreadingCampaign.
post_3lang_batch() {
    smoke_log_section "POST /api/media/voiceover/generate (3 items: it-IT + en-US + pt-BR, project=$PROJECT_ID)"
    local payload
    payload=$(jq -n --arg rid "$REQ_ID" --arg vid "$TAG_PREFIX" --arg fid "$VELOX_DRIVE_VOICEOVER_ROOT" --arg proj "$PROJECT_ID" '{
        request_id: $rid,
        items: [
            {text: "Prima frase di prova smoke test.",                    language: "it-IT", voice: "it-IT-DiegoNeural",   filename: ("smoke_it_" + $vid + ".mp3"), required: true},
            {text: "First sentence of the smoke test.",                  language: "en-US", voice: "en-US-GuyNeural",     filename: ("smoke_en_" + $vid + ".mp3"), required: true},
            {text: "Terceira frase do teste de fumaca.",                 language: "pt-BR", voice: "pt-BR-AntonioNeural", filename: ("smoke_pt_" + $vid + ".mp3"), required: true}
        ],
        destination: {kind: "explicit", folder_id: $fid},
        project: $proj,
        options: {remove_silence: false, strategy: "verify", parallelism: 3}
    }')

    local code
    code=$(smoke_curl POST "/api/media/voiceover/generate" --data "$payload")
    if ! smoke_assert_http_2xx "POST /api/media/voiceover/generate"; then
        fail "post_3lang_batch_http_${SMOKE_LAST_HTTP}"
        return 1
    fi
    JOB_ID=$(jq -r '.job_id // .id // empty' "$SMOKE_LAST_BODY" 2>/dev/null || echo "")
    if [[ -z "$JOB_ID" ]]; then
        fail "post_3lang_batch_no_job_id_in_response"
        printf '%sFAIL: POST returned no job_id in body%s\n' "$RED" "$RESET" >&2
        return 1
    fi
    printf '  %senqueued parent job_id=%s (correlation_id=%s, project=%s)%s\n' \
        "$GREEN" "$JOB_ID" "$REQ_ID" "$PROJECT_ID" "$RESET"
    return 0
}

# ── Poll parent to terminal ─────────────────────────────────────
# Tolerance window: SMOKE_POLL_TIMEOUT_SECONDS (default 180). 3 children
# in parallel: each child does TTS (~1-3s) + Drive upload (~2-4s) +
# DB commit (~0.1s) = ~4-8s per child. With 3 siblings in parallel,
# wall-clock is ~8-12s + parent aggregator tick (~5s). 180s is
# comfortable; tighten for CI only if production is consistently faster.
poll_parent_to_terminal() {
    smoke_log_section "Poll parent to terminal (timeout ${SMOKE_POLL_TIMEOUT_SECONDS}s)"
    if ! smoke_poll_terminal "$JOB_ID"; then
        fail "poll_parent_to_terminal_rc_$?"
        printf '%sFAIL: parent job %s did not reach terminal in %ss (last status=%s)%s\n' \
            "$RED" "$JOB_ID" "$SMOKE_POLL_TIMEOUT_SECONDS" "${SMOKE_LAST_STATUS:-?}" "$RESET" >&2
        return 1
    fi
    if [[ "$SMOKE_LAST_STATUS" != "completed" && "$SMOKE_LAST_STATUS" != "SUCCEEDED" ]]; then
        fail "poll_parent_to_terminal_status_${SMOKE_LAST_STATUS}"
        printf '%sFAIL: parent terminal status=%s (expected completed/SUCCEEDED)%s\n' \
            "$RED" "$SMOKE_LAST_STATUS" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: parent reached terminal status=%s%s\n' "$GREEN" "$SMOKE_LAST_STATUS" "$RESET"
    return 0
}

# ── Per-child Drive verification ───────────────────────────────
# For each of the 3 voiceovers rows (filtered by request_id), call the
# Drive API and assert the 4 facts. Failures map to the canonical
# PR-VOICEOVER-DRIVE-DRIFT-FORWARD-POINTER per godlike/06 one-fact-
# one-owner; the per-fact sub-label discriminates the failure class
# (id mismatch, zero size, parent mismatch, missing webViewLink).
verify_child_on_drive() {
    local child_id="$1"   # voiceover row id
    local drive_file_id="$2"
    local folder_id="$3"  # expected parent folder (the {project}/{language} subdir)
    local language="$4"
    local api_url="$DRIVE_API_BASE/drive/v3/files/${drive_file_id}?fields=id,name,mimeType,size,parents,webViewLink"
    local token
    token=$(cat "$WORK_DIR/drive_token")

    local code
    code=$(curl -s --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" \
        -o "$WORK_DIR/drive_${child_id}.json" -w '%{http_code}' \
        -H "Authorization: Bearer $token" \
        "$api_url" 2>/tmp/smoke_drive_err)
    if [[ "$code" != "200" ]]; then
        fail "drive_api_404_or_500_id_mismatch_lang_${language}_code_${code}"
        printf '%s  FAIL: Drive API GET returned HTTP %s for child_id=%s (language=%s, db.drive_file_id=%s)%s\n' \
            "$RED" "$code" "$child_id" "$language" "$drive_file_id" "$RESET" >&2
        if [[ -s /tmp/smoke_drive_err ]]; then
            cat /tmp/smoke_drive_err >&2
        fi
        return 1
    fi
    local body
    body=$(cat "$WORK_DIR/drive_${child_id}.json")
    local drive_id drive_size parents_csv webviewlink
    drive_id=$(printf '%s' "$body" | jq -r '.id // empty')
    drive_size=$(printf '%s' "$body" | jq -r '.size // 0')
    parents_csv=$(printf '%s' "$body" | jq -r '.parents // [] | join(",")')
    webviewlink=$(printf '%s' "$body" | jq -r '.webViewLink // empty')

    # A1: ID match
    if [[ "$drive_id" != "$drive_file_id" ]]; then
        fail "drive_id_mismatch_lang_${language}_db_${drive_file_id}_api_${drive_id}"
        printf '%s  FAIL: A1 (ID match) for language=%s: db=%s vs drive=%s%s\n' \
            "$RED" "$language" "$drive_file_id" "$drive_id" "$RESET" >&2
        return 1
    fi
    # A2: size > 0. Drive returns size="0" transiently during finalization —
    # retry once with 2s backoff before failing (the canonical 12-assertion
    # contract is "non-zero bytes" not "immediate non-zero bytes").
    if [[ -z "$drive_size" || "$drive_size" == "0" || "$drive_size" == "null" ]]; then
        sleep 2
        local code_retry
        code_retry=$(curl -s --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" \
            -o "$WORK_DIR/drive_${child_id}.json" -w '%{http_code}' \
            -H "Authorization: Bearer $token" \
            "$api_url" 2>/tmp/smoke_drive_err)
        if [[ "$code_retry" == "200" ]]; then
            body=$(cat "$WORK_DIR/drive_${child_id}.json")
            drive_size=$(printf '%s' "$body" | jq -r '.size // 0')
        fi
    fi
    if [[ -z "$drive_size" || "$drive_size" == "0" || "$drive_size" == "null" ]]; then
        fail "drive_size_zero_lang_${language}_file_id_${drive_file_id}"
        printf '%s  FAIL: A2 (size>0) for language=%s: drive.size=%s%s\n' \
            "$RED" "$language" "${drive_size:-empty}" "$RESET" >&2
        return 1
    fi
    # A3: parents contains folder_id
    if [[ ",${parents_csv}," != *",${folder_id},"* ]]; then
        fail "drive_parents_mismatch_lang_${language}_expected_${folder_id}_got_${parents_csv}"
        printf '%s  FAIL: A3 (parents contains {project}/{language} folder) for language=%s: expected=%s, got=[%s]%s\n' \
            "$RED" "$language" "$folder_id" "$parents_csv" "$RESET" >&2
        return 1
    fi
    # A4: webViewLink non-empty
    if [[ -z "$webviewlink" ]]; then
        fail "drive_webviewlink_empty_lang_${language}_file_id_${drive_file_id}"
        printf '%s  FAIL: A4 (webViewLink non-empty) for language=%s%s\n' \
            "$RED" "$language" "$RESET" >&2
        return 1
    fi
    printf '  %sOK: language=%s file_id=%s size=%s parents=[%s] webViewLink=%s…%s\n' \
        "$GREEN" "$language" "$drive_id" "$drive_size" "$parents_csv" "${webviewlink:0:60}" "$RESET"
    return 0
}

# ── 7-step loop per child ──────────────────────────────────────
# Single loop covering all 3 children: query voiceovers → Drive API →
# 4 assertions per child. Aggregate verdict at the end.
verify_all_children_on_drive() {
    smoke_log_section "7-step loop: 3 children × (DB read + Drive API + 4 assertions)"

    # Pull the 3 voiceovers rows: child_id, drive_file_id, folder_id, language.
    # Ordered by language for deterministic output (it-IT, en-US, pt-BR).
    # Single SQL with strict stderr capture (mirrors the b2 smoke pattern).
    local rows
    rows=$(sqlite_q "SELECT id || '|' || COALESCE(drive_file_id, '') || '|' || COALESCE(folder_id, '') || '|' || language FROM voiceovers WHERE request_id = '${REQ_ID}' AND drive_file_id != '' AND folder_id != '' ORDER BY language")
    if [[ -z "$rows" ]]; then
        fail "no_voiceovers_with_drive_file_id_for_request_${REQ_ID}"
        printf '%sFAIL: no voiceovers rows with non-empty drive_file_id+folder_id for request_id=%s%s\n' \
            "$RED" "$REQ_ID" "$RESET" >&2
        return 1
    fi
    local count
    count=$(printf '%s\n' "$rows" | wc -l | tr -d ' ')
    if [[ "$count" != "3" ]]; then
        fail "voiceovers_with_drive_link_count_${count}_expected_3"
        printf '%sFAIL: %s voiceovers rows with non-empty drive_file_id+folder_id (expected 3)%s\n' \
            "$RED" "$count" "$RESET" >&2
        return 1
    fi

    local child_ok=0 child_fail=0
    while IFS='|' read -r child_id drive_file_id folder_id language; do
        [[ -z "$child_id" ]] && continue
        if verify_child_on_drive "$child_id" "$drive_file_id" "$folder_id" "$language"; then
            child_ok=$((child_ok + 1))
        else
            child_fail=$((child_fail + 1))
        fi
    done <<< "$rows"
    printf '  child verification summary: ok=%s fail=%s (target: ok=3, fail=0)\n' \
        "$child_ok" "$child_fail"
    if [[ "$child_fail" -gt 0 ]]; then
        return 1
    fi
    return 0
}

main() {
    smoke_log_section "Voiceover 3-language translated Drive real smoke"
    printf '  target:        %s\n  db:            %s\n  project:       %s\n  voice_root:    %s\n  token_file:    %s\n  tag:           %s\n  run_id:        %s\n  req_id:        %s\n' \
        "$SMOKE_API_BASE" "$SMOKE_DB" "$PROJECT_ID" "$VELOX_DRIVE_VOICEOVER_ROOT" \
        "$SMOKE_DRIVE_TOKEN_FILE" "$TAG_PREFIX" "$RUN_ID" "$REQ_ID"

    # Preflight (fail-fast before any state-mutating call).
    # Each precheck prints its own error and records a specific tag in
    # FAILURES; we propagate the return codes explicitly (no `|| true`
    # masking) so the aggregate gate below is the single authority.
    local preflight_rc=0
    precheck_go_server_up || preflight_rc=1
    precheck_token_file || preflight_rc=1
    precheck_drive_api_responsive || preflight_rc=1

    if (( preflight_rc != 0 || ${#FAILURES[@]} > 0 )); then
        printf '%sFAIL: precheck(s) failed, aborting before POST%s\n' "$RED" "$RESET" >&2
        printf '  failures:\n'
        for f in "${FAILURES[@]}"; do
            printf '    - %s\n' "$f" >&2
        done
        printf '\n  All Drive drift failures map to the canonical:\n'
        printf '    PR-VOICEOVER-DRIVE-DRIFT-FORWARD-POINTER\n'
        exit 1
    fi

    # Happy path
    post_3lang_batch || { fail "post_3lang_batch"; exit 1; }
    poll_parent_to_terminal || { fail "poll_parent_to_terminal"; }

    # 3 children × 4 Drive assertions. verify_all_children_on_drive
    # records per-child tags into FAILURES and returns non-zero on any
    # child failure; propagate that rc explicitly (no `|| true` masking)
    # so the aggregate report below remains the single authority.
    local verify_rc=0
    verify_all_children_on_drive || verify_rc=1

    echo
    if (( verify_rc == 0 && ${#FAILURES[@]} == 0 )); then
        printf '%sOK: voiceover 3-language Drive real smoke PASS (12/12 Drive assertions across 3 children)%s\n' \
            "$GREEN" "$RESET"
        exit 0
    fi
    printf '%sFAIL: %d assertion(s) failed:%s\n' \
        "$RED" "${#FAILURES[@]}" "$RESET" >&2
    for f in "${FAILURES[@]}"; do
        printf '  - %s\n' "$f" >&2
    done
    printf '\n  All Drive drift failures map to the canonical:\n'
    printf '    PR-VOICEOVER-DRIVE-DRIFT-FORWARD-POINTER\n'
    exit 1
}
main "$@"
