#!/usr/bin/env bash
# Local, deterministic Rust migration certification.
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
RUST_TOOLCHAIN="${RUSTUP_TOOLCHAIN:-stable}"
MANIFEST=rust/Cargo.toml
MUSCLES=rust/target/release/pipelinegen-muscles
VISUALNER=rust/target/release/visualner
MEDIASAMPLER=rust/target/release/mediasampler
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/pipelinegen-rust-cert.XXXXXX")"
trap 'rm -rf "$TMP_DIR"' EXIT
FAILED=()
NOT_RUN=(crash_recovery rss_1000_request old_vs_rust_benchmark)

gate() {
  local name="$1"; shift
  if "$@" >"$TMP_DIR/$name.log" 2>&1; then
    printf '%-34s PASS\n' "$name"
  else
    printf '%-34s FAIL\n' "$name"
    sed -n '1,80p' "$TMP_DIR/$name.log"
    FAILED+=("$name")
  fi
}

gate cargo_test env RUSTUP_TOOLCHAIN="$RUST_TOOLCHAIN" rustup run "$RUST_TOOLCHAIN" cargo test --manifest-path "$MANIFEST"
gate cargo_check env RUSTUP_TOOLCHAIN="$RUST_TOOLCHAIN" rustup run "$RUST_TOOLCHAIN" cargo check --workspace --manifest-path "$MANIFEST"
gate cargo_clippy env RUSTUP_TOOLCHAIN="$RUST_TOOLCHAIN" rustup run "$RUST_TOOLCHAIN" cargo clippy --workspace --manifest-path "$MANIFEST" -- -D warnings
gate build_muscles make build-muscles
gate build_release env RUSTUP_TOOLCHAIN="$RUST_TOOLCHAIN" rustup run "$RUST_TOOLCHAIN" cargo build --release --manifest-path "$MANIFEST"

gate muscles_health bash -c "printf '%s\\n' '{\"version\":\"mediaexec.v1\",\"operation\":\"health\"}' | '$MUSCLES' | python3 -c 'import json,sys; x=json.load(sys.stdin); assert x.get(\"ok\") is True and x.get(\"operation\")==\"health\", x'"
gate visualner_golden bash -c "printf '%s\\n' '{\"source_text\":\"Gerard Butler spoke at an event in London.\",\"entity_count\":4}' | '$VISUALNER' | python3 -c 'import json,sys; x=json.load(sys.stdin)[\"entities\"]; got={(e[\"text\"],e[\"type\"]) for e in x}; assert {(\"Gerard Butler\",\"PERSON\"),(\"London\",\"LOCATION\")}.issubset(got), got'"
gate visualner_grounding bash -c "printf '%s\\n' '{\"source_text\":\"Greek salad combines tomatoes, feta cheese and olives.\",\"entity_count\":4}' | '$VISUALNER' | python3 -c 'import json,sys; src=\"Greek salad combines tomatoes, feta cheese and olives.\"; x=json.load(sys.stdin)[\"entities\"]; assert all(src[e[\"start\"]:e[\"end\"]]==e[\"text\"]==e[\"evidence\"] for e in x), x'"

ner_request='{"source_text":"Greek salad combines tomatoes, feta cheese and olives.","entity_count":4}'
printf '%s\n' "$ner_request" | "$VISUALNER" >"$TMP_DIR/ner.first"
for _ in $(seq 1 100); do printf '%s\n' "$ner_request" | "$VISUALNER" | cmp -s - "$TMP_DIR/ner.first" || { FAILED+=(visualner_determinism_100); break; }; done
if [[ " ${FAILED[*]} " != *" visualner_determinism_100 "* ]]; then printf '%-34s PASS\n' visualner_determinism_100; fi

sampler_request='{"scene":{"id":"scene-a","subject":"Greek Salad","terms":["feta","tomatoes","olives"]},"candidates":[{"id":"a","label":"woman wearing boxing gloves","generic_similarity":0.99,"owner_segment_id":"scene-a"},{"id":"b","label":"Greek salad with feta tomatoes olives","generic_similarity":0.80,"owner_segment_id":"scene-a"},{"id":"c","label":"Mediterranean restaurant exterior","generic_similarity":0.70,"owner_segment_id":"scene-a"}],"allow_reuse":false}'
gate mediasampler_semantic bash -c "printf '%s\\n' '$sampler_request' | '$MEDIASAMPLER' | python3 -c 'import json,sys; x=json.load(sys.stdin); assert x[\"winner_id\"]==\"b\",x; r={a[\"candidate_id\"]:a for a in x[\"results\"]}; assert r[\"a\"].get(\"rejection\")==\"subject_mismatch\",x'"
printf '%s\n' "$sampler_request" | "$MEDIASAMPLER" >"$TMP_DIR/sampler.first"
for _ in $(seq 1 100); do printf '%s\n' "$sampler_request" | "$MEDIASAMPLER" | cmp -s - "$TMP_DIR/sampler.first" || { FAILED+=(mediasampler_determinism_100); break; }; done
if [[ " ${FAILED[*]} " != *" mediasampler_determinism_100 "* ]]; then printf '%-34s PASS\n' mediasampler_determinism_100; fi

gate invalid_operation_rejected bash -c "printf '%s\\n' '{\"version\":\"mediaexec.v1\",\"operation\":\"run_command\"}' | '$MUSCLES' | python3 -c 'import json,sys; x=json.load(sys.stdin); assert x.get(\"ok\") is False and x.get(\"operation\")==\"invalid\",x'"
gate cancellation_zombie_scan go test ./internal/platform/media/rustexec -run 'TestRustProcessRunnerKillsDescendantsOnCancellation|TestPersistentRustProcessRunnerReusesDispatcherProcess' -count=1
gate vidrush_media_intelligence make verify-media-intelligence
gate full_repository_regression make verify-full

if rg -n 'rusqlite|sqlx|database/sql|qdrant::|qdrant_.*(write|upsert)|google_drive|drive::|dotenv|std::fs.*(secret|credential)' rust --glob '!target/**' --glob '*.rs' >"$TMP_DIR/boundaries.log"; then
  printf '%-34s FAIL\n' rust_boundary_scan; sed -n '1,80p' "$TMP_DIR/boundaries.log"; FAILED+=(rust_boundary_scan)
else
  printf '%-34s PASS\n' rust_boundary_scan
fi
if rg -n 'NewHybridExtractor|localnlp\.NewExtractor|NewOllamaEntityExtractorAdapter|VidRushWindowRanker' internal cmd --glob '*.go' --glob '!**/*_test.go' >"$TMP_DIR/legacy.log"; then
  printf '%-34s FAIL\n' legacy_go_compute_scan; sed -n '1,80p' "$TMP_DIR/legacy.log"; FAILED+=(legacy_go_compute_scan)
else
  printf '%-34s PASS\n' legacy_go_compute_scan
fi

for item in "${NOT_RUN[@]}"; do printf '%-34s NOT_RUN\n' "$item"; done
printf '\nRUST MIGRATION CERTIFICATION\n'
if ((${#FAILED[@]} == 0 && ${#NOT_RUN[@]} == 0)); then
  printf 'FINAL_RUST_MIGRATION_CERTIFIED    TRUE\n'
  exit 0
fi
printf 'FINAL_RUST_MIGRATION_CERTIFIED    FALSE\nFAILED: %s\n' "${FAILED[*]}"
printf 'NOT_RUN: %s\n' "${NOT_RUN[*]}"
exit 1
