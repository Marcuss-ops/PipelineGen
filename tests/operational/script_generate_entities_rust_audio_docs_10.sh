#!/usr/bin/env bash
# script_generate_entities_rust_audio_docs_10.sh — E2E certification of the
# full script.generate chain with 10 independent, controlled items.
#
# Certified chain (one job per item, never one batch of 10 items):
#
#     POST /api/script/generate
#         ↓
#     text generation
#         ↓
#     SpecScene
#         ↓
#     ENTITY EXTRACTION          (persons / places / concepts, known a priori)
#         ↓
#     VOICEOVER                  (per-scene Drive audio links)
#         ↓
#     CanonicalTimeline
#         ↓
#     CompiledAudioPlan
#         ↓
#     RUST → final_audio.m4a     (COMBINED_TIMELINE master, encoded once)
#         ↓
#     audio certification + Drive upload
#         ↓
#     GOOGLE DOC (docs.enabled)
#         ↓
#     SUCCEEDED
#
# Every item uses source.type=text with the three expected entities embedded
# in source_text and an explicit instruction to the model to NOT introduce
# additional named entities. The canonical entity result (artifacts.entities)
# is typed (persons/places/concepts), so expectations are checked without
# parsing raw output.
#
# AUDIO-ONLY CONTRACT (2026-08): generate_timeline, audio.mode=
# COMBINED_TIMELINE and render_video are three independent facts.
# GenerateTimeline builds the canonical timeline; COMBINED_TIMELINE
# compiles ONE certified final_audio.m4a; RenderVideo ONLY controls binary
# video render work. This certification runs with render_video ABSENT
# (absence means false): the pipeline must build script → entities →
# voiceovers → canonical timeline → compiled audio plan → Rust
# final_audio.m4a → Drive → Google Doc and STOP. No RenderPlan, no video
# segments, no video render job may be required or produced.
#
# TIMING CONTRACT (2026-08): every item pins audio.timing.mode=required
# (boundary=word, formats json+srt+vtt). A scene whose synthesis cannot
# produce valid word boundaries — or whose silence-remap lacks an edit map
# — FAILS the job instead of degrading to fake timestamps. The per-scene
# timing bundle (json_link/srt_link/vtt_link + word_count/duration_us/
# text_sha256/audio_sha256) must be present for every scene. This is the
# payload-side half of the EDGE/WORD/PHRASE/MASTER/SILENCE certification;
# the durable runner must carry the same policy (audio.timing) into its
# voiceover generation for the gate to be enforced end-to-end.
#
# Certified per-job surface. The durable single-item runner (Rust audio
# path, wired when cfg.External.RustMusclesPath is set) persists the
# CAPABILITY result under /api/jobs/<id>/full → .result.data.result:
#   scenes[] (voiceover.<lang>.url per scene), canonical_timeline,
#   audio_plan, render_plan, final_audio, audio_metrics, documents,
#   document_renderers, document_specscene_sha256, document_scene_counts,
#   audio_mode, audio_strategy.
# The legacy single-item path (no Rust runtime) emits the domain envelope
# under .result.data.items[0].result (output/artifacts/timings). Every
# check below is SURFACE-TOLERANT: it resolves each value from whichever
# surface the deployment exposes, preferring the canonical durable one.
#
# Per-job assertions implemented in assert_item_result():
#   - dispatch HTTP 202 + job_id, terminal job status SUCCEEDED
#   - SCRIPT   : scenes > 0, per-scene text non-empty, word_count > 0
#   - VOICEOVER: EXACTLY one non-empty Drive audio link per scene for the
#                requested language, zero empty links (durable:
#                scenes[].voiceover[lang].url; legacy: artifacts.voiceovers
#                [lang].drive_links length == scene count)
#   - TIMELINE : canonical_timeline present, duration_us > 0, segment count
#                == scene count, segments contiguous and non-overlapping
#                (index == position, timeline_start_us == cumulative end,
#                final end == duration_us); legacy envelope falls back to
#                the surfaced timeline paths when present
#   - AUDIO    : final_audio container m4a, codec aac, profile LC,
#                sample_rate 48000, channels 2, duration_ms > 0,
#                audio_plan_sha256 + final_audio_sha256 both 64-hex,
#                final_mix and copy_eligible both true, audio_asset_id +
#                drive_link non-empty
#   - ENCODE   : audio_encode_passes == 1 per job — canonical
#                audio_metrics.audio_encode_passes on the durable surface,
#                legacy flat timings.audio_encode_passes cross-checked when
#                present (a dual-contract divergence is a FAIL); the
#                render/mux copy path must add ZERO encodes
#   - COPY     : audio_strategy == FINAL_AUDIO_COPY and, on the durable
#                surface, render_plan null + render_job absent (audio-only
#                run, no video render work) — the mux copies the certified
#                master, never re-encodes it
#   - DOC      : published document present with id + link (durable:
#                documents[lang]; legacy: artifacts.document doc_id/doc_link)
#   - ENTITIES : EXACT typed match on the legacy envelope —
#                artifacts.entities is the PRIMARY source (never
#                entities_json): each category (persons/places/concepts)
#                must contain EXACTLY the expected value from the controlled
#                source_text and NOTHING else (no extra entities). The
#                durable capability result does NOT expose artifacts.entities
#                (entity extraction runs in the VidRush enrichment plane); on
#                that surface the ENTITIES check is reported N/A with a NOTE
#                rather than a fake PASS.
#
# Google Doc CONTENT certification (export tier): when a Drive OAuth token
# is available, each published document is exported via the Drive API
# (files/{id}/export?mimeType=text/plain) and verified against the real
# GenerationResult surface:
#   - Title present
#   - Full Audio section: Lang English, full M4A Drive URL, Duration MM:SS
#   - Per-scene sections with text + Voiceover Drive link
#   - SpecScene JSON: parses, scene count == canonical, and its sha256
#     (Go-compatible compact marshal) equals the recorded
#     document_specscene_sha256 — proof the doc contains the real SpecScene
#   - Audio Timeline JSON: parses, segments == scene count, duration_us
#     matches canonical_timeline
#   - Final Audio JSON: audio_asset_id + drive_link + audio_plan_sha256 +
#     final_audio_sha256 + duration_us all aligned with
#     GenerationResult.final_audio
#
# Output. The run ends with a per-job table and an exact summary:
#
#     JOB | PERSON | PLACE | CONCEPT | ENTITIES | SCENES | VO | M4A |
#         RUST ENCODE | DOC | DOC AUDIO MATCH | STATUS
#
#     jobs_requested              = 10
#     jobs_succeeded              = 10
#     scripts_nonempty            = 10
#     entities_expected_match     = 10   (N/A on the durable surface)
#     entity_failures             = 0
#     voiceover_jobs_complete     = 10
#     final_m4a                   = 10
#     audio_master_encode_passes  = 10
#     render_plan_null            = 10
#     google_docs                 = 10
#     google_docs_valid_content   = 10
#     doc_specscene_hash_match    = 10
#     doc_final_audio_asset_match = 10
#     FAILED                      = 0
#     CERTIFIED = YES | NO
#
# A job SUCCEEDED with any missing/invalid artifact (entities, voiceover,
# timeline, final_audio.m4a, encode passes != 1, render leak, document or
# document content) is a FAIL of the certification — never a silent pass.
# Without a token the tier is reported SKIPPED (doc id/link are still
# verified from the result envelope) and is excluded from the CERTIFIED gate.
#
# Environment (overridable; defaults shown):
#   API_BASE                    host:port (default 127.0.0.1:${VELOX_PORT:-8000})
#   VELOX_ADMIN_TOKEN           bearer token (or TOKEN_FILE via common.sh)
#   CERT_DOCS_FOLDER_ID         Google Drive folder_id for docs (REQUIRED;
#                               fallback VELOX_DRIVE_SCRIPTS_GENERATE)
#   CERT_DRIVE_TOKEN_FILE       Google OAuth token.json with .access_token
#                               (default <repo>/token.json; SKIPPED if absent)
#   CERT_DRIVE_API_BASE         Drive API base (default
#                               https://www.googleapis.com)
#   CERT_LANGUAGE               target language (default en)
#   CERT_WAVES                  space-separated wave sizes, e.g. "2 4 4"
#                               (default: sequential, one job at a time)
#   CERT_RESULTS_DIR            per-run artifact dir (default
#                               /tmp/pipelinegen-cert-entities-audio-docs-<ts>)
#   CERT_JOBS                   space-separated job indices to run
#                               (default 01..10)
#   SMOKE_TIMEOUT_SECONDS       overall wall clock (default 3600)
#   SMOKE_POLL_TIMEOUT_SECONDS  per-job poll ceiling (default 900)
#
# Exit codes (tests/operational/lib/common.sh contract):
#   0   every job PASS (CERTIFIED)
#   1   one or more jobs FAILED (or a SUCCEEDED job missed required artifacts)
#   2   setup error (missing folder id / binaries / token / stale Drive token)
#   124 poll loop or wall-clock timeout exceeded

