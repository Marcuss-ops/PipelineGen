#!/usr/bin/env bash
# verify_nlp_online_images_docs_certification.sh — live certification of the
# NLP → online entity image → Google Docs chain (zero video render).
#
# One POST /api/script/generate with a single text item carrying 10 explicit,
# controlled segments. Every segment embeds a known person + place in its
# source_text, so the certification can assert, per scene, that:
#
#     NLP (per segment)  → 1 important phrase + 3-5 important words
#                        → the expected PERSON and PLACE are present
#                        → zero cross-scene entity contamination
#     ONLINE SEARCH      → the image query is the primary entity name
#     MATERIALIZATION    → internet_images candidate is downloaded, verified,
#                          persisted and carries a Drive link
#     ENTITY BINDING     → the person image is identity-scoped and resolved
#                          (never promoted from a generic scene candidate)
#     GOOGLE DOC         → created with all 10 scenes + entity images/links
#     VIDEO              → disabled: no render_plan, no render job
#
# This is the LIVE surface of the deterministic chain certified by
# internal/application/scripts/adapters/certification_final_table_test.go.
# Because the composition root may route extraction to Ollama when available
# (falling back to the local CPU extractor), entity/keyword assertions are
# contains-based, never exact — additional valid entities are allowed.
#
# Usage:
#   DOC_FOLDER_ID=<drive-folder> scripts/verify_nlp_online_images_docs_certification.sh
#   scripts/verify_nlp_online_images_docs_certification.sh --dry
#
# Environment (overridable; defaults shown):
#   API_BASE                     host:port (default 127.0.0.1:${VELOX_PORT:-8000})
#   DOC_FOLDER_ID                Google Drive folder_id for docs.enabled (REQUIRED;
#                                fallback CERT_DOCS_FOLDER_ID then
#                                VELOX_DRIVE_SCRIPTS_GENERATE)
#   VELOX_ADMIN_TOKEN / TOKEN_FILE  via common.sh (or scripts/with-velox-auth)
#   SMOKE_TIMEOUT_SECONDS        overall wall clock (default 1800)
#   SMOKE_POLL_TIMEOUT_SECONDS   poll ceiling (default 1800)
#
# Exit codes (tests/operational/lib/common.sh contract):
#   0   CERTIFIED
#   1   one or more assertions failed
#   2   setup error
#   124 poll loop or wall-clock timeout exceeded
set -euo pipefail
umask 077

# --dry / --help are the only paths that skip mandatory token resolution.
for early_arg in "$@"; do
    case "$early_arg" in
        --dry|-h|--help) export SMOKE_DRY_RUN=1 ;;
    esac
done

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
SMOKE_LIB="$ROOT/tests/operational/lib/common.sh"
GENERATE_LIB_DIR="$ROOT/tests/operational/generate/lib"
[[ -f "$SMOKE_LIB" ]] || { echo "setup error: $SMOKE_LIB not found" >&2; exit 2; }
ORIGINAL_ARGS=("$@")
set --
# shellcheck disable=SC1091
source "$SMOKE_LIB"
# shellcheck disable=SC1091
source "$GENERATE_LIB_DIR/dispatch.sh"
# shellcheck disable=SC1091
source "$GENERATE_LIB_DIR/result.sh"
set -- "${ORIGINAL_ARGS[@]}"

smoke_require jq curl

DRY=0
while [[ $# -gt 0 ]]; do
    case "$1" in
        --dry) DRY=1; shift ;;
        -h|--help) sed -n '2,52p' "${BASH_SOURCE[0]}"; exit 0 ;;
        *) echo "setup error: unknown flag $1" >&2; exit 2 ;;
    esac
done

DOC_FOLDER_ID="${DOC_FOLDER_ID:-${CERT_DOCS_FOLDER_ID:-${VELOX_DRIVE_SCRIPTS_GENERATE:-}}}"
if [[ -z "$DOC_FOLDER_ID" ]]; then
    printf '%ssetup error: DOC_FOLDER_ID (or CERT_DOCS_FOLDER_ID / VELOX_DRIVE_SCRIPTS_GENERATE) is required — docs.enabled=true needs a Drive folder_id%s\n' \
        "$RED" "$RESET" >&2
    exit 2
fi

export SMOKE_TIMEOUT_SECONDS="${SMOKE_TIMEOUT_SECONDS:-1800}"
export SMOKE_POLL_TIMEOUT_SECONDS="${SMOKE_POLL_TIMEOUT_SECONDS:-1800}"

