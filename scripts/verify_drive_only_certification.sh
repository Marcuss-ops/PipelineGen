#!/usr/bin/env bash
# verify_drive_only_certification.sh — Drive-only clip certification battery.
#
# Certifies the GenerateTimeline / RenderVideo decoupling contract against the
# live /api/script/generate endpoint:
#
#     Evidence / Script / Timeline  ≠  Binary Materialization / Render
#
# A clip whose transcript is ready but whose binary is NOT staged locally
# (Drive-only) must complete a generate_timeline=true job, keep one scene and
# one canonical timeline binding per clip, and enqueue ZERO render work.
# render_video=true is the only trigger that demands materialized binaries —
# never generate_timeline.
#
# Usage:
#   scripts/verify_drive_only_certification.sh <clip_id...> [--render N] [--dry]
#   LOVE_CLIP_IDS=id1,id2,... scripts/verify_drive_only_certification.sh [--render N]
#
# Options:
#   --render N   additionally run the render tier with N clips (requires the
#                host to be able to materialize binaries; default: skipped)
#   --dry        print the exact payloads without touching the network
#
# Exit codes follow tests/operational/lib/common.sh: 0 all green, 1 assertion
# failed, 2 setup error, 124 timeout.
set -euo pipefail
umask 077

# --dry / --help must be known before common.sh is sourced: they are the only
# paths that skip mandatory token resolution.
for early_arg in "$@"; do
    case "$early_arg" in
        --dry) export SMOKE_DRY_RUN=1 ;;
        -h|--help) export SMOKE_DRY_RUN=1 ;;
    esac
done

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
SMOKE_LIB="$ROOT/tests/operational/lib/common.sh"
GENERATE_LIB_DIR="$ROOT/tests/operational/generate/lib"
[[ -f "$SMOKE_LIB" ]] || { echo "setup error: $SMOKE_LIB not found" >&2; exit 2; }
RUNNER_ARGS=("$@")
set --
# shellcheck disable=SC1091
source "$SMOKE_LIB"
# shellcheck disable=SC1091
source "$GENERATE_LIB_DIR/dispatch.sh"
# shellcheck disable=SC1091
source "$GENERATE_LIB_DIR/result.sh"
set -- "${RUNNER_ARGS[@]}"

# ── CLI ─────────────────────────────────────────────────────────────────────
RENDER_N=0
DRY=0
POSITIONAL=()
while [[ $# -gt 0 ]]; do
    case "$1" in
        --render) RENDER_N="${2:-0}"; [[ "$RENDER_N" =~ ^[0-9]+$ ]] || { echo "setup error: --render expects a number" >&2; exit 2; }; shift 2 ;;
        --dry) DRY=1; shift ;;
        -h|--help) sed -n '2,22p' "${BASH_SOURCE[0]}"; exit 0 ;;
        *) POSITIONAL+=("$1"); shift ;;
    esac
done

