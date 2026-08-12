#!/usr/bin/env bash
# tests/operational/comedian_clips_generate_voiceover_subtitles.sh
#
# Full end-to-end: 5 comedian clips → script generate → voiceover →
# subtitle verification → Velox Master submit → poll → verify.
#
# Steps:
#   1. Preflight: DB, clips, Velox Master health, worker connectivity
#   2. POST /api/script/generate (5 comedian clips, source.type=clips)
#   3. Poll PipelineGen until terminal
#   4. Assert script output + specscene bindings
#   5. Generate voiceover for all scenes in one batch
#   6. Poll voiceover jobs until terminal
#   7. Verify subtitle artifacts in SQLite
#   8. Build velox payload with all assets
#   9. Submit to Velox Master POST /api/v1/jobs
#  10. Poll Velox until terminal (PENDING→LEASED→RUNNING→SUCCEEDED)
#  11. Verify final output artifact
#
# Environment:
#   VELOX_ADMIN_TOKEN        PipelineGen admin token (mandatory)
#   VELOX_MASTER_ADMIN_TOKEN Velox Master admin token for asset upload + worker preflight
#   VELOX_M2M_TOKEN          Velox Master M2M token for job submit (mandatory for step 9+)
#   VELOX_MASTER_URL         Velox Master base URL (default: http://127.0.0.1:8000)
#   SMOKE_DB                 SQLite path (default: data/media/media.db.sqlite)
#   VELOX_DESTINATION_ID     Target destination (default: comedy_test)
#
# Exit codes:
#   0   all steps passed
#   1   assertion failed
#   2   setup error
#  124  poll timeout

set -euo pipefail

DIR=$(cd "$(dirname "$0")" && pwd)
# shellcheck disable=SC1091
source "$DIR/lib/common.sh"
# shellcheck disable=SC1091
source "$DIR/lib/comedian_clips_setup.sh"
# shellcheck disable=SC1091
source "$DIR/lib/comedian_clips_generation.sh"
# shellcheck disable=SC1091
source "$DIR/lib/comedian_clips_velox.sh"
smoke_require sqlite3 jq curl ffmpeg ffprobe python3

comedian_setup "$@"
comedian_preflight
comedian_dispatch_script
comedian_poll_script
comedian_assert_script
comedian_generate_voiceover
comedian_poll_voiceover
comedian_verify_subtitles
comedian_build_velox_payload
comedian_submit_velox
comedian_poll_velox
comedian_verify_final
