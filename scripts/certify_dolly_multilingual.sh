#!/usr/bin/env bash
# certify_dolly_multilingual.sh — one command for the Dolly Parton 5x4 target lane (25 including EN source assets).
#
# Hermetic mode always verifies the canonical five clips and request contract.
# Live mode additionally submits POST /api/script/generate, waits for the job,
# and verifies per-language burned renders, canonical asset identities and Drive
# publication through tests/e2e/dolly_parton_multilingual_runtime_test.go.
#
# Usage:
#   scripts/certify_dolly_multilingual.sh              # hermetic contract
#   PIPELINEGEN_DOLLY_PARTON_LIVE=1 \
#     VELOX_ADMIN_TOKEN=... scripts/certify_dolly_multilingual.sh
#
# Optional:
#   DOLLY_PARTON_LANGUAGES=it,es,de,fr
#   DOLLY_PARTON_RUNTIME_TIMEOUT=20m
#   VELOX_API_BASE_URL=http://127.0.0.1:8000
set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd -- "$SCRIPT_DIR/.." && pwd)"

: "${DOLLY_PARTON_LANGUAGES:=it,es,de,fr}"
: "${DOLLY_PARTON_RUNTIME_TIMEOUT:=20m}"
: "${DOLLY_PARTON_GO_TEST_TIMEOUT:=25m}"
: "${VELOX_API_BASE_URL:=http://127.0.0.1:8000}"

command -v go >/dev/null 2>&1 || { echo "go is required" >&2; exit 2; }

cd "$ROOT_DIR"
echo "[1/3] Dolly hermetic contracts"
go test ./tests/e2e -run 'TestDollyParton(MultilingualRequestContract|ClipBatch)' -count=1

echo "[2/3] localized render and subtitle contracts"
go test ./internal/app/wiring \
  ./internal/capabilities/localization \
  ./internal/capabilities/localization/adapters \
  ./internal/capabilities/cliprender \
  ./internal/capabilities/scripts \
  -run 'Test(LocalizedRenderEnqueuer|LocalizedClipRenderer|OutputProbeFromCertified|CertifiedFacts|Runner_(SourceClipsAudioNone|LocalizedRender))' \
  -count=1

if [[ "${PIPELINEGEN_DOLLY_PARTON_LIVE:-0}" != "1" ]]; then
  echo "[3/3] live lane skipped (set PIPELINEGEN_DOLLY_PARTON_LIVE=1 to submit the target matrix)"
  echo "PASS: hermetic Dolly contract (5 clips × 4 localized targets; 25 total when EN source assets are included)"
  exit 0
fi

# Keep the shell gate aligned with the Go live test: operators may provide the
# bearer token directly or through the repository-standard shell env file.
if [[ -z "${VELOX_ADMIN_TOKEN:-}" && -n "${TOKEN_FILE:-}" && -r "$TOKEN_FILE" ]]; then
  while IFS= read -r line; do
    line="${line#export }"
    if [[ "$line" == VELOX_ADMIN_TOKEN=* ]]; then
      VELOX_ADMIN_TOKEN="${line#VELOX_ADMIN_TOKEN=}"
      VELOX_ADMIN_TOKEN="${VELOX_ADMIN_TOKEN%\"}"
      VELOX_ADMIN_TOKEN="${VELOX_ADMIN_TOKEN#\"}"
      VELOX_ADMIN_TOKEN="${VELOX_ADMIN_TOKEN%\'}"
      VELOX_ADMIN_TOKEN="${VELOX_ADMIN_TOKEN#\'}"
      export VELOX_ADMIN_TOKEN
      break
    fi
  done < "$TOKEN_FILE"
fi

[[ -n "${VELOX_ADMIN_TOKEN:-}" ]] || {
  echo "VELOX_ADMIN_TOKEN is required for the live lane" >&2
  exit 2
}

echo "[3/3] live Dolly target matrix runtime"
PIPELINEGEN_DOLLY_PARTON_LIVE=1 \
  DOLLY_PARTON_LANGUAGES="$DOLLY_PARTON_LANGUAGES" \
  DOLLY_PARTON_RUNTIME_TIMEOUT="$DOLLY_PARTON_RUNTIME_TIMEOUT" \
  VELOX_API_BASE_URL="$VELOX_API_BASE_URL" \
  go test -timeout "$DOLLY_PARTON_GO_TEST_TIMEOUT" \
    ./tests/e2e -run '^TestLiveDollyPartonMultilingualRuntime$' -count=1 -v

echo "PASS: live Dolly multilingual target lane (matrix verified: 5 clips × requested targets)"