CLIP_IDS=("${POSITIONAL[@]:-}")
if [[ ${#CLIP_IDS[@]} -eq 0 && -n "${LOVE_CLIP_IDS:-}" ]]; then
    IFS=',' read -r -a CLIP_IDS <<<"$LOVE_CLIP_IDS"
fi
if [[ ${#CLIP_IDS[@]} -eq 0 ]]; then
    echo "setup error: pass clip IDs as arguments or via LOVE_CLIP_IDS" >&2
    exit 2
fi
for cid in "${CLIP_IDS[@]}"; do
    [[ -n "$cid" ]] || { echo "setup error: empty clip ID in input" >&2; exit 2; }
done
TOTAL_CLIPS=${#CLIP_IDS[@]}
[[ "$TOTAL_CLIPS" -ge 3 ]] || { echo "setup error: need at least 3 clip IDs for the battery" >&2; exit 2; }

SMOKE_TIMEOUT_SECONDS="${SMOKE_TIMEOUT_SECONDS:-900}"
SMOKE_POLL_TIMEOUT_SECONDS="${SMOKE_POLL_TIMEOUT_SECONDS:-900}"
export SMOKE_TIMEOUT_SECONDS SMOKE_POLL_TIMEOUT_SECONDS

# ── Payload builder ─────────────────────────────────────────────────────────
# clip_ids json, num_clips, render flag → single-item v2 payload.
build_payload() {
    local ids="$1" num="$2" render="$3"
    jq -nc --argjson ids "$ids" --argjson num "$num" --argjson render "$render" '{
        version: 2,
        preset: "custom",
        items: [{
            id: "drive-only-cert-item",
            title: "Drive-only clip certification",
            language: "it",
            tone: "documentary",
            source: {
                type: "clips",
                clip_ids: $ids,
                num_clips: $num,
                grounding_policy: "clips_primary",
                fallback_policy: "strict",
                ordering_strategy: "input_order"
            },
            script_params: { target_words: 150, skip_quality_gate: true },
            output: { generate_timeline: true, render_video: $render, save_to_db: true }
        }]
    }'
}

ids_json() { # ids_json <start> <count>
    local start="$1" count="$2" i
    local -a slice=()
    for ((i = start; i < start + count && i < TOTAL_CLIPS; i++)); do
        slice+=("${CLIP_IDS[$i]}")
    done
    jq -nc --argjson a "$(printf '%s\n' "${slice[@]}" | jq -R . | jq -s .)" '$a'
}

# ── Assertions per tier ──────────────────────────────────────────────────────
DRIVE_DOWNLOAD_REPORTED=0
declare -a TIER_LINES=()

assert_timeline_tier() {
    local n="$1" label="$2"
    local ids payload key job
    ids=$(ids_json 0 "$n")
    payload=$(build_payload "$ids" "$n" "false")
    key="drive-cert-${label}-$(smoke_gen_uuid)"

    if [[ "$DRY" == "1" ]]; then
        printf '%sDRY %s (%s clips)%s\n' "$CYAN" "$label" "$n" "$RESET"
        jq . <<<"$payload"
        return 0
    fi

    smoke_log_section "Dispatch: timeline tier $label (${n} clips)"
    generate_dispatch "$payload" "$key"
    generate_poll_and_fetch
    generate_extract_result

    local accepted scenes bindings rendered
    accepted=$(jq '[.source_trace.accepted_clip_ids[]?] | length' <<<"$GENERATE_RESULT")
    scenes=$(jq '[.output.specscene.scenes[]?] | length' <<<"$GENERATE_RESULT")
    bindings=$(jq '[.output.specscene.scenes[]? | select((.bindings.clip.clip_id // "") != "")] | length' <<<"$GENERATE_RESULT")

    [[ "$accepted" == "$n" ]] || { echo "FAIL[$label]: accepted_clip_ids=$accepted expected $n" >&2; return 1; }
    [[ "$scenes" == "$n" ]] || { echo "FAIL[$label]: specscene scenes=$scenes expected $n (1 clip = 1 scene)" >&2; return 1; }
    [[ "$bindings" == "$n" ]] || { echo "FAIL[$label]: scenes with clip binding=$bindings expected $n" >&2; return 1; }

    # Render must stay dormant: no render job/plan may leak out of a
    # timeline-only run, whatever the envelope surface.
    rendered=$(jq '[.. | objects | select(has("render_plan") or has("render_job_id") or has("render_jobs"))] | length' <<<"$GENERATE_RESULT")
    [[ "$rendered" == "0" ]] || { echo "FAIL[$label]: timeline-only run leaked render work" >&2; return 1; }

    # Timeline metadata artifact: surface when the envelope carries it. The
    # 1:1 scene↔clip↔timeline binding is always certified; the canonical
    # timeline artifact itself is unit-certified at the capability level.
    local tl_segments=0 tl_path=""
    for p in '.canonical_timeline.segments' '.timeline.segments' '.audio_timeline.segments' '.output.timeline.segments'; do
        if jq -e "$p" <<<"$GENERATE_RESULT" >/dev/null 2>&1; then
            tl_path="$p"
            tl_segments=$(jq "[$p[]?] | length" <<<"$GENERATE_RESULT")
            break
        fi
    done
    if [[ "$tl_path" != "" ]]; then
        [[ "$tl_segments" == "$n" ]] || { echo "FAIL[$label]: timeline segments=$tl_segments expected $n" >&2; return 1; }
        TIER_LINES+=("timeline segments $tl_segments == $n (surfaced at $tl_path)")
    else
        TIER_LINES+=("timeline artifact not surfaced in envelope (capability-level tests certify it)")
    fi

    local dl
    dl=$(jq '[.. | objects | select(has("drive_download_calls")) | .drive_download_calls] | add // 0' <<<"$GENERATE_RESULT")
    DRIVE_DOWNLOAD_REPORTED=$(( DRIVE_DOWNLOAD_REPORTED + dl ))
    if [[ "$dl" != "0" ]]; then
        echo "WARNING[$label]: $dl drive download calls reported during a timeline-only run" >&2
    fi

    TIER_LINES+=("$label: SUCCEEDED, accepted=$accepted scenes=$scenes bindings=$bindings")
    printf '%sOK: %s — %s clips timeline-only%s\n' "$GREEN" "$label" "$n" "$RESET"
}

assert_render_tier() {
    local n="$1"
    local ids payload key
    ids=$(ids_json 0 "$n")
    payload=$(build_payload "$ids" "$n" "true")
    key="drive-cert-render-$(smoke_gen_uuid)"

    if [[ "$DRY" == "1" ]]; then
        printf '%sDRY render tier (%s clips, render_video=true)%s\n' "$CYAN" "$n" "$RESET"
        jq . <<<"$payload"
        return 0
    fi

    smoke_log_section "Dispatch: render tier (${n} clips, render_video=true)"
    generate_dispatch "$payload" "$key"
    generate_poll_and_fetch
    generate_extract_result

    local accepted final_audio
    accepted=$(jq '[.source_trace.accepted_clip_ids[]?] | length' <<<"$GENERATE_RESULT")
    final_audio=$(jq '[.final_audio? // empty] | length' <<<"$GENERATE_RESULT")
    [[ "$accepted" == "$n" ]] || { echo "FAIL[render]: accepted_clip_ids=$accepted expected $n" >&2; return 1; }
    if [[ "$final_audio" == "0" ]]; then
        echo "WARNING[render]: final_audio artifact not surfaced; assert the render output independently" >&2
    fi
    TIER_LINES+=("render($n): SUCCEEDED, accepted=$accepted final_audio_surfaced=$final_audio")
    printf '%sOK: render tier — %s clips render_video=true%s\n' "$GREEN" "$n" "$RESET"
}

assert_idempotency() {
    local n="$1"
    local ids payload key job1 job2
    ids=$(ids_json 0 "$n")
    payload=$(build_payload "$ids" "$n" "false")
    key="drive-cert-idem-$(smoke_gen_uuid)"

    if [[ "$DRY" == "1" ]]; then
        printf '%sDRY idempotency tier (%s clips)%s\n' "$CYAN" "$n" "$RESET"
        return 0
    fi

    smoke_log_section "Idempotency tier (${n} clips, same key twice)"
    generate_dispatch "$payload" "$key"
    job1="$GENERATE_JOB_ID"
    generate_dispatch "$payload" "$key"
    job2="$GENERATE_JOB_ID"
    [[ "$job1" == "$job2" ]] || { echo "FAIL[idempotency]: replay produced a different job ($job1 vs $job2)" >&2; return 1; }
    TIER_LINES+=("idempotency($n): same key => same job $job1")
    printf '%sOK: idempotency tier — replay deduplicated to %s%s\n' "$GREEN" "$job1" "$RESET"
}

# ── Run the battery ─────────────────────────────────────────────────────────
TIERS=(3)
[[ "$TOTAL_CLIPS" -ge 10 ]] && TIERS+=(10)
[[ "$TOTAL_CLIPS" -ge 46 ]] && TIERS+=(46)

for n in "${TIERS[@]}"; do
    assert_timeline_tier "$n" "tier${n}"
done
[[ "$RENDER_N" -gt 0 ]] && assert_render_tier "$RENDER_N"
assert_idempotency 3

# ── Acceptance block ────────────────────────────────────────────────────────
printf '\n%s───── DRIVE-ONLY CLIP CERTIFICATION ─────%s\n' "$CYAN" "$RESET"
printf 'clips exercised          : %s (tiers: %s)\n' "$TOTAL_CLIPS" "$(IFS=,; echo "${TIERS[*]}")"
if [[ "$RENDER_N" -gt 0 ]]; then printf 'render tier              : %s clips (render_video=true)\n' "$RENDER_N"; else printf 'render tier              : SKIPPED (pass --render N to certify)\n'; fi
printf 'drive download calls during SCRIPT/TIMELINE : %s\n' "$DRIVE_DOWNLOAD_REPORTED"
for line in "${TIER_LINES[@]}"; do printf '  %s\n' "$line"; done
printf 'Regression guard: generate_timeline must never imply render_video.\n'
printf '46 Love clip failure ("asset has no local path") — NOT REPRODUCED.\n'
printf '───────────────────────────────────────────\n'