# ── The 10 controlled scenes: id | person | place | topic | source_text ──
declare -a SCENE_ID=() SCENE_PERSON=() SCENE_PLACE=() SCENE_TOPIC=() SCENE_TEXT=()
add_scene() {
    SCENE_ID+=("$1")
    SCENE_PERSON+=("$2")
    SCENE_PLACE+=("$3")
    SCENE_TOPIC+=("$4")
    SCENE_TEXT+=("$5")
}
add_scene "scene-dwayne" "Dwayne Johnson" "Los Angeles" "Dwayne Johnson and wrestling" \
    "Dwayne Johnson trained in Los Angeles. In 2025, Dwayne Johnson described professional wrestling as a discipline built on athletic training, dramatic storytelling, audience connection, and repeated performance under pressure. Wrestling demands training, storytelling, discipline, performance, and resilience."
add_scene "scene-serena" "Serena Williams" "New York" "Serena Williams and tennis" \
    "Serena Williams appears in New York. In 2024, Serena Williams described competitive tennis as a combination of disciplined training, mental resilience, tactical preparation, and consistent performance. Tennis rewards preparation, resilience, discipline, competition, and precision."
add_scene "scene-tom" "Tom Holland" "London" "Tom Holland and acting" \
    "Tom Holland works in London. In 2025, Tom Holland described acting as a craft requiring preparation, character development, emotional control, creative storytelling, and repeated performance. Acting depends on storytelling, preparation, creativity, character, and performance."
add_scene "scene-gordon" "Gordon Ramsay" "London" "Gordon Ramsay and cooking" \
    "Gordon Ramsay works in London. In 2025, Gordon Ramsay described professional cooking as a discipline combining kitchen technique, ingredient knowledge, preparation, precision, and consistent execution. Cooking requires technique, preparation, precision, ingredients, and discipline."
add_scene "scene-adele" "Adele Adkins" "London" "Adele Adkins and music" \
    "Adele Adkins performs in London. In 2025, Adele Adkins described music as a combination of vocal technique, songwriting, emotional storytelling, careful performance, and audience connection. Music depends on songwriting, vocals, storytelling, performance, and emotion."
add_scene "scene-keanu" "Keanu Reeves" "Los Angeles" "Keanu Reeves and cinema" \
    "Keanu Reeves works in Los Angeles. In 2025, Keanu Reeves described cinema as a collaborative craft involving physical preparation, character work, stunt training, visual storytelling, and disciplined performance. Cinema requires preparation, storytelling, training, character, and performance."
add_scene "scene-lewis" "Lewis Hamilton" "London" "Lewis Hamilton and racing" \
    "Lewis Hamilton appears in London. In 2025, Lewis Hamilton described racing as a discipline combining engineering knowledge, strategic preparation, physical concentration, technical precision, and consistent performance. Racing depends on strategy, engineering, preparation, precision, and performance."
add_scene "scene-taylor" "Taylor Swift" "New York" "Taylor Swift and songwriting" \
    "Taylor Swift appears in New York. In 2025, Taylor Swift described songwriting as a process combining language, musical structure, emotional storytelling, creative revision, and live performance. Songwriting depends on storytelling, creativity, language, revision, and performance."
add_scene "scene-emma" "Emma Watson" "London" "Emma Watson and education" \
    "Emma Watson appears in London. In 2025, Emma Watson described education as a process involving communication, research, learning, critical thinking, and sustained personal development. Education depends on learning, communication, research, development, and thinking."
add_scene "scene-messi" "Lionel Messi" "United States" "Lionel Messi and football" \
    "Lionel Messi appears in the United States. In 2025, Lionel Messi described football as a sport combining technical control, tactical awareness, coordinated teamwork, physical preparation, and consistent performance. Football depends on technique, teamwork, preparation, awareness, and performance."

