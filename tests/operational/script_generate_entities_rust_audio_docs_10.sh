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


# ── Continuation ────────────────────────────────────────────────────
# Payload builders, per-job assertions, doc verification, run_one_job and
# the orchestration/certification tail run from the sourced _part files
# (same shell: every variable stays in scope) so each physical file
# stays under 400 lines.
source "$DIR/script_generate_entities_rust_audio_docs_10_part2"
