#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
PIPELINEGEN_ROOT="$(cd -- "${SCRIPT_DIR}/../.." && pwd)"
WORKSPACE_ROOT="$(cd -- "${PIPELINEGEN_ROOT}/.." && pwd)"
BENCH_OUTPUT="${PIPELINEGEN_ROOT}/.cache/scene-intelligence/rust_benchmark.json"
REPORT_OUTPUT="${PIPELINEGEN_ROOT}/.cache/scene-intelligence/certification-report.json"
CERT_RESULTS=()

run_group() {
  local name="$1"
  shift
  printf '\n=== %s ===\n' "$name"
  "$@"
  printf 'PASS: %s\n' "$name"
  CERT_RESULTS+=("$name")
}

cd "$PIPELINEGEN_ROOT"
run_group 'PipelineGen SI / timeline / admission / RenderingGen bridge Go packages' go test -count=1 \
  ./internal/kernel/script/ \
  ./internal/capabilities/overlays/ \
  ./internal/capabilities/scripts/ \
  ./internal/capabilities/scripts/overlay/ \
  ./internal/platform/media/rustexec/

cd "${WORKSPACE_ROOT}/RenderingGen/renderinggen"
run_group 'RenderingGen overlay / processor / workspace Go suites' go test -count=1 \
  ./internal/overlay/ ./internal/processor/ ./internal/workspace/...
run_group 'RenderingGen concurrent lease regression (repeat 10 times)' go test ./cmd/renderinggen \
  -run '^TestConcurrentLeasesDoNotShareState$' -count=10

cd "${WORKSPACE_ROOT}/RenderingGen/queue"
run_group 'RenderingGen queue full Go suite' go test -count=1 ./...

cd "$PIPELINEGEN_ROOT"
if [[ -f "$BENCH_OUTPUT" ]]; then
  printf '\n=== Existing BrainRust benchmark artifact ===\n%s\n' "$BENCH_OUTPUT"
  python3 - "$BENCH_OUTPUT" <<'PY'
import json
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
report = json.loads(path.read_text())
assert report.get("schema_version"), "benchmark artifact schema_version missing"
assert report.get("sizes"), "benchmark artifact sizes missing"
for row in report["sizes"]:
    assert row.get("words", 0) > 0 and row.get("sentences", 0) > 0, row
    print(f"{row['words']} words: {row['sentences']} sentences")
PY
  CERT_RESULTS+=("Existing BrainRust benchmark artifact validation")
else
  printf '\nNo local Scene Intelligence benchmark artifact; benchmark runs in BrainRust CI.\n'
fi
python3 - "$REPORT_OUTPUT" "${CERT_RESULTS[@]}" <<'PY'
import json
import pathlib
import sys
from datetime import datetime, timezone

path = pathlib.Path(sys.argv[1])
result = {
    "schema_version": "scene-intelligence-certification.v1",
    "generated_at_utc": datetime.now(timezone.utc).isoformat(),
    "passed_groups": sys.argv[2:],
    "passed_count": len(sys.argv) - 2,
    "pending_external_gates": [
        "independent final-dataset precision/recall/F1",
        "human recap and visual-readability review",
        "live production rendering comparison",
        "E5 inference benchmark for Scene Intelligence (not implemented in this lexical detector)",
    ],
}
path.parent.mkdir(parents=True, exist_ok=True)
path.write_text(json.dumps(result, indent=2) + "\n")
print(f"\nCertification report written: {path}")
print(f"Passed groups: {result['passed_count']}; external gates pending: {len(result['pending_external_gates'])}")
PY
printf '\nALL PIPELINEGEN CONSUMER CERTIFICATION GROUPS PASSED\n'