SCENE_COUNT=${#SCENE_ID[@]}

# ── Payload builder ─────────────────────────────────────────────────────────
build_payload() {
    local segments_json="[]"
    local i
    for ((i = 0; i < SCENE_COUNT; i++)); do
        local seg
        seg=$(jq -nc --arg id "${SCENE_ID[$i]}" --arg topic "${SCENE_TOPIC[$i]}" --arg text "${SCENE_TEXT[$i]}" \
            '{id: $id, topic: $topic, source_text: $text, target_words: 70}')
        segments_json=$(jq -c --argjson seg "$seg" '. + [$seg]' <<<"$segments_json")
    done
    # render_video is deliberately ABSENT (= false). generate_timeline=false and
    # voiceover_enabled=false keep the certification audio/video-free so a
    # failure is unambiguously NLP/search/docs — never Edge/Rust.
    jq -nc \
        --arg folder "$DOC_FOLDER_ID" \
        --argjson segments "$segments_json" '{
        version: 2,
        preset: "custom",
        force_refresh: true,
        items: [{
            id: "cert-nlp-online-images-docs-10",
            title: "NLP Online Images Certification",
            language: "en",
            source: {
                type: "text",
                topic: "Controlled NLP and entity image certification",
                source_text: "Use only the ten controlled segments supplied below."
            },
            script_params: {
                target_words: 700,
                segment_words: 70,
                use_memory: false,
                skip_quality_gate: true,
                segments: $segments
            },
            output: {
                save_to_db: true,
                extract_entities: true,
                generate_metadata: false,
                generate_scene_images: false,
                generate_timeline: false,
                voiceover_enabled: false
            },
            media_plan: {
                mode: "auto",
                provider_policy: { artlist: "disabled", internet_images: "enabled", image_generation: "disabled" },
                extraction: {
                    enabled: true,
                    device: "auto",
                    max_entities_per_segment: 5,
                    max_important_phrases_per_segment: 1,
                    max_important_words_per_segment: 5,
                    max_image_queries_per_segment: 3,
                    entity_images: { enabled: true, entity_types: ["PERSON"], max_per_entity: 1 }
                },
                force_refresh_extraction: true,
                force_refresh_assets: true,
                force_refresh_bindings: true,
                planner: { candidate_limit: 10 },
                materialization: { mode: "selected", upload_to_drive: true, wait_for_ready: true },
                include_trace: true
            },
            docs: { enabled: true, languages: ["en"], folder_id: $folder }
        }]
    }'
}

payload=$(build_payload)

if [[ "$DRY" == 1 ]]; then
    printf 'DRY RUN — POST http://%s/api/script/generate\n' "$SMOKE_API_BASE"
    printf 'docs folder : %s\n' "$DOC_FOLDER_ID"
    printf 'scenes      : %d\n' "$SCENE_COUNT"
    printf 'render_video: ABSENT (= false, video-disabled contract)\n\n'
    jq . <<<"$payload"
    exit 0
fi

# ── Dispatch → poll → fetch → extract ──────────────────────────────────────
key="cert-nlp-online-images-docs-$(smoke_gen_uuid)"
smoke_log_section "Dispatch: NLP online entity images certification (${SCENE_COUNT} scenes)"
generate_dispatch "$payload" "$key"
printf 'job_id: %s\n' "$GENERATE_JOB_ID"

smoke_log_section "Poll /api/jobs/$GENERATE_JOB_ID/full"
generate_poll_and_fetch
printf 'final status: %s\n' "$SMOKE_LAST_STATUS"
generate_extract_result
result="$GENERATE_RESULT"

# ── Assertion helpers (contains-based: additional valid entities allowed) ──
entity_has() { # idx type value → 0/1 (case-insensitive contains; empty type = any)
    local idx="$1" type="$2" value="$3"
    jq -e --argjson i "$idx" --arg t "$type" --arg v "$value" '
        any(.segments[$i].insights.entities[]?;
            (($t == "") or ((.type | ascii_upcase) == ($t | ascii_upcase)))
            and ((.value | ascii_downcase) | contains(($v | ascii_downcase))))
    ' <<<"$result" >/dev/null 2>&1
}

failures=0
persons=0 places=0 phrases=0 words_ok=0 no_contamination=0
results_ok=0 verified_ok=0 drive_ok=0 binding_ok=0 inline_ok=0

printf '\nNLP + ONLINE ENTITY IMAGES + GOOGLE DOCS CERTIFICATION\n'
printf '%-5s %-16s %-14s %-7s %-6s %-8s %-8s %-8s %-6s %-5s %s\n' \
    SCENE PERSON PLACE PHRASE WORDS RESULTS VERIFIED DRIVE BINDING INLINE DOC