set -euo pipefail
umask 077

DIR=$(cd "$(dirname "$0")" && pwd)

# ── Configuration (BEFORE sourcing common.sh: SMOKE_DEADLINE is computed
# at source-time from SMOKE_TIMEOUT_SECONDS, so these must be in place first
# or the wall clock falls back to the 180s default and long jobs time out). ──
SMOKE_TIMEOUT_SECONDS="${SMOKE_TIMEOUT_SECONDS:-3600}"
SMOKE_POLL_TIMEOUT_SECONDS="${SMOKE_POLL_TIMEOUT_SECONDS:-900}"
export SMOKE_TIMEOUT_SECONDS SMOKE_POLL_TIMEOUT_SECONDS

# shellcheck disable=SC1091
source "$DIR/lib/common.sh"

smoke_require jq curl sha256sum

# ── Help / dry-run ───────────────────────────────────────────────
if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    sed -n '2,60p' "$0"
    exit 0
fi

CERT_LANGUAGE="${CERT_LANGUAGE:-en}"
DOCS_FOLDER_ID="${CERT_DOCS_FOLDER_ID:-${VELOX_DRIVE_SCRIPTS_GENERATE:-}}"
CERT_JOBS="${CERT_JOBS:-01 02 03 04 05 06 07 08 09 10}"
CERT_WAVES="${CERT_WAVES:-}"

REPO_ROOT="$(cd "$DIR/../.." && pwd)"
CERT_DRIVE_TOKEN_FILE="${CERT_DRIVE_TOKEN_FILE:-${SMOKE_DRIVE_TOKEN_FILE:-$REPO_ROOT/token.json}}"
CERT_DRIVE_API_BASE="${CERT_DRIVE_API_BASE:-https://www.googleapis.com}"
CERT_DRIVE_TOKEN=""
DOC_CONTENT_ACTIVE=0

RUN_KEY="$(date +%s)_$$"
CERT_RESULTS_DIR="${CERT_RESULTS_DIR:-/tmp/pipelinegen-cert-entities-audio-docs-${RUN_KEY}}"
mkdir -p "$CERT_RESULTS_DIR"
chmod 700 "$CERT_RESULTS_DIR"

# ── Setup guards (fail-closed before any POST) ───────────────────
if [[ -z "$DOCS_FOLDER_ID" ]]; then
    printf '%ssetup error: CERT_DOCS_FOLDER_ID (or VELOX_DRIVE_SCRIPTS_GENERATE) is required — docs.enabled=true needs a Drive folder_id%s\n' \
        "$RED" "$RESET" >&2
    exit 2
fi

# ── Drive OAuth token (Google Doc content export tier) ───────────
# Optional: when token.json with .access_token is available the exported
# document content is verified per job (strict). Without it the tier is
# SKIPPED — never a fake PASS.
if [[ -f "$CERT_DRIVE_TOKEN_FILE" ]]; then
    CERT_DRIVE_TOKEN=$(jq -r '.access_token // empty' "$CERT_DRIVE_TOKEN_FILE" 2>/dev/null || true)
    if [[ -n "$CERT_DRIVE_TOKEN" ]]; then
        pre_code=$(curl -s --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" -o /dev/null -w '%{http_code}' \
            -H "Authorization: Bearer $CERT_DRIVE_TOKEN" \
            "$CERT_DRIVE_API_BASE/drive/v3/about?fields=user")
        if [[ "$pre_code" != "200" ]]; then
            printf '%ssetup error: Drive API preflight returned HTTP %s — stale token in %s%s\n' \
                "$RED" "$pre_code" "$CERT_DRIVE_TOKEN_FILE" "$RESET" >&2
            exit 2
        fi
        DOC_CONTENT_ACTIVE=1
    else
        printf '%sNOTE: %s has no access_token — Google Doc CONTENT verification SKIPPED (id/link still verified)%s\n' \
            "$YELLOW" "$CERT_DRIVE_TOKEN_FILE" "$RESET"
    fi
else
    printf '%sNOTE: no Drive OAuth token (%s) — Google Doc CONTENT verification SKIPPED (id/link still verified)%s\n' \
        "$YELLOW" "$CERT_DRIVE_TOKEN_FILE" "$RESET"
fi
# ── The 10 controlled items: person | place | concept ────────────
# The source_text template explicitly names exactly three supplied elements
# and forbids inventing additional named people, places, organizations,
# dates or events, so the typed entity extraction has known expectations.
declare -a ITEM_ID=() ITEM_PERSON=() ITEM_PLACE=() ITEM_CONCEPT=()
add_item() {
    ITEM_ID+=("$1")
    ITEM_PERSON+=("$2")
    ITEM_PLACE+=("$3")
    ITEM_CONCEPT+=("$4")
}
add_item "cert-entities-audio-docs-01" "Jackie Chan"    "Hong Kong"  "martial arts"
add_item "cert-entities-audio-docs-02" "Tom Holland"    "London"     "acting"
add_item "cert-entities-audio-docs-03" "Adam Sandler"   "New York"   "comedy"
add_item "cert-entities-audio-docs-04" "Serena Williams" "Miami"     "tennis"
add_item "cert-entities-audio-docs-05" "Gordon Ramsay"  "London"     "cooking"
add_item "cert-entities-audio-docs-06" "Keanu Reeves"   "Toronto"    "filmmaking"
add_item "cert-entities-audio-docs-07" "Lewis Hamilton" "Monaco"     "Formula One"
add_item "cert-entities-audio-docs-08" "Adele"          "London"     "music"
add_item "cert-entities-audio-docs-09" "Emma Watson"    "Paris"      "education"
add_item "cert-entities-audio-docs-10" "Dwayne Johnson" "Miami"      "wrestling"

# The certification deliberately does NOT set skip_quality_gate: the real
# editorial path must be exercised end to end.
CERT_STYLE="${CERT_STYLE:-Write a short English narration. Use only the supplied source text. Preserve the named person, place and main concept. Do not invent additional people, places, organizations, quotes or events.}"
CERT_TONE="${CERT_TONE:-clear, factual and conversational}"
# Project is the artifact-routing namespace required by the runner's
# voiceover gate (ErrProjectRequired: a voiceover-enabled generation fails
# closed before the first TTS call when Project is empty).
CERT_PROJECT="${CERT_PROJECT:-perf-cert-compare}"

# ── Payload builder ──────────────────────────────────────────────
# build_item IDX → single-item payload JSON (canonical v2 envelope).
build_item_payload() {
    local idx="$1"
    local id="${ITEM_ID[$idx]}" person="${ITEM_PERSON[$idx]}"
    local place="${ITEM_PLACE[$idx]}" concept="${ITEM_CONCEPT[$idx]}"
    local title="${person} and ${concept}"
    local source_text
    source_text="${person} is the central person in this short narration. The setting discussed is ${place}. The main concept is ${concept}. Explain these three supplied elements in a concise and natural way without introducing additional named people, places, organizations, dates or events."
    # render_video is deliberately ABSENT: absence means false. The
    # audio-only contract certifies generate_timeline=true +
    # voiceover_enabled=true + COMBINED_TIMELINE with NO video render work.
    jq -nc --arg id "$id" --arg title "$title" --arg topic "$title" \
        --arg text "$source_text" --arg lang "$CERT_LANGUAGE" \
        --arg tone "$CERT_TONE" --arg style "$CERT_STYLE" \
        --arg folder "$DOCS_FOLDER_ID" --arg project "$CERT_PROJECT" '{
        version: 2,
        preset: "custom",
        force_refresh: true,
        items: [{
            id: $id,
            title: $title,
            project: $project,
            language: $lang,
            tone: $tone,
            style: $style,
            source: {
                type: "text",
                topic: $topic,
                source_text: $text
            },
            script_params: {
                target_words: 100,
                min_words: 60,
                segment_words: 60,
                use_memory: false
            },
            output: {
                save_to_db: true,
                extract_entities: true,
                generate_metadata: false,
                generate_scene_images: false,
                generate_timeline: true,
                voiceover_enabled: true
            },
            audio: {
                mode: "COMBINED_TIMELINE",
                timing: {
                    mode: "required",
                    boundary: "word",
                    formats: ["json", "srt", "vtt"]
                }
            },
            docs: { enabled: true, languages: [$lang], folder_id: $folder }
        }]
    }'
}

