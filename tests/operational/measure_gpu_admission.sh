#!/usr/bin/env bash
# measure_gpu_admission.sh — the host-side measurement procedure for the two
# GPU-bound acceptance criteria that cannot be decided from a checkout:
#
#   d2  render serialization / GPU admission (TICKET-PIPELINE-CRITICAL-PATH
#       -DEPLOYMENT section "D2"). Question: does overlay render wall reconcile
#       with its accumulated work, and is admission concurrency > 1 beneficial?
#
#   i1  chunk overlap, VRAM-gated (TICKET-I1-CHUNK-PRODUCER-CONTRACT section 6).
#       Question: do two DISJOINT chunks of one plan overlap (wall w strictly
#       less than the sum of their solo walls)?
#
#   preflight (default) resolves the GPU admission CONTRACT and refuses to
#       proceed when the peers sharing the device declare different values.
#
# Why this exists as one script
# -----------------------------
# Both criteria are decided by the same three numbers: how many holders the
# admission contract admits, what one job peaks at in VRAM, and whether the
# wall time of N concurrent jobs is below the serial sum. Splitting them across
# two scripts would let the two halves disagree about the contract.
#
# Premises corrected (2026-09-28) — do not restore the old wording
# ---------------------------------------------------------------
# The D2 ticket still describes `platform/overlays/gpu_gate.go` as "a single
# host-wide flock(LOCK_EX|LOCK_NB)" that must be relaxed before concurrency > 1
# can be measured. That is stale. The gate owns N lock files and admits up to N
# holders (`NewGPUGateWithSlots`); `slots == 1` is the historical behaviour and
# the built-in default is `DefaultGPUGateSlots = 3`
# (`internal/app/wiring/rendering_runtime.go`), pinned explicitly by
# `scripts/systemd/pipelinegen.service.d/gpu-slots.conf`. So "relax the gate"
# is not a prerequisite: the knob is `RENDERINGGEN_GPU_SLOTS`.
#
# The D2 ticket also states the benchmark levels are 1/2/3/4. The benchmark's
# own table is `{1, 2, 3, 5}` (`renderBenchConcurrencyLevels`); this script
# reports whatever the binary reports rather than restating a level list.
#
# The I1 ticket's "Overlap (VRAM-gated, live)" entry names a host as its only
# blocker. The host is necessary but not sufficient: no producer declares chunk
# membership today, so every job charges as `MonolithicSamePlan`, which
# `decide_render_admission` returns `Serial` for BY CONSTRUCTION regardless of
# device memory. Live overlap therefore also needs a chunk producer. This
# script reports that as BLOCKED-ON-PRODUCER and never as a measurement it did
# not take.
#
# Usage:
#   bash tests/operational/measure_gpu_admission.sh [--gate] [--dry] [--out DIR] [MODE]
#
#   MODE       preflight (default) | d2 | i1 | all
#   --gate     exit non-zero when an invariant is violated (default: report only)
#   --dry      resolve and print the plan; run no render, write no artifact
#   --out DIR  artifact directory (default ops/benchmarks/gpu-admission-<UTC>)
#
# Environment overrides:
#   CLIP_PATH                sample mp4 for the D2 real-stack render
#                            (default: the first *.mp4 under tests/operational/payloads)
#   CLIP_COUNT               clips per concurrency level (default: the benchmark's own)
#   RENDERINGGEN_GPU_SLOTS   the overlay admission contract this host declares
#   CHRONON_RENDER_JOB_SLOTS the daemon's REQUESTED render-job capacity
#   GPU_LOCK_PATH            the gate lock path (default: the wired default)
#   RENDERINGGEN_CONFIG      RenderingGen config to read gpu_lanes from
#                            (default: RenderingGen/renderinggen/config.yaml)
#   CHRONON_TEST_BIN         the daemon concurrency gate binary
#
# Exit codes:
#   0  reported (or gate passed)
#   1  setup failure: the host is not ready, so NOTHING was measured
#   2  gate failed on a measured number (including a diverged admission contract)
#   3  a criterion is structurally unreachable here (blocked on a producer),
#      reported as such rather than as a pass or a failure
#
# This script is read-only with respect to the repository and the running
# services: it runs the benchmark, reads env/systemd/config, and writes only
# into its own --out directory. It never edits config, never restarts a unit
# and never deletes an artifact.
set -Eeuo pipefail

DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REFACTORED_ROOT=$(cd "$DIR/../.." && pwd)
MONO_ROOT=$(cd "$REFACTORED_ROOT/.." && pwd)

