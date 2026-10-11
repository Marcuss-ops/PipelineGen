#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
PIPELINEGEN_ROOT="$(cd -- "${SCRIPT_DIR}/../.." && pwd)"
WORKSPACE_ROOT="$(cd -- "${PIPELINEGEN_ROOT}/.." && pwd)"
RUST_MANIFEST="${PIPELINEGEN_ROOT}/rust/pipelinegen-muscles/Cargo.toml"
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
run_group 'Rust SI unit + worker suite' cargo +stable test --manifest-path "$RUST_MANIFEST" scene_intelligence --lib
run_group 'Rust unseen Level-C smoke evaluation' cargo +stable test --manifest-path "$RUST_MANIFEST" --test si_level_c_unseen
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
run_group 'Rust SI lexical benchmark (1k/3k/10k)' env \
  SCENE_INTELLIGENCE_BENCH_OUTPUT="$BENCH_OUTPUT" \
  cargo +stable bench --manifest-path "$RUST_MANIFEST" --bench scene_intelligence

printf '\n=== Benchmark artifact ===\n%s\n' "$BENCH_OUTPUT"
python3 - "$BENCH_OUTPUT" <<'PY'
import json
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
report = json.loads(path.read_text())
assert [row["words"] for row in report["sizes"]] == [1020, 3056, 10000], report["sizes"]
assert all(row["sentences"] > 0 and row["measured_runs"] > 0 for row in report["sizes"])
assert all(row["total_ms"]["p50_ms"] > 0 for row in report["sizes"])
assert "no embeddings" in report["description"].lower()
for row in report["sizes"]:
    print(f"{row['words']} words: sentences={row['sentences']} p50={row['total_ms']['p50_ms']:.3f}ms "
          f"p95={row['total_ms']['p95_ms']:.3f}ms p99={row['total_ms']['p99_ms']:.3f}ms "
          f"sentences/s={row['sentences_per_second']:.0f}")
print(f"peak_rss_mb={report['peak_rss_mb']} cpu_percent={report['process_cpu_percent']}")
PY
CERT_RESULTS+=("Rust SI lexical benchmark + JSON artifact validation")
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
printf '\nALL LOCAL CERTIFICATION GROUPS PASSED\n'