# ── Per-job state ────────────────────────────────────────────────
declare -a ROW_JOBS=()
# JOB_CHECKS[job_id] = "entities|scenes|vo|m4a|encode|doc|doc_audio" per
# column of the certification table (PASS/FAIL/N-A or a count).
declare -A JOB_CHECKS=()
JOBS_SUCCEEDED=0
JOBS_FAILED=0
FINAL_M4A=0
GOOGLE_DOCS=0
RENDER_PLAN_NULL=0
AUDIO_ENCODE_1=0
VOICEOVER_OK=0
TIMELINE_OK=0
ENTITIES_OK=0
ENTITIES_NA=0
GOOGLE_DOCS_CONTENT=0
DOC_SPECSCENE_MATCH=0
DOC_FINALAUDIO_MATCH=0

fail_row() { printf '%sFAIL%s' "$RED" "$RESET"; }
pass_row() { printf '%sPASS%s' "$GREEN" "$RESET"; }

# assert_item_result FULL_FILE PERSON PLACE CONCEPT → runs the FULL strict
# certification on one job's /full JSON: script, entities (exact typed
# match), voiceover, timeline, final audio master, encode passes, no-render
# and Google Doc. Returns 0 when EVERY check passes (a SUCCEEDED job with
# any missing artifact is a FAIL).
#
# SURFACE DETECTION: the durable capability result (.result.data.result)
# carries scenes[].voiceover.<lang>.url, canonical_timeline, audio_metrics
# and documents[lang]; the legacy domain envelope (.result.data.items[0]
# .result) carries output/artifacts (entities, voiceovers.drive_links,
# document) and flat timings.audio_encode_passes. Each check resolves from
# whichever surface the deployment exposes.
assert_item_result() {
    local full_file="$1" person="$2" place="$3" concept="$4" job_id="$5" rc=0
    # Per-column status for the certification table. Values: PASS/FAIL/N-A
    # or a literal count (SCENES, RUST ENCODE).
    local col_entities="N-A" col_scenes="FAIL" col_vo="FAIL" col_m4a="FAIL" col_encode="N-A" col_doc="FAIL" col_doc_audio="SKIP"
    local item_result surface
    item_result=$(jq -c '.result.data.items[0].result // .result.items[0].result // .result.data.result // .result // empty' "$full_file")
    if [[ -z "$item_result" || "$item_result" == "null" ]]; then
        printf '%sFAIL: no canonical generation result in envelope%s\n' "$RED" "$RESET" >&2
        JOB_CHECKS[$job_id]="$col_entities|$col_scenes|$col_vo|$col_m4a|$col_encode|$col_doc|$col_doc_audio"
        return 1
    fi
    if jq -e '.scenes | type == "array"' <<<"$item_result" >/dev/null 2>&1; then
        surface="durable"
    else
        surface="legacy"
    fi

    # Terminal status: the legacy envelope carries GenerationResult.status;
    # the durable capability result does not (job-level terminal status was
    # already certified by the poll before this function runs).
    if [[ "$surface" == "legacy" ]]; then
        local item_status
        item_status=$(jq -r '.status // empty' <<<"$item_result")
        if [[ -n "$item_status" && "$item_status" != "SUCCEEDED" ]]; then
            printf '%sFAIL: GenerationResult.status=%s (expected SUCCEEDED)%s\n' "$RED" "$item_status" "$RESET" >&2
            rc=1
        fi
    fi

    # SCRIPT + canonical scene count (SpecScene count is the reference for
    # the voiceover and timeline segment-count checks below).
    local scenes=0
    if [[ "$surface" == "durable" ]]; then
        # The durable surface does not populate result.word_count; the
        # script non-emptiness intent is preserved by computing the word
        # count from the certified-language scene text.
        jq -e --arg lang "$CERT_LANGUAGE" '([.scenes[] | (.text[$lang] // "")] | add | split(" ") | map(select(length > 0)) | length) > 0' <<<"$item_result" >/dev/null || { printf '%sFAIL: no words in scene text for language=%s%s\n' "$RED" "$CERT_LANGUAGE" "$RESET" >&2; rc=1; }
        jq -e --arg lang "$CERT_LANGUAGE" '[.scenes[] | select((( .text[$lang] // "") | length) > 0)] | length > 0' <<<"$item_result" >/dev/null || { printf '%sFAIL: no per-scene text for language=%s%s\n' "$RED" "$CERT_LANGUAGE" "$RESET" >&2; rc=1; }
        scenes=$(jq -r '[.scenes[]?] | length' <<<"$item_result")
    else
        jq -e '.output.text | type == "string" and length > 0' <<<"$item_result" >/dev/null || { printf '%sFAIL: output.text is empty%s\n' "$RED" "$RESET" >&2; rc=1; }
        jq -e '.output.word_count | type == "number" and . > 0' <<<"$item_result" >/dev/null || { printf '%sFAIL: output.word_count <= 0%s\n' "$RED" "$RESET" >&2; rc=1; }
        scenes=$(jq -r '[.output.specscene.scenes[]?] | length' <<<"$item_result")
    fi
    if [[ ! "$scenes" =~ ^[0-9]+$ || "$scenes" -le 0 ]]; then
        printf '%sFAIL: canonical scene count is empty%s\n' "$RED" "$RESET" >&2
        rc=1
    else
        col_scenes="$scenes"
    fi

    # ENTITIES EXACT MATCH — the typed entity aggregate is the PRIMARY
    # source for the person/place/concept expectations (never raw JSON). The
    # controlled source_text names exactly one person, one place and one
    # concept and forbids extras, so the strict contract is: each category
    # must contain EXACTLY the expected value (case-insensitive, trimmed)
    # and NOTHING else. Durable exposes the aggregate as result.entities
    # (projected from the VidRush barrier); legacy exposes
    # artifacts.entities. A SUCCEEDED job with a missing or mismatched
    # aggregate is a FAIL on both surfaces.
    if [[ "$surface" == "durable" ]]; then
        local entities_path='.entities'
        local entities_label='entities'
    else
        local entities_path='.artifacts.entities'
        local entities_label='artifacts.entities'
    fi
    if jq -e --arg p "$person" --arg pl "$place" --arg c "$concept" --arg path "$entities_path" '
            ((getpath(($path | ltrimstr(".") | split("."))) // null)) as $e
            | $e != null
            and ([$e.persons // [] | .[] | (.value // "") | ascii_downcase] == [($p | ascii_downcase)])
            and ([$e.places // [] | .[] | (.value // "") | ascii_downcase] == [($pl | ascii_downcase)])
            and ([$e.concepts // [] | .[] | (.value // "") | ascii_downcase] == [($c | ascii_downcase)])
        ' <<<"$item_result" >/dev/null 2>&1; then
        ENTITIES_OK=$(( ENTITIES_OK + 1 ))
        col_entities="PASS"
    else
        printf '%sFAIL: %s must match EXACTLY person="%s" place="%s" concept="%s" with no extra entities%s\n' "$RED" "$entities_label" "$person" "$place" "$concept" "$RESET" >&2
        rc=1
        col_entities="FAIL"
    fi

    # VOICEOVER STRICT — exactly one non-empty Drive audio link per scene
    # for the requested language; zero empty links. Durable: scene
    # .voiceover[lang].url; legacy: artifacts.voiceovers[lang].drive_links.
    if [[ "$surface" == "durable" ]]; then
        # The durable surface exposes per-scene voiceovers as id +
        # (url | file_path) — the audio reference is non-empty either way.
        if jq -e --arg lang "$CERT_LANGUAGE" --argjson n "$scenes" '
            ([.scenes[] | select((( .voiceover[$lang].id // "") | length) > 0
                and (((.voiceover[$lang].url // "") | length) > 0
                     or ((.voiceover[$lang].file_path // "") | length) > 0))] | length) == $n
        ' <<<"$item_result" >/dev/null 2>&1; then
            VOICEOVER_OK=$(( VOICEOVER_OK + 1 ))
            col_vo="PASS"
        else
            printf '%sFAIL: voiceover[%s]: expected one non-empty Drive link per scene (%s), found gaps%s\n' "$RED" "$CERT_LANGUAGE" "$scenes" "$RESET" >&2
            rc=1
            col_vo="FAIL"
        fi
    else
        if jq -e --arg lang "$CERT_LANGUAGE" --argjson n "$scenes" '
            ([.artifacts.voiceovers[] | select(.language == $lang) | .drive_links[]?] | length) == $n
            and ([.artifacts.voiceovers[] | select(.language == $lang) | .drive_links[]? | select((. // "") == "")] | length) == 0
        ' <<<"$item_result" >/dev/null 2>&1; then
            VOICEOVER_OK=$(( VOICEOVER_OK + 1 ))
            col_vo="PASS"
        else
            printf '%sFAIL: voiceovers[%s]: drive_links must equal scene count (%s) with no empty link%s\n' "$RED" "$CERT_LANGUAGE" "$scenes" "$RESET" >&2
            rc=1
            col_vo="FAIL"
        fi
    fi

    # TIMELINE STRICT — canonical_timeline present, duration_us > 0, segment
    # count == SpecScene count, segments contiguous and non-overlapping:
    # index == position, timeline_start_us == cumulative previous end, final
    # segment end == duration_us.
    if [[ "$surface" == "durable" ]]; then
        if jq -e --argjson n "$scenes" '
            .canonical_timeline != null
            and (.canonical_timeline.duration_us > 0)
            and ([.canonical_timeline.segments[]] | length) == $n
            and ([.canonical_timeline.segments[] | .index] == [range(0; $n)])
            and (([.canonical_timeline.segments[] | .duration_us] | min) > 0)
            and ([.canonical_timeline.segments[] | .timeline_start_us] ==
                 ([.canonical_timeline.segments[] | .duration_us] | reduce .[] as $d ([0]; . + [(.[-1] // 0) + $d]) | .[0:$n]))
            and ([.canonical_timeline.segments[] | .timeline_start_us + .duration_us] | .[-1]) == .canonical_timeline.duration_us
        ' <<<"$item_result" >/dev/null 2>&1; then
            TIMELINE_OK=$(( TIMELINE_OK + 1 ))
        else
            printf '%sFAIL: canonical_timeline violates strict contract (segments vs scenes=%s, duration_us, contiguity/non-overlap)%s\n' "$RED" "$scenes" "$RESET" >&2
            rc=1
        fi
    else
        # Legacy envelope: the timeline may not be surfaced; when it is,
        # enforce the same strict segment-count contract.
        local tl_path="" p
        for p in '.canonical_timeline' '.timeline' '.audio_timeline' '.output.timeline'; do
            if jq -e "$p | type == \"object\" and (.segments | type == \"array\")" <<<"$item_result" >/dev/null 2>&1; then
                tl_path="$p"; break
            fi
        done
        if [[ -n "$tl_path" ]]; then
            if jq -e --argjson n "$scenes" "$tl_path.duration_us > 0 and ([$tl_path.segments[]] | length) == \$n" <<<"$item_result" >/dev/null 2>&1; then
                TIMELINE_OK=$(( TIMELINE_OK + 1 ))
            else
                printf '%sFAIL: timeline %s: segment count must equal scenes (%s) and duration_us > 0%s\n' "$RED" "$tl_path" "$scenes" "$RESET" >&2
                rc=1
            fi
        else
            printf '%sNOTE: timeline artifact not surfaced on legacy envelope (N/A)%s\n' "$CYAN" "$RESET"
        fi
    fi

    # FULL AUDIO MASTER STRICT — the certified final_audio.m4a contract:
    # container m4a, codec aac + profile LC, sample_rate 48000, channels 2,
    # duration_ms > 0, both integrity hashes 64-hex, final_mix + copy_eligible
    # true, audio_asset_id + drive_link non-empty.
    if jq -e '.final_audio != null and ((.final_audio.audio_asset_id // "") | length > 0)' <<<"$item_result" >/dev/null 2>&1; then
        if jq -e '
            .final_audio.container == "m4a"
            and .final_audio.codec == "aac"
            and ((.final_audio.profile // "") | ascii_downcase) == "lc"
            and .final_audio.sample_rate == 48000
            and .final_audio.channels == 2
            and (.final_audio.duration_ms > 0)
            and ((.final_audio.audio_plan_sha256 // "") | test("^[0-9a-fA-F]{64}$"))
            and ((.final_audio.final_audio_sha256 // "") | test("^[0-9a-fA-F]{64}$"))
            and .final_audio.final_mix == true
            and .final_audio.copy_eligible == true
            and ((.final_audio.drive_link // "") | length > 0)
        ' <<<"$item_result" >/dev/null 2>&1; then
            FINAL_M4A=$(( FINAL_M4A + 1 ))
            col_m4a="PASS"
        else
            printf '%sFAIL: final_audio violates the certified master contract (m4a/aac/LC/48000/2ch/duration/hashes/final_mix/copy_eligible/drive_link)%s\n' "$RED" "$RESET" >&2
            rc=1
            col_m4a="FAIL"
        fi
    else
        printf '%sFAIL: final_audio missing or audio_asset_id empty%s\n' "$RED" "$RESET" >&2
        rc=1
        col_m4a="FAIL"
    fi

    # AUDIO ENCODE — the master is encoded EXACTLY once. Canonical:
    # audio_metrics.audio_encode_passes == 1. Legacy: flat timings.
    # audio_encode_passes cross-checked when present; a dual-contract
    # divergence is a FAIL. The mux/copy path must add ZERO encodes.
    local encode_ok=1
    if [[ "$surface" == "durable" ]]; then
        jq -e '(.audio_metrics.audio_encode_passes // -1) == 1' <<<"$item_result" >/dev/null 2>&1 || { printf '%sFAIL: audio_metrics.audio_encode_passes != 1%s\n' "$RED" "$RESET" >&2; rc=1; encode_ok=0; col_encode="FAIL"; }
        [[ "$encode_ok" == "1" ]] && col_encode="1"
    else
        if jq -e '.timings.audio_encode_passes != null' <<<"$item_result" >/dev/null 2>&1; then
            jq -e '.timings.audio_encode_passes == 1' <<<"$item_result" >/dev/null 2>&1 || { printf '%sFAIL: timings.audio_encode_passes != 1%s\n' "$RED" "$RESET" >&2; rc=1; encode_ok=0; col_encode="FAIL"; }
            [[ "$encode_ok" == "1" ]] && col_encode="1"
        else
            printf '%sNOTE: audio_encode_passes not surfaced on legacy envelope (N/A)%s\n' "$CYAN" "$RESET"
            encode_ok=0
            col_encode="N-A"
        fi
    fi
    [[ "$encode_ok" == "1" ]] && AUDIO_ENCODE_1=$(( AUDIO_ENCODE_1 + 1 ))

    # NO VIDEO RENDER + COPY — the audio-only run must never build a
    # RenderPlan / render job, and the certified strategy must be
    # FINAL_AUDIO_COPY (the mux copies the certified master, never
    # re-encodes it).
    if [[ "$surface" == "durable" ]]; then
        if jq -e '(.render_plan // null) == null' <<<"$item_result" >/dev/null 2>&1; then
            RENDER_PLAN_NULL=$(( RENDER_PLAN_NULL + 1 ))
        else
            printf '%sFAIL: render_plan must be null (audio-only run)%s\n' "$RED" "$RESET" >&2; rc=1
        fi
        if ! jq -e '(.render_job // null) == null' <<<"$item_result" >/dev/null 2>&1; then
            printf '%sFAIL: render_job must be absent (audio-only run)%s\n' "$RED" "$RESET" >&2; rc=1
        fi
        if ! jq -e '(.audio_strategy // "") == "FINAL_AUDIO_COPY"' <<<"$item_result" >/dev/null 2>&1; then
            printf '%sFAIL: audio_strategy must be FINAL_AUDIO_COPY (mux copies, never re-encodes)%s\n' "$RED" "$RESET" >&2; rc=1
        fi
    else
        # Legacy envelope: scan the whole result for any render work leak.
        # The audio-only run must never build a RenderPlan or enqueue a
        # render job, whatever the envelope surface.
        if jq -e '[.. | objects | select(has("render_plan") or has("render_job_id") or has("render_jobs"))] | length == 0' <<<"$item_result" >/dev/null 2>&1; then
            RENDER_PLAN_NULL=$(( RENDER_PLAN_NULL + 1 ))
        else
            printf '%sFAIL: audio-only run leaked render work (render_plan/render_job present)%s\n' "$RED" "$RESET" >&2; rc=1
        fi
        if ! jq -e '(.audio_strategy // "") == "FINAL_AUDIO_COPY"' <<<"$item_result" >/dev/null 2>&1; then
            printf '%sFAIL: audio_strategy must be FINAL_AUDIO_COPY (mux copies, never re-encodes)%s\n' "$RED" "$RESET" >&2; rc=1
        fi
    fi

    # GOOGLE DOC — published document surfaced with id + link (durable:
    # documents[lang]; legacy: artifacts.document).
    if [[ "$surface" == "durable" ]]; then
        if jq -e --arg lang "$CERT_LANGUAGE" '(.documents[$lang] != null) and ((.documents[$lang].id // "") | length > 0) and ((.documents[$lang].link // "") | length > 0)' <<<"$item_result" >/dev/null 2>&1; then
            GOOGLE_DOCS=$(( GOOGLE_DOCS + 1 ))
            col_doc="PASS"
        else
            printf '%sFAIL: documents[%s] missing id/link%s\n' "$RED" "$CERT_LANGUAGE" "$RESET" >&2; rc=1
            col_doc="FAIL"
        fi
    elif jq -e '.artifacts.document != null and ((.artifacts.document.doc_id // "") | length > 0) and ((.artifacts.document.doc_link // "") | length > 0)' <<<"$item_result" >/dev/null 2>&1; then
        GOOGLE_DOCS=$(( GOOGLE_DOCS + 1 ))
        col_doc="PASS"
    else
        printf '%sFAIL: artifacts.document missing doc_id/doc_link%s\n' "$RED" "$RESET" >&2
        rc=1
        col_doc="FAIL"
    fi

    # GOOGLE DOC CONTENT — export the real document and verify its content
    # against the GenerationResult surface (only when the Drive OAuth token
    # is available; otherwise the tier is SKIPPED, never a fake PASS).
    if [[ "$DOC_CONTENT_ACTIVE" == "1" ]]; then
        if assert_document_content "$full_file"; then
            col_doc_audio="PASS"
        else
            col_doc_audio="FAIL"
            rc=1
        fi
    else
        col_doc_audio="SKIP"
    fi

    JOB_CHECKS[$job_id]="$col_entities|$col_scenes|$col_vo|$col_m4a|$col_encode|$col_doc|$col_doc_audio"
    return $rc
}

# ── Google Doc CONTENT verification (real export) ──────────────────
# extract_doc_block FILE START_REGEX END_REGEX → the lines between the START
# marker and the first END marker (used to pull the SpecScene / Audio
# Timeline / Final Audio JSON blocks out of the exported plain text).
extract_doc_block() {
    local file="$1" start="$2" end="$3"
    awk -v s="$start" -v e="$end" '
        $0 ~ s { capture = 1; next }
        capture && e != "" && $0 ~ e { capture = 0 }
        capture { print }
    ' "$file"
}

# go_compact_sha256 → replicates Go's encoding/json Marshal semantics (compact
# separators + HTML escaping of <, >, & inside strings) and prints the sha256.
# The recorded document_specscene_sha256 was computed over json.Marshal of
# the SpecScene, so this helper lets us prove the doc embeds the SAME
# SpecScene that was rendered.
go_compact_sha256() {
    # -j: no trailing newline. jq -c appends \n while Go's json.Marshal (used
    # for the recorded document_specscene_sha256) does not — hashing the newline
    # would never match the recorded value.
    jq -cj . | sed -e 's/&/\\u0026/g' -e 's/</\\u003c/g' -e 's/>/\\u003e/g' | sha256sum | awk '{print $1}'
}

# assert_document_content FULL_FILE → exports the real Google Doc via the
# Drive API (files/{id}/export?mimeType=text/plain) and verifies against the
# GenerationResult surface: title, Full Audio (Lang English / M4A Drive URL /
# Duration), per-scene text + Voiceover links, SpecScene JSON (sha256 matches
# the recorded document_specscene_sha256), Audio Timeline JSON, Final Audio
# JSON (audio_asset_id + hashes aligned with final_audio).
assert_document_content() {
    local full_file="$1" rc=0
    local item_result surface
    item_result=$(jq -c '.result.data.items[0].result // .result.items[0].result // .result.data.result // .result // empty' "$full_file")
    if [[ -z "$item_result" || "$item_result" == "null" ]]; then
        printf '%sFAIL: doc content — no result in envelope%s\n' "$RED" "$RESET" >&2
        return 1
    fi
    if jq -e '.scenes | type == "array"' <<<"$item_result" >/dev/null 2>&1; then
        surface="durable"
    else
        surface="legacy"
    fi

    # Resolve the published doc id + expected title from the surface.
    local doc_id expected_title
    if [[ "$surface" == "durable" ]]; then
        doc_id=$(jq -r --arg lang "$CERT_LANGUAGE" '.documents[$lang].id // empty' <<<"$item_result")
        expected_title=$(jq -r '.title // empty' <<<"$item_result")
    else
        doc_id=$(jq -r '.artifacts.document.doc_id // empty' <<<"$item_result")
        expected_title=$(jq -r '.title // empty' <<<"$item_result")
    fi
    if [[ -z "$doc_id" || "$doc_id" == "null" ]]; then
        printf '%sFAIL: doc content — no doc_id on the result surface%s\n' "$RED" "$RESET" >&2
        return 1
    fi

    local export_file="$CERT_RESULTS_DIR/doc-${doc_id}.txt"
    local code
    code=$(curl -s --max-time "$SMOKE_HTTP_TIMEOUT_SECONDS" -o "$export_file" -w '%{http_code}' \
        -H "Authorization: Bearer $CERT_DRIVE_TOKEN" \
        "$CERT_DRIVE_API_BASE/drive/v3/files/${doc_id}/export?mimeType=text/plain")
    if [[ "$code" != "200" ]]; then
        printf '%sFAIL: doc export returned HTTP %s (doc_id=%s)%s\n' "$RED" "$code" "$doc_id" "$RESET" >&2
        return 1
    fi
    [[ -s "$export_file" ]] || { printf '%sFAIL: doc export body empty (doc_id=%s)%s\n' "$RED" "$doc_id" "$RESET" >&2; return 1; }
    # Google Docs text/plain export arrives as UTF-8 BOM + CRLF. Normalize
    # both so the block markers (`^SpecScene JSON$` …) match and the JSON
    # blocks parse cleanly.
    sed -i '1s/^\xEF\xBB\xBF//' "$export_file"
    sed -i 's/\r$//' "$export_file"
    local flat
    flat=$(tr -s '[:space:]' ' ' < "$export_file")

    # TITLE + FULL AUDIO section.
    if [[ -n "$expected_title" ]] && ! grep -Fq "$expected_title" <<<"$flat"; then
        printf '%sFAIL: doc content — title "%s" missing from exported doc%s\n' "$RED" "$expected_title" "$RESET" >&2; rc=1
    fi
    if ! grep -Fq "Full Audio" <<<"$flat"; then
        printf '%sFAIL: doc content — "Full Audio" section missing%s\n' "$RED" "$RESET" >&2; rc=1
    fi
    if ! grep -Fq "Lang: English" <<<"$flat"; then
        printf '%sFAIL: doc content — "Lang: English" missing%s\n' "$RED" "$RESET" >&2; rc=1
    fi

    # FULL AUDIO Drive URL + Duration (from final_audio, both surfaces).
    local fa_drive_link fa_duration_ms fa_asset fa_sha fa_plan fa_dur_us
    fa_drive_link=$(jq -r '.final_audio.drive_link // empty' <<<"$item_result")
    fa_duration_ms=$(jq -r '.final_audio.duration_ms // 0' <<<"$item_result")
    fa_asset=$(jq -r '.final_audio.audio_asset_id // empty' <<<"$item_result")
    fa_sha=$(jq -r '.final_audio.final_audio_sha256 // empty' <<<"$item_result")
    fa_plan=$(jq -r '.final_audio.audio_plan_sha256 // empty' <<<"$item_result")
    fa_dur_us=$(jq -r '.final_audio.duration_us // 0' <<<"$item_result")
    if [[ -n "$fa_drive_link" ]] && ! grep -Fq "$fa_drive_link" <<<"$flat"; then
        printf '%sFAIL: doc content — final M4A Drive URL missing from Full Audio%s\n' "$RED" "$RESET" >&2; rc=1
    fi
    if [[ "$fa_duration_ms" -gt 0 ]]; then
        local expect_dur
        expect_dur=$(printf '%02d:%02d' $(( fa_duration_ms / 1000 / 60 )) $(( fa_duration_ms / 1000 % 60 )))
        if ! grep -Fq "Duration: $expect_dur" <<<"$flat"; then
            printf '%sFAIL: doc content — Duration %s missing from Full Audio%s\n' "$RED" "$expect_dur" "$RESET" >&2; rc=1
        fi
    fi

    # PER-SCENE sections: text + Voiceover Drive link per scene.
    local scenes
    if [[ "$surface" == "durable" ]]; then
        scenes=$(jq -r '[.scenes[]?] | length' <<<"$item_result")
    else
        scenes=$(jq -r '[.output.specscene.scenes[]?] | length' <<<"$item_result")
    fi
    local i scene_text vo_link
    for ((i = 0; i < scenes; i++)); do
        if [[ "$surface" == "durable" ]]; then
            scene_text=$(jq -r --argjson i "$i" --arg lang "$CERT_LANGUAGE" '.scenes[$i].text[$lang] // ""' <<<"$item_result")
            vo_link=$(jq -r --argjson i "$i" --arg lang "$CERT_LANGUAGE" '.scenes[$i].voiceover[$lang].url // ""' <<<"$item_result")
        else
            scene_text=$(jq -r --argjson i "$i" '.output.specscene.scenes[$i].text // ""' <<<"$item_result")
            vo_link=$(jq -r --argjson i "$i" --arg lang "$CERT_LANGUAGE" '.output.specscene.scenes[$i].bindings.voiceover.links[$lang] // .output.specscene.scenes[$i].bindings.voiceover.link // ""' <<<"$item_result")
        fi
        if [[ -n "$scene_text" ]] && ! grep -Fq "$(printf '%s' "$scene_text" | cut -c1-40)" <<<"$flat"; then
            printf '%sFAIL: doc content — scene %d text missing from exported doc%s\n' "$RED" "$((i + 1))" "$RESET" >&2; rc=1
        fi
        if [[ -n "$vo_link" ]] && ! grep -Fq "$vo_link" <<<"$flat"; then
            printf '%sFAIL: doc content — scene %d Voiceover Drive link missing%s\n' "$RED" "$((i + 1))" "$RESET" >&2; rc=1
        elif [[ -z "$vo_link" && "$surface" == "durable" ]]; then
            # The durable surface exposes per-scene voiceovers as id +
            # (url | file_path); only a published Drive URL is expected to
            # appear in the document, so a bare asset id / local path is N/A.
            printf '%sNOTE: doc content — scene %d voiceover not surfaced as a Drive URL (N/A, id/file_path only)%s\n' "$YELLOW" "$((i + 1))" "$RESET"
        fi
    done

    # SPECSCENE JSON block — hash must match the recorded specscene_sha256.
    local recorded_sha spec_block spec_sha
    if [[ "$surface" == "durable" ]]; then
        recorded_sha=$(jq -r --arg lang "$CERT_LANGUAGE" '.document_specscene_sha256[$lang] // empty' <<<"$item_result")
    else
        recorded_sha=$(jq -r '.artifacts.document.specscene_sha256 // empty' <<<"$item_result")
    fi
    spec_block=$(extract_doc_block "$export_file" '^SpecScene JSON$' '^(Audio Timeline JSON|Final Audio JSON|Rendered Overlay JSON)$')
    if [[ -z "$spec_block" ]]; then
        printf '%sFAIL: doc content — SpecScene JSON block missing%s\n' "$RED" "$RESET" >&2; rc=1
    else
        if ! jq -e --argjson n "$scenes" '(.version == 1) and ([.scenes[]?] | length) == $n' <<<"$spec_block" >/dev/null 2>&1; then
            printf '%sFAIL: doc content — SpecScene JSON invalid or scene count != %s%s\n' "$RED" "$scenes" "$RESET" >&2; rc=1
        fi
        if [[ -n "$recorded_sha" ]]; then
            spec_sha=$(go_compact_sha256 <<<"$spec_block")
            if [[ "$spec_sha" != "$recorded_sha" ]]; then
                printf '%sFAIL: doc content — SpecScene sha256 %s != recorded %s%s\n' "$RED" "$spec_sha" "$recorded_sha" "$RESET" >&2; rc=1
            else
                DOC_SPECSCENE_MATCH=$(( DOC_SPECSCENE_MATCH + 1 ))
            fi
        else
            printf '%sNOTE: doc content — recorded specscene_sha256 absent on surface; hash not cross-checked%s\n' "$YELLOW" "$RESET"
        fi
    fi

    # AUDIO TIMELINE JSON block — segments == scene count, duration_us > 0.
    local tl_block
    tl_block=$(extract_doc_block "$export_file" '^Audio Timeline JSON$' '^(Final Audio JSON|Rendered Overlay JSON)$')
    if [[ -z "$tl_block" ]]; then
        printf '%sFAIL: doc content — Audio Timeline JSON block missing%s\n' "$RED" "$RESET" >&2; rc=1
    elif ! jq -e --argjson n "$scenes" '(.duration_us // 0) > 0 and ([.segments[]?] | length) == $n' <<<"$tl_block" >/dev/null 2>&1; then
        printf '%sFAIL: doc content — Audio Timeline JSON invalid (segments != %s or duration_us <= 0)%s\n' "$RED" "$scenes" "$RESET" >&2; rc=1
    fi

    # FINAL AUDIO JSON block — asset id + hashes aligned with final_audio.
    local fa_block
    fa_block=$(extract_doc_block "$export_file" '^Final Audio JSON$' '^(Rendered Overlay JSON)$')
    if [[ -z "$fa_block" ]]; then
        printf '%sFAIL: doc content — Final Audio JSON block missing%s\n' "$RED" "$RESET" >&2; rc=1
    else
        if ! jq -e --arg asset "$fa_asset" --arg link "$fa_drive_link" --arg sha "$fa_sha" --arg plan "$fa_plan" \
                --argjson dur "$fa_dur_us" '
            (.audio_asset_id // "") == $asset
            and (.drive_link // "") == $link
            and (.final_audio_sha256 // "") == $sha
            and (.audio_plan_sha256 // "") == $plan
            and ($dur <= 0 or (.duration_us // 0) == $dur)
        ' <<<"$fa_block" >/dev/null 2>&1; then
            printf '%sFAIL: doc content — Final Audio JSON does not match final_audio (asset/link/sha256/plan/duration)%s\n' "$RED" "$RESET" >&2; rc=1
        else
            DOC_FINALAUDIO_MATCH=$(( DOC_FINALAUDIO_MATCH + 1 ))
        fi
    fi

    if (( rc == 0 )); then
        GOOGLE_DOCS_CONTENT=$(( GOOGLE_DOCS_CONTENT + 1 ))
        printf '  %sOK: doc %s content verified (title/Full Audio/scenes/SpecScene/Timeline/FinalAudio)%s\n' "$GREEN" "$doc_id" "$RESET"
    else
        printf '%sFAIL: doc %s content verification failed%s\n' "$RED" "$doc_id" "$RESET" >&2
    fi
    return $rc
}

# run_one_job IDX → dispatch, poll, persist /full, assert. Returns 0 on PASS.
run_one_job() {
    local idx="$1"
    local id="${ITEM_ID[$idx]}" person="${ITEM_PERSON[$idx]}"
    local place="${ITEM_PLACE[$idx]}" concept="${ITEM_CONCEPT[$idx]}"
    local key="cert-${id}-${RUN_KEY}"

    smoke_log_section "Job ${id} — ${person} | ${place} | ${concept}"
    local payload
    payload=$(build_item_payload "$idx")

    if [[ "$DRY_RUN" == "1" ]]; then
        printf 'DRY — POST /api/script/generate (Idempotency-Key: %s)\n' "$key"
        jq . <<<"$payload"
        return 0
    fi

    local dispatch_body job_id
    export SMOKE_IDEMPOTENCY_KEY="$key"
    smoke_curl POST "/api/script/generate" --data "$payload" >/dev/null
    unset SMOKE_IDEMPOTENCY_KEY
    if [[ "$SMOKE_LAST_HTTP" != "202" && "$SMOKE_LAST_HTTP" != "200" ]]; then
        printf '%sFAIL[%s]: dispatch returned HTTP %s (expected 202)%s\n' "$RED" "$id" "$SMOKE_LAST_HTTP" "$RESET" >&2
        smoke_echo_safe "$(head -c 1200 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
        JOBS_FAILED=$(( JOBS_FAILED + 1 ))
        ROW_JOBS+=("$id|FAIL-DISPATCH")
        return 1
    fi
    dispatch_body="$SMOKE_LAST_BODY"
    job_id=$(jq -r '.job_id // .id // empty' "$dispatch_body")
    if [[ -z "$job_id" || "$job_id" == "null" ]]; then
        printf '%sFAIL[%s]: dispatch returned no job_id%s\n' "$RED" "$id" "$RESET" >&2
        JOBS_FAILED=$(( JOBS_FAILED + 1 ))
        ROW_JOBS+=("$id|FAIL-DISPATCH")
        return 1
    fi
    printf '  %sdispatch OK%s job_id=%s\n' "$GREEN" "$RESET" "$job_id"

    if ! smoke_poll_terminal "$job_id"; then
        printf '%sFAIL[%s]: poll did not reach terminal (status=%s)%s\n' "$RED" "$id" "${SMOKE_LAST_STATUS:-?}" "$RESET" >&2
        JOBS_FAILED=$(( JOBS_FAILED + 1 ))
        ROW_JOBS+=("$id|FAIL-POLL")
        return 1
    fi
    case "$SMOKE_LAST_STATUS" in
        SUCCEEDED|completed) ;;
        *)
            printf '%sFAIL[%s]: job ended with status %s%s\n' "$RED" "$id" "$SMOKE_LAST_STATUS" "$RESET" >&2
            smoke_echo_safe "$(head -c 1200 "$SMOKE_LAST_BODY" 2>/dev/null || true)" >&2
            JOBS_FAILED=$(( JOBS_FAILED + 1 ))
            ROW_JOBS+=("$id|FAIL-JOB")
            return 1 ;;
    esac

    local full_file="$CERT_RESULTS_DIR/job-${idx}-full.json"
    smoke_curl GET "/api/jobs/${job_id}/full" >/dev/null
    if [[ "$SMOKE_LAST_HTTP" != "200" ]]; then
        printf '%sFAIL[%s]: GET /api/jobs/%s/full returned HTTP %s%s\n' "$RED" "$id" "$job_id" "$SMOKE_LAST_HTTP" "$RESET" >&2
        JOBS_FAILED=$(( JOBS_FAILED + 1 ))
        ROW_JOBS+=("$id|FAIL-FULL")
        return 1
    fi
    cp "$SMOKE_LAST_BODY" "$full_file"
    chmod 600 "$full_file"

    if assert_item_result "$full_file" "$person" "$place" "$concept" "$id"; then
        JOBS_SUCCEEDED=$(( JOBS_SUCCEEDED + 1 ))
        ROW_JOBS+=("$id|PASS")
        printf '  %sOK: %s PASS%s\n' "$GREEN" "$id" "$RESET"
        return 0
    fi
    JOBS_FAILED=$(( JOBS_FAILED + 1 ))
    ROW_JOBS+=("$id|FAIL-ARTIFACTS")
    return 1
}

# ── Orchestration ────────────────────────────────────────────────
declare -a IDX_LIST=()
for j in $CERT_JOBS; do
    case "$j" in
        [0-9]|[0-9][0-9]) IDX_LIST+=("$(( 10#$j - 1 ))") ;;
        *) printf '%ssetup error: CERT_JOBS entries must be 01..10 (got %s)%s\n' "$RED" "$j" "$RESET" >&2; exit 2 ;;
    esac
done
for idx in "${IDX_LIST[@]}"; do
    (( idx >= 0 && idx < 10 )) || { printf '%ssetup error: CERT_JOBS index out of range (01..10)%s\n' "$RED" "$RESET" >&2; exit 2; }
done

if [[ "$DRY_RUN" == "1" ]]; then
    printf '\nDRY RUN — jobs: %s\n' "$CERT_JOBS"
    printf 'docs folder : %s\n' "$DOCS_FOLDER_ID"
    printf 'language    : %s\n' "$CERT_LANGUAGE"
    printf 'render_video: ABSENT (= false, audio-only contract)\n'
    for idx in "${IDX_LIST[@]}"; do
        printf '\n----- %s -----\n' "${ITEM_ID[$idx]}"
        build_item_payload "$idx" | jq .
    done
    printf '\nDRY RUN — would POST /api/script/generate per job and poll /api/jobs/<id>/full\n'
    exit 0
fi

smoke_log_section "Certification: script.generate + entities + Rust audio + Google Docs (10 items)"
printf '  target      : %s\n  docs folder : %s\n  language    : %s\n  render_video: ABSENT (= false, audio-only contract)\n  doc content : %s\n  results dir : %s\n' \
    "$SMOKE_API_BASE" "$DOCS_FOLDER_ID" "$CERT_LANGUAGE" \
    "$([ "$DOC_CONTENT_ACTIVE" == "1" ] && printf 'ACTIVE (%s)' "$CERT_DRIVE_TOKEN_FILE" || printf 'SKIPPED')" \
    "$CERT_RESULTS_DIR"

if [[ -z "$CERT_WAVES" ]]; then
    # Sequential: one job at a time — the most conservative default.
    for idx in "${IDX_LIST[@]}"; do
        run_one_job "$idx" || true
    done
else
    # Wave ramp: e.g. CERT_WAVES="2 4 4" — dispatch the wave group first,
    # then wait for all its jobs to reach terminal before the next wave.
    # The last wave may be truncated by the CERT_JOBS scope: the effective
    # wave size is clamped to the number of remaining jobs so a shortened
    # final wave is never indexed out of bounds under `set -u`.
    WAVE_SIZES=()
    declare -A JOB_ID_BY_IDX=()
    local_wave_start=0
    for wave_size in $CERT_WAVES; do
        [[ "$wave_size" =~ ^[0-9]+$ && "$wave_size" -gt 0 ]] || { printf '%ssetup error: CERT_WAVES entries must be positive integers%s\n' "$RED" "$RESET" >&2; exit 2; }
        remaining=$(( ${#IDX_LIST[@]} - local_wave_start ))
        (( remaining > 0 )) || break
        effective="$wave_size"
        (( effective > remaining )) && effective="$remaining"
        WAVE_SIZES+=("$effective")
        local_wave_start=$(( local_wave_start + effective ))
    done

    # Gated ramp: dispatch a wave → wait for terminal → persist /full → assert
    # the strict artifact contract. A wave that does not fully pass ABORTS the
    # ramp (no further waves are dispatched); the remaining jobs are marked
    # SKIPPED and the certification gate stays fail-closed.
    local_wave_start=0
    wave_num=0
    wave_pass=1
    for wave_size in "${WAVE_SIZES[@]}"; do
        # Ramp gate: only dispatch further waves while every previous wave
        # fully passed. On a failed wave the remaining jobs are SKIPPED.
        if (( wave_pass != 1 )); then
            for ((k = local_wave_start; k < ${#IDX_LIST[@]}; k++)); do
                skip_idx="${IDX_LIST[$k]}"
                ROW_JOBS+=("${ITEM_ID[$skip_idx]}|SKIPPED")
            done
            break
        fi
        wave_num=$(( wave_num + 1 ))
        wave_first="${IDX_LIST[$local_wave_start]}"
        wave_last="${IDX_LIST[$(( local_wave_start + wave_size - 1 ))]}"
        smoke_log_section "Wave $wave_num of $wave_size (jobs ${ITEM_ID[$wave_first]}..${ITEM_ID[$wave_last]})"
        WAVE_JOB_IDS=()
        for ((w = 0; w < wave_size; w++)); do
            wave_idx="${IDX_LIST[$(( local_wave_start + w ))]}"
            build_item_payload "$wave_idx" > "$CERT_RESULTS_DIR/payload-${ITEM_ID[$wave_idx]}.json"
            export SMOKE_IDEMPOTENCY_KEY="cert-${ITEM_ID[$wave_idx]}-${RUN_KEY}"
            smoke_curl POST "/api/script/generate" --data-binary "@$CERT_RESULTS_DIR/payload-${ITEM_ID[$wave_idx]}.json" >/dev/null
            unset SMOKE_IDEMPOTENCY_KEY
            if [[ "$SMOKE_LAST_HTTP" != "202" && "$SMOKE_LAST_HTTP" != "200" ]]; then
                printf '%sFAIL[%s]: dispatch HTTP %s (expected 202)%s\n' "$RED" "${ITEM_ID[$wave_idx]}" "$SMOKE_LAST_HTTP" "$RESET" >&2
                JOBS_FAILED=$(( JOBS_FAILED + 1 ))
                ROW_JOBS+=("${ITEM_ID[$wave_idx]}|FAIL-DISPATCH")
                continue
            fi
            job_id=""
            job_id=$(jq -r '.job_id // .id // empty' "$SMOKE_LAST_BODY")
            if [[ -z "$job_id" || "$job_id" == "null" ]]; then
                printf '%sFAIL[%s]: dispatch returned no job_id%s\n' "$RED" "${ITEM_ID[$wave_idx]}" "$RESET" >&2
                JOBS_FAILED=$(( JOBS_FAILED + 1 ))
                ROW_JOBS+=("${ITEM_ID[$wave_idx]}|FAIL-DISPATCH")
                continue
            fi
            printf '  %sdispatch OK%s %s job_id=%s\n' "$GREEN" "$RESET" "${ITEM_ID[$wave_idx]}" "$job_id"
            WAVE_JOB_IDS+=("$job_id")
            JOB_ID_BY_IDX[$wave_idx]="$job_id"
        done
        # Wait for every dispatched job of this wave to terminal, persist /full.
        for jid in "${WAVE_JOB_IDS[@]}"; do
            if ! smoke_poll_terminal "$jid"; then
                printf '%sFAIL: wave poll did not reach terminal for job %s (status=%s)%s\n' "$RED" "$jid" "${SMOKE_LAST_STATUS:-?}" "$RESET" >&2
                continue
            fi
            full_file="$CERT_RESULTS_DIR/job-${jid}-full.json"
            smoke_curl GET "/api/jobs/${jid}/full" >/dev/null
            cp "$SMOKE_LAST_BODY" "$full_file" 2>/dev/null || true
        done
        # Assert every job of this wave; a single FAIL fails the wave.
        wave_pass=1
        for ((w = 0; w < wave_size; w++)); do
            wave_idx="${IDX_LIST[$(( local_wave_start + w ))]}"
            full_file=""
            if [[ -n "${JOB_ID_BY_IDX[$wave_idx]:-}" ]]; then
                f="$CERT_RESULTS_DIR/job-${JOB_ID_BY_IDX[$wave_idx]}-full.json"
                [[ -f "$f" ]] && full_file="$f"
            fi
            if [[ -z "$full_file" ]]; then
                printf '%sFAIL[%s]: no persisted /full JSON found%s\n' "$RED" "${ITEM_ID[$wave_idx]}" "$RESET" >&2
                JOBS_FAILED=$(( JOBS_FAILED + 1 ))
                ROW_JOBS+=("${ITEM_ID[$wave_idx]}|FAIL-FULL")
                wave_pass=0
                continue
            fi
            if assert_item_result "$full_file" "${ITEM_PERSON[$wave_idx]}" "${ITEM_PLACE[$wave_idx]}" "${ITEM_CONCEPT[$wave_idx]}" "${ITEM_ID[$wave_idx]}"; then
                JOBS_SUCCEEDED=$(( JOBS_SUCCEEDED + 1 ))
                ROW_JOBS+=("${ITEM_ID[$wave_idx]}|PASS")
            else
                JOBS_FAILED=$(( JOBS_FAILED + 1 ))
                ROW_JOBS+=("${ITEM_ID[$wave_idx]}|FAIL-ARTIFACTS")
                wave_pass=0
            fi
        done
        if [[ "$wave_pass" == "1" ]]; then
            printf '  %sOK: wave of %s PASS%s\n' "$GREEN" "$wave_size" "$RESET"
        else
            printf '%sFAIL: wave of %s did not fully pass (see rows above) — ramp ABORTED%s\n' "$RED" "$wave_size" "$RESET"
        fi
        local_wave_start=$(( local_wave_start + wave_size ))
    done
fi

# ── Certification table (per-column, per job) ────────────────────
# Column cells are plain PASS/FAIL/N-A/SKIP or a count; only the STATUS
# column is colored so the fixed-width alignment stays intact.
printf '\n%s===== CERTIFICATION TABLE =====%s\n' "$CYAN" "$RESET"
printf '%-27s | %-18s | %-14s | %-14s | %-8s | %-6s | %-4s | %-4s | %-11s | %-4s | %-14s | %s\n' \
    "JOB" "PERSON" "PLACE" "CONCEPT" "ENTITIES" "SCENES" "VO" "M4A" "RUST ENCODE" "DOC" "DOC AUDIO MATCH" "STATUS"
printf '%s\n' "------------------------------------------------------------------------------------------------------------------------------------"
for row in "${ROW_JOBS[@]}"; do
    job_id="${row%%|*}"
    status="${row##*|}"
    idx=""
    for i in "${!ITEM_ID[@]}"; do
        [[ "${ITEM_ID[$i]}" == "$job_id" ]] && idx=$i
    done
    # checks = "entities|scenes|vo|m4a|encode|doc|doc_audio"
    checks="${JOB_CHECKS[$job_id]:-FAIL|FAIL|FAIL|FAIL|N-A|FAIL|SKIP}"
    IFS='|' read -r c_entities c_scenes c_vo c_m4a c_encode c_doc c_docaudio <<<"$checks"
    s=""
    case "$status" in
        PASS) s=$(pass_row) ;;
        SKIPPED) s=$(printf '%sSKIPPED%s' "$YELLOW" "$RESET") ;;
        *) s=$(fail_row) ;;
    esac
    printf '%-27s | %-18s | %-14s | %-14s | %-8s | %-6s | %-4s | %-4s | %-11s | %-4s | %-14s | %s\n' \
        "$job_id" "${ITEM_PERSON[$idx]}" "${ITEM_PLACE[$idx]}" "${ITEM_CONCEPT[$idx]}" \
        "$c_entities" "$c_scenes" "$c_vo" "$c_m4a" "$c_encode" "$c_doc" "$c_docaudio" "$s"
done

printf '\n%s===== CERTIFICATION SUMMARY =====%s\n' "$CYAN" "$RESET"
printf 'jobs_requested              = %d\n' "${#IDX_LIST[@]}"
printf 'jobs_succeeded              = %d\n' "$JOBS_SUCCEEDED"
printf 'scripts_nonempty            = %d\n' "$JOBS_SUCCEEDED"
printf 'entities_expected_match     = %d\n' "$ENTITIES_OK"
if (( ENTITIES_NA == ${#IDX_LIST[@]} )); then
    printf 'entity_failures             = N/A (entities not exposed on the durable surface)\n'
else
    printf 'entity_failures             = %d\n' "$(( ${#IDX_LIST[@]} - ENTITIES_OK - ENTITIES_NA ))"
fi
printf 'voiceover_jobs_complete     = %d\n' "$VOICEOVER_OK"
printf 'final_m4a                   = %d\n' "$FINAL_M4A"
printf 'audio_master_encode_passes  = %d\n' "$AUDIO_ENCODE_1"
printf 'render_plan_null            = %d\n' "$RENDER_PLAN_NULL"
printf 'google_docs                 = %d\n' "$GOOGLE_DOCS"
if [[ "$DOC_CONTENT_ACTIVE" == "1" ]]; then
    printf 'google_docs_valid_content   = %d\n' "$GOOGLE_DOCS_CONTENT"
    printf 'doc_specscene_hash_match    = %d\n' "$DOC_SPECSCENE_MATCH"
    printf 'doc_final_audio_asset_match = %d\n' "$DOC_FINALAUDIO_MATCH"
else
    printf 'google_docs_valid_content   = SKIPPED (no Drive OAuth token)\n'
fi
printf 'FAILED                      = %d\n' "$JOBS_FAILED"
printf 'results dir                 = %s\n' "$CERT_RESULTS_DIR"

if (( JOBS_FAILED == 0 && JOBS_SUCCEEDED == ${#IDX_LIST[@]} && VOICEOVER_OK == JOBS_SUCCEEDED && TIMELINE_OK == JOBS_SUCCEEDED && FINAL_M4A == JOBS_SUCCEEDED && AUDIO_ENCODE_1 == JOBS_SUCCEEDED && RENDER_PLAN_NULL == JOBS_SUCCEEDED && GOOGLE_DOCS == JOBS_SUCCEEDED )) && { [[ "$DOC_CONTENT_ACTIVE" != "1" ]] || (( GOOGLE_DOCS_CONTENT == JOBS_SUCCEEDED && DOC_SPECSCENE_MATCH == JOBS_SUCCEEDED && DOC_FINALAUDIO_MATCH == JOBS_SUCCEEDED )); }; then
    printf '\n%sCERTIFIED = YES%s\n' "$GREEN" "$RESET"
    exit 0
fi
printf '\n%sCERTIFIED = NO%s\n' "$RED" "$RESET"
exit 1