MODE="preflight"
GATE=0
DRY=0
OUT="${OUT:-}"

want_out=""
for arg in "$@"; do
    case "$arg" in
        --gate) GATE=1 ;;
        --dry) DRY=1 ;;
        preflight | d2 | i1 | all) MODE="$arg" ;;
        --out=*) OUT="${arg#--out=}" ;;
        *) : ;;
    esac
    if [[ "$want_out" == "1" ]]; then
        OUT="$arg"
        want_out=""
    fi
    if [[ "$arg" == "--out" ]]; then want_out="1"; fi
done

RENDERINGGEN_CONFIG="${RENDERINGGEN_CONFIG:-$MONO_ROOT/RenderingGen/renderinggen/config.yaml}"
CHRONON_TEST_BIN="${CHRONON_TEST_BIN:-$MONO_ROOT/Chronon3d/build/chronon/linux-video-test/tests/chronon3d_daemon_render_concurrency_tests}"
GPU_SLOTS_DROPIN="$REFACTORED_ROOT/scripts/systemd/pipelinegen.service.d/gpu-slots.conf"

STAMP=$(date -u +%Y%m%dT%H%M%SZ)
if [[ -z "$OUT" ]]; then
    OUT="$REFACTORED_ROOT/ops/benchmarks/gpu-admission-$STAMP"
fi

say() { printf '%s\n' "$*"; }
rule() { printf '%s\n' "────────────────────────────────────────────────────────────────────────"; }

declare -A SRC=()

# ── contract resolution ─────────────────────────────────────────────────────
# The overlay admission contract has four possible sources, in precedence
# order: the environment, the systemd drop-in that pins it for the service,
# the built-in default, and (for the PEER side) RenderingGen's own config.
# They must agree; when they do not, that divergence IS the finding.
resolve_overlay_slots() {
    if [[ -n "${RENDERINGGEN_GPU_SLOTS:-}" ]]; then
        SRC[pipelinegen]="$RENDERINGGEN_GPU_SLOTS"
        SRC[pipelinegen_from]="environment"
        return
    fi
    if [[ -f "$GPU_SLOTS_DROPIN" ]]; then
        local pinned
        pinned=$(grep -oE 'RENDERINGGEN_GPU_SLOTS=[0-9]+' "$GPU_SLOTS_DROPIN" | tail -1 | cut -d= -f2 || true)
        if [[ -n "$pinned" ]]; then
            SRC[pipelinegen]="$pinned"
            SRC[pipelinegen_from]="$GPU_SLOTS_DROPIN"
            return
        fi
    fi
    SRC[pipelinegen]="3"
    SRC[pipelinegen_from]="built-in default (DefaultGPUGateSlots)"
}

resolve_peer_lanes() {
    if [[ -f "$RENDERINGGEN_CONFIG" ]]; then
        local lanes
        lanes=$(grep -E '^[[:space:]]*gpu_lanes:[[:space:]]*[0-9]+' "$RENDERINGGEN_CONFIG" | tail -1 | grep -oE '[0-9]+' || true)
        if [[ -n "$lanes" ]]; then
            SRC[peer]="$lanes"
            SRC[peer_from]="$RENDERINGGEN_CONFIG"
            return
        fi
    fi
    SRC[peer]="?"
    SRC[peer_from]="not found ($RENDERINGGEN_CONFIG)"
}

resolve_gpu_lock() {
    if [[ -n "${GPU_LOCK_PATH:-}" ]]; then
        SRC[lock]="$GPU_LOCK_PATH"
        return
    fi
    SRC[lock]="${TMPDIR:-/tmp}/pipelinegen/gpu-0.lock"
}

# Payload for the real-stack render. The benchmark needs a real mp4; without
# one it runs stub mode and captures no hardware number, so it cannot decide D2.
resolve_clip_path() {
    if [[ -n "${CLIP_PATH:-}" ]]; then
        SRC[clip]="$CLIP_PATH"
        return
    fi
    local found
    found=$(find "$DIR/payloads" -maxdepth 2 -name '*.mp4' -type f 2>/dev/null | head -1 || true)
    SRC[clip]="${found:-}"
}

resolve_all() {
    resolve_overlay_slots
    resolve_peer_lanes
    resolve_gpu_lock
    resolve_clip_path
}