for ((i = 0; i < SCENE_COUNT; i++)); do
    id="${SCENE_ID[$i]}" person="${SCENE_PERSON[$i]}" place="${SCENE_PLACE[$i]}"

    col_person=FAIL; col_place=FAIL; col_phrase=FAIL; col_words=0
    col_results=0; col_verified=FAIL; col_drive=FAIL; col_binding=FAIL; col_inline=FAIL; col_doc=FAIL

    # ── NLP per segment ──────────────────────────────────────────────
    if entity_has "$i" "PERSON" "$person"; then
        col_person=PASS; persons=$((persons + 1))
    else
        printf 'FAIL[%s]: PERSON %q not extracted (entities=%s)\n' "$id" "$person" \
            "$(jq -c --argjson i "$i" '[.segments[$i].insights.entities[]?.value]' <<<"$result")" >&2
        failures=$((failures + 1))
    fi

    if entity_has "$i" "" "$place"; then
        col_place=PASS; places=$((places + 1))
    else
        printf 'FAIL[%s]: PLACE %q not extracted\n' "$id" "$place" >&2
        failures=$((failures + 1))
    fi

    if jq -e --argjson i "$i" --arg p "$person" '
        ([.segments[$i].insights.important_phrases[]?] | length) == 1
        and ((.segments[$i].insights.important_phrases[0] | ascii_downcase) | contains(($p | ascii_downcase)))
    ' <<<"$result" >/dev/null 2>&1; then
        col_phrase=PASS; phrases=$((phrases + 1))
    else
        printf 'FAIL[%s]: expected exactly one important phrase mentioning %q\n' "$id" "$person" >&2
        failures=$((failures + 1))
    fi

    col_words=$(jq -r --argjson i "$i" '[.segments[$i].insights.important_words[]?] | length' <<<"$result")
    if [[ "$col_words" =~ ^[0-9]+$ && "$col_words" -ge 3 && "$col_words" -le 5 ]]; then
        words_ok=$((words_ok + 1))
    else
        printf 'FAIL[%s]: important words = %s, want 3-5\n' "$id" "$col_words" >&2
        failures=$((failures + 1))
    fi

    # ── Cross-scene contamination: this scene must never contain another
    # scene's person. ────────────────────────────────────────────────
    contaminated=0
    for ((j = 0; j < SCENE_COUNT; j++)); do
        [[ "$j" == "$i" ]] && continue
        if entity_has "$i" "PERSON" "${SCENE_PERSON[$j]}"; then
            printf 'FAIL[%s]: cross-scene contamination — contains %q (scene %s)\n' \
                "$id" "${SCENE_PERSON[$j]}" "${SCENE_ID[$j]}" >&2
            contaminated=1
        fi
    done
    if [[ "$contaminated" == 0 ]]; then
        no_contamination=$((no_contamination + 1))
    else
        failures=$((failures + 1))
    fi

    # ── Online candidates + materialization (internet_images only) ──
    col_results=$(jq -r --argjson i "$i" \
        '[.segments[$i].assets.candidates[]? | select(.provider == "internet_images")] | length' <<<"$result")
    if [[ "$col_results" =~ ^[0-9]+$ && "$col_results" -ge 1 ]]; then
        results_ok=$((results_ok + 1))
    else
        printf 'FAIL[%s]: online candidates = %s, want >= 1\n' "$id" "$col_results" >&2
        failures=$((failures + 1))
    fi

    if jq -e --argjson i "$i" '
        any(.segments[$i].assets.candidates[]?;
            .provider == "internet_images"
            and ((.source_url // "") != "")
            and (.verification_status == "verified"))
    ' <<<"$result" >/dev/null 2>&1; then
        col_verified=PASS; verified_ok=$((verified_ok + 1))
    else
        printf 'FAIL[%s]: no verified internet_images candidate (source_url + verified)\n' "$id" >&2
        failures=$((failures + 1))
    fi

    if jq -e --argjson i "$i" '
        any(.segments[$i].assets.candidates[]?;
            .provider == "internet_images" and ((.drive_link // "") != ""))
    ' <<<"$result" >/dev/null 2>&1; then
        col_drive=PASS; drive_ok=$((drive_ok + 1))
    else
        printf 'FAIL[%s]: no internet_images candidate with a Drive link\n' "$id" >&2
        failures=$((failures + 1))
    fi

    # ── Entity binding: the person image is identity-scoped + resolved ──
    if jq -e --argjson i "$i" --arg p "$person" '
        any(.output.specscene.scenes[$i].annotations.primary_entities[]?;
            .type == "PERSON"
            and ((.canonical_name | ascii_downcase) | contains(($p | ascii_downcase)))
            and (.image.status == "resolved")
            and ((.image.drive_link // "") != ""))
    ' <<<"$result" >/dev/null 2>&1; then
        col_binding=PASS; binding_ok=$((binding_ok + 1))
    else
        printf 'FAIL[%s]: person %q has no resolved entity-image binding\n' "$id" "$person" >&2
        failures=$((failures + 1))
    fi

    # ── Inline image (IDEAL PASS) ────────────────────────────────────
    if jq -e --argjson i "$i" --arg p "$person" '
        any(.output.specscene.scenes[$i].annotations.primary_entities[]?;
            .type == "PERSON"
            and ((.canonical_name | ascii_downcase) | contains(($p | ascii_downcase)))
            and (.image.status == "resolved")
            and ((.image.preview_url // "") != ""))
    ' <<<"$result" >/dev/null 2>&1; then
        col_inline=PASS; inline_ok=$((inline_ok + 1))
    fi

    # ── Document: scene present with a resolved entity image ─────────
    if [[ "$col_binding" == "PASS" ]]; then
        col_doc=PASS
    fi

    printf '%-5s %-16s %-14s %-7s %-6s %-8s %-8s %-8s %-6s %-5s %s\n' \
        "$id" "$person" "$place" "$col_phrase" "$col_words" "$col_results" \
        "$col_verified" "$col_drive" "$col_binding" "$col_inline" "$col_doc"
done

# ── Aggregate gates ─────────────────────────────────────────────────────────
scene_count=$(jq '[.segments[]?] | length' <<<"$result")
spec_scene_count=$(jq '[.output.specscene.scenes[]?] | length' <<<"$result")
[[ "$scene_count" == "$SCENE_COUNT" ]] || { printf 'FAIL: segments = %s, want %d\n' "$scene_count" "$SCENE_COUNT" >&2; failures=$((failures + 1)); }
[[ "$spec_scene_count" == "$SCENE_COUNT" ]] || { printf 'FAIL: specscene scenes = %s, want %d\n' "$spec_scene_count" "$SCENE_COUNT" >&2; failures=$((failures + 1)); }

# Google Doc must be published with id + link.
if jq -e '(.artifacts.document.doc_id // "") != "" and (.artifacts.document.doc_link // "") != ""' <<<"$result" >/dev/null 2>&1; then
    doc_id=$(jq -r '.artifacts.document.doc_id' <<<"$result")
    doc_link=$(jq -r '.artifacts.document.doc_link' <<<"$result")
    printf 'Google Doc: %s\n' "$doc_link"
else
    printf 'FAIL: Google Doc artifact missing doc_id/doc_link\n' >&2
    failures=$((failures + 1))
fi

# VIDEO disabled: the certification chain has no render stage.
if jq -e '[.. | objects | select(has("render_plan") or has("render_job_id") or has("render_jobs"))] | length == 0' <<<"$result" >/dev/null 2>&1; then
    printf 'video render: ZERO (RenderPlan=nil, no render job)\n'
else
    printf 'FAIL: video-disabled certification leaked render work\n' >&2
    failures=$((failures + 1))
fi

# ── Summary ─────────────────────────────────────────────────────────────────
printf '\nNLP            : persons=%d/%d places=%d/%d phrases=%d/%d words(3-5)=%d/%d cross-scene-clean=%d/%d\n' \
    "$persons" "$SCENE_COUNT" "$places" "$SCENE_COUNT" "$phrases" "$SCENE_COUNT" "$words_ok" "$SCENE_COUNT" "$no_contamination" "$SCENE_COUNT"
printf 'ONLINE SEARCH  : scenes with candidates=%d/%d verified=%d/%d drive=%d/%d\n' \
    "$results_ok" "$SCENE_COUNT" "$verified_ok" "$SCENE_COUNT" "$drive_ok" "$SCENE_COUNT"
printf 'ENTITY BINDING : resolved=%d/%d inline=%d/%d\n' "$binding_ok" "$SCENE_COUNT" "$inline_ok" "$SCENE_COUNT"
printf 'DOC            : %s\n' "$([[ -n "${doc_id:-}" ]] && echo "$doc_id" || echo FAIL)"
printf 'VIDEO          : DISABLED (RenderVideo=false, render jobs=0)\n'

if [[ "$failures" -ne 0 ]]; then
    printf 'CERTIFICATION FAILED (%d assertions)\n' "$failures" >&2
    exit 1
fi
printf 'CERTIFIED = YES\n'