# ── preflight ───────────────────────────────────────────────────────────────
preflight() {
    rule
    say "GPU admission preflight — $(date -u +%Y-%m-%dT%H:%M:%SZ)"
    rule

    local diverged=0

    say "admission contract"
    say "  PipelineGen overlay slots : ${SRC[pipelinegen]}   [${SRC[pipelinegen_from]}]"
    say "  RenderingGen gpu_lanes    : ${SRC[peer]}   [${SRC[peer_from]}]"
    say "  gate lock path            : ${SRC[lock]}"
    if [[ "${SRC[peer]}" != "?" && "${SRC[pipelinegen]}" != "${SRC[peer]}" ]]; then
        say "  DIVERGENCE: the two processes sharing the GPU declare different"
        say "    admission contracts (${SRC[pipelinegen]} vs ${SRC[peer]}). The drop-in"
        say "    documents that they MUST be equal and changed in the same commit."
        diverged=1
    fi

    say ""
    say "host"
    if command -v nvidia-smi >/dev/null 2>&1; then
        local gpu
        gpu=$(nvidia-smi --query-gpu=name,memory.total,memory.used --format=csv,noheader 2>/dev/null | head -1 || true)
        say "  device                    : ${gpu:-unavailable}"
    else
        say "  device                    : nvidia-smi absent — not a GPU host"
    fi

    say ""
    say "tooling"
    if [[ -x "$CHRONON_TEST_BIN" ]]; then
        say "  daemon policy gate        : present  $CHRONON_TEST_BIN"
    else
        say "  daemon policy gate        : MISSING  $CHRONON_TEST_BIN"
        say "    build: cmake --build <build dir> --target chronon3d_daemon_render_concurrency_tests"
    fi
    if [[ -n "${SRC[clip]}" ]]; then
        local clip_state="MISSING"
        if [[ -f "${SRC[clip]}" ]]; then clip_state="present"; fi
        say "  D2 sample clip            : $clip_state  ${SRC[clip]}"
    else
        say "  D2 sample clip            : MISSING (set CLIP_PATH; without it D2 runs in stub mode and measures no hardware)"
    fi
    if command -v jq >/dev/null 2>&1; then
        say "  jq                        : present"
    else
        say "  jq                        : MISSING"
    fi

    say ""
    say "requested daemon capacity"
    say "  CHRONON_RENDER_JOB_SLOTS  : ${CHRONON_RENDER_JOB_SLOTS:-<unset — daemon uses kTargetConcurrentRenderJobs>}"
    say "  The effective capacity is bounded by the policy verdict. A MONOLITHIC"
    say "  job is Serial by construction, so the slot count is ADMISSION capacity"
    say "  (less queue wait), NOT permission for two Chronon renders to execute"
    say "  at the same time."

    if [[ $diverged -eq 1 ]]; then
        say ""
        say "preflight: DIVERGED — fix the contract before trusting any number below."
        return 2
    fi
    say ""
    say "preflight: contract consistent."
    return 0
}

# ── D2: render concurrency benchmark ────────────────────────────────────────
# Runs the shipped benchmark in real-stack mode. The benchmark declares its own
# levels; this script parses what it emitted and never asserts a level list.
# The deciding number is `speedup = work / wall` per level: >1 at a level above
# 1 means concurrency pays. A level above 1 that does not beat level 1's wall
# closes D2 as "rejected with evidence".
d2() {
    local outfile="$OUT/d2-render-concurrency-$STAMP.log"
    say "d2: real-stack render concurrency benchmark"
    say "    clip       : ${SRC[clip]:-<none>}"
    say "    clip count : ${CLIP_COUNT:-<benchmark default>}"
    say "    contract   : slots=${SRC[pipelinegen]} lock=${SRC[lock]}"
    say "    output     : $outfile"

    local -a envs=(
        "VELOX_BENCH_REAL_RENDER=true"
        "RENDERINGGEN_GPU_SLOTS=${SRC[pipelinegen]}"
        "RENDERINGGEN_GPU_LOCK=${SRC[lock]}"
        "VELOX_BENCH_CLIP_PATH=${SRC[clip]}"
    )
    if [[ -n "${CLIP_COUNT:-}" ]]; then
        envs+=("VELOX_BENCH_CLIP_COUNT=${CLIP_COUNT}")
    fi

    if [[ $DRY -eq 1 ]]; then
        if [[ -z "${SRC[clip]:-}" || ! -f "${SRC[clip]:-}" ]]; then
            say "    [dry] NOTE: no sample clip resolved — a real run would refuse to"
            say "          measure D2 (stub mode captures no hardware numbers)."
        fi
        say "    [dry] would run (in $REFACTORED_ROOT):"
        printf '      %s \\\n' "${envs[@]}" | sed 's/^/    /'
        say "      go test ./internal/capabilities/scripts/ -run TestRenderConcurrencyBenchmark -count=1 -timeout 30m -v"
        return 0
    fi

    if [[ -z "${SRC[clip]:-}" || ! -f "${SRC[clip]:-}" ]]; then
        say ""
        say "d2: no sample clip — NOT MEASURED."
        say "    Provide CLIP_PATH=/path/to/sample.mp4. Without it the benchmark"
        say "    runs stub mode and captures no wall, VRAM or NVENC numbers, so it"
        say "    cannot decide D2."
        return 1
    fi

    mkdir -p "$OUT"
    (
        cd "$REFACTORED_ROOT"
        env "${envs[@]}" go test ./internal/capabilities/scripts/ \
            -run TestRenderConcurrencyBenchmark -count=1 -timeout 30m -v
    ) 2>&1 | tee "$outfile"

    # Parse the benchmark's own stable per-level line (renderBenchReport.String):
    #   concurrency=3 | wall=1234ms | work=2345ms | avg_per_render=781ms | ... 
    local rows
    rows=$(grep -oE 'concurrency=[0-9]+ \| wall=[0-9]+ms \| work=[0-9]+ms' "$outfile" \
        | sed -E 's/concurrency=([0-9]+) \| wall=([0-9]+)ms \| work=([0-9]+)ms/\1|\2|\3/' \
        | sort -t'|' -k1,1n || true)

    if [[ -z "$rows" ]]; then
        say ""
        say "d2: the benchmark emitted no concurrency rows — NOT MEASURED."
        say "    The full output is preserved at $outfile."
        return 1
    fi

    say ""
    say "d2 results"
    printf '%s\n' "$rows" | awk -F'|' '{
        sp = ($3 > 0) ? $3 / $2 : 0;
        printf "  concurrency=%-2s wall=%-8s work=%-8s speedup=%.2fx\n", $1, $2, $3, sp
    }'

    local summary="$OUT/d2-summary.json"
    python3 - "$rows" "$summary" <<'PY'
import json, sys
rows, path = sys.argv[1], sys.argv[2]
levels = []
for line in rows.splitlines():
    c, w, k = line.split("|")
    c, w, k = int(c), int(w), int(k)
    levels.append({"concurrency": c, "wall_ms": w, "work_ms": k,
                   "speedup": (k / w) if w else 0.0})
levels.sort(key=lambda r: r["concurrency"])
serial = next((r for r in levels if r["concurrency"] == 1), None)
best = max(levels, key=lambda r: r["speedup"])
pays = bool(serial and best["concurrency"] > 1 and best["wall_ms"] < serial["wall_ms"])
with open(path, "w") as fh:
    json.dump({
        "criterion": "D2",
        "levels": levels,
        "serial_wall_ms": serial["wall_ms"] if serial else None,
        "best_concurrency": best["concurrency"],
        "best_speedup": round(best["speedup"], 3),
        "concurrency_pays": pays,
    }, fh, indent=2)
print("  summary -> " + path)
PY

    if [[ $GATE -eq 1 ]]; then
        if ! grep -q '"concurrency_pays": true' "$summary"; then
            say ""
            say "d2 GATE: concurrency above 1 does NOT beat the serial wall on this host."
            say "  D2 closes as REJECTED-WITH-EVIDENCE, not as a regression: keep the"
            say "  current admission contract and quote the numbers above."
            return 2
        fi
        say ""
        say "d2 gate: OK — admission concurrency above 1 pays on this host."
    fi
    return 0
}

# ── I1: chunk overlap ───────────────────────────────────────────────────────
# Two halves, and only the second is host-bound.
#
#   1. Is there a producer that declares chunk membership? If not, every job is
#      MonolithicSamePlan and the verdict is Serial BY CONSTRUCTION. No amount
#      of device memory changes it, so the live gate is unreachable here and
#      this script says so instead of reporting a pass.
#   2. Is the device's own verdict for ChunkedDisjointSamePlan ParallelDisjoint?
#      That IS decidable on this host by running the shipped gate binary, which
#      derives the verdict from measured footprints.
i1() {
    local blocked=0
    say "i1: chunk overlap (VRAM-gated, live)"
    say ""
    say "1) producer check — who declares chunk membership?"
    local decl
    decl=$(grep -rIn 'ChunkedDisjointSamePlan' "$MONO_ROOT/RenderingGen" 2>/dev/null | grep -v '_test' || true)
    if [[ -z "$decl" ]]; then
        say "   no RenderingGen producer declares ChunkedDisjointSamePlan."
        say "   RenderingGen submits whole clips (clip lane) and whole plans (overlay"
        say "   lane), so every job charges as kDefaultRenderJobClass ="
        say "   MonolithicSamePlan, and decide_render_admission returns Serial for it"
        say "   regardless of free VRAM."
        say ""
        say "   => I1 Overlap is BLOCKED-ON-PRODUCER, not on this host."
        say "      Declaring the class while the producer still submits monolithic"
        say "      jobs is a no-op by construction and must not be reported as"
        say "      progress (TICKET-I1 section 7)."
        blocked=1
    else
        say "   producer declarations found:"
        printf '%s\n' "$decl" | sed 's/^/     /'
    fi

    say ""
    say "2) device policy verdict — decidable on this host"
    if [[ ! -x "$CHRONON_TEST_BIN" ]]; then
        say "   gate binary missing: $CHRONON_TEST_BIN"
        say "   NOT MEASURED. Build the target named in preflight and re-run."
        return 1
    fi
    say "   binary: $CHRONON_TEST_BIN"
    if [[ $DRY -eq 1 ]]; then
        say "   [dry] would run: $CHRONON_TEST_BIN"
        return 0
    fi
    local outfile="$OUT/i1-daemon-policy-$STAMP.log"
    mkdir -p "$OUT"
    "$CHRONON_TEST_BIN" 2>&1 | tee "$outfile"
    # doctest summary: `[doctest] test cases:  25 |  25 passed | 0 failed | ...`
    local passed
    passed=$(grep -oE 'test cases:[[:space:]]*[0-9]+[[:space:]]*\|[[:space:]]*[0-9]+ passed' "$outfile" \
        | tail -1 | grep -oE '[0-9]+' | paste -sd'/' - || true)
    if [[ -n "$passed" ]]; then
        say "   policy gate: $passed cases passed"
    else
        say "   policy gate: no case summary found (full output at $outfile)"
    fi

    say ""
    if [[ $blocked -eq 1 ]]; then
        say "i1: BLOCKED-ON-PRODUCER. The device-policy half is measured and"
        say "    preserved at $outfile; the live overlap number (wall w < sum of solo"
        say "    walls) cannot be produced until a producer declares chunks."
        return 3
    fi
    say "i1: producer present — run the two-chunk overlap measurement and record"
    say "    wall w against the sum of the solo walls, plus the VRAM peak."
    return 0
}

# ── main ────────────────────────────────────────────────────────────────────
resolve_all

if [[ $DRY -eq 1 ]]; then
    say "[dry] no render will run and no artifact will be written."
fi

rc=0
case "$MODE" in
    preflight)
        preflight || rc=$?
        ;;
    d2 | i1 | all)
        # The contract is always resolved and shown: a measurement is never
        # quoted against an undeclared contract. It is NOT fatal here, though
        # — each mode below still has to reach its own finding, and a mode that
        # produced no number cannot be invalidated by a diverged contract.
        local_pre_rc=0
        preflight || local_pre_rc=$?
        mode_rc=0
        case "$MODE" in
            d2) d2 || mode_rc=$? ;;
            i1) i1 || mode_rc=$? ;;
            all)
                d2 || mode_rc=$?
                if [[ $mode_rc -eq 0 ]]; then i1 || mode_rc=$?; fi
                ;;
        esac
        # The mode's own finding is the more specific one and wins. A diverged
        # contract only fails the gate when a number WAS produced from it.
        if [[ $mode_rc -ne 0 ]]; then
            rc=$mode_rc
        elif [[ $local_pre_rc -ne 0 ]]; then
            say ""
            say "gate: the admission contract diverges, so no number above may be"
            say "      quoted until PipelineGen and RenderingGen declare the same one."
            rc=$local_pre_rc
        fi
        ;;
esac

if [[ $DRY -eq 1 ]]; then
    # A dry run resolves and prints; it never fails. Everything it found is
    # already on stdout, including a diverged contract.
    exit 0
fi

if [[ -d "$OUT" ]]; then
    rule
    say "artifacts preserved in $OUT"
    ls -1 "$OUT" | sed 's/^/  /'
fi

exit $rc
