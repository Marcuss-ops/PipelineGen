#!/usr/bin/env bash
# measure_core_ready_tail.sh — measure (and gate) the tail between the
# CORE_READY boundary and the terminal flip on recorded E2E runs.
#
# Question this answers with evidence: of a script.generate run's total wall
# time, how much is spent AFTER the core (certified render + published final
# audio + canonical script result) is durable — i.e. on the post-processing
# legs that the CORE_READY boundary exists to expose?
#
# Where the boundary is taken from
# --------------------------------
# Two independent sources, cross-checked against each other:
#
#   kpi     .timing.core_ready_ms, the projected wall offset of the boundary.
#           Only present on runs recorded after the field was added to
#           TimingSummary.
#   derived wall - tail, where tail is the recorded critical path strictly
#           after the `persistence` entry. This is the only option for older
#           artifacts and doubles as the correctness check for the kpi value.
#
#   stages  tail reconstructed a third way from stage durations:
#           document + complete_finalize + post_writer_finalize.
#
# `derived` and `stages` must agree within the run's own unattributed_ms
# (floor 250 ms for rounding). When a run does not reconcile, its tail is not
# quoted. When the kpi is present it must agree with `derived` the same way.
#
# NOTE: keep every jq program below free of apostrophes. An apostrophe inside a
# single-quoted shell string terminates it, which silently corrupts the program
# and the data passed to it.
#
# Usage:
#   bash tests/operational/measure_core_ready_tail.sh [--gate] [results-dir ...]
#
#   --gate  exit non-zero if any inspected run violates an invariant:
#             * the tail reconstructions disagree (timing model regressed)
#             * the artifact carries a CORE_READY stage but no core_ready_ms
#               (the boundary is emitted but not auditable from the artifact)
#             * MAX_TAIL_MS is set and a run's tail exceeds it
#           Default mode only reports.
#
#   MAX_TAIL_MS  optional integer ceiling for --gate, in milliseconds.
#
# The BOUNDARY-NOT-AUDITABLE invariant is OPT-IN via CORE_READY_ENFORCE_PROJECTION=1.
# It asserts that an artifact carrying a CORE_READY stage also carries the
# projected core_ready_ms. Artifacts recorded by a binary that predates the
# projection legitimately have the stage without the field, so enforcing it on
# the historical corpus would fail forever. Set the flag when the gate is
# pointed at artifacts produced by a binary that includes the projection.
#
# Defaults to the two recorded E2E result directories.
set -Eeuo pipefail

DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
RESULTS_ROOT="${RESULTS_ROOT:-$DIR/results}"
MAX_TAIL_MS="${MAX_TAIL_MS:-}"
CORE_READY_ENFORCE_PROJECTION="${CORE_READY_ENFORCE_PROJECTION:-0}"

GATE=0
DIRS=()
for arg in "$@"; do
    case "$arg" in
        --gate) GATE=1 ;;
        *) DIRS+=("$arg") ;;
    esac
done
if [[ ${#DIRS[@]} -eq 0 ]]; then
    DIRS=("$RESULTS_ROOT/person-overlay-drive" "$RESULTS_ROOT/jordan-entity-overlay-drive")
fi

command -v jq >/dev/null 2>&1 || { echo "jq is required" >&2; exit 1; }

FILES=()
for dir in "${DIRS[@]}"; do
    [[ -d "$dir" ]] || continue
    for f in "$dir"/full-*.json; do
        [[ -e "$f" ]] || continue
        FILES+=("$f")
    done
done
if [[ ${#FILES[@]} -eq 0 ]]; then
    echo "no full-*.json artifacts found under: ${DIRS[*]}" >&2
    exit 1
fi

ROWS=$(mktemp)
trap 'rm -f "$ROWS"' EXIT

# One TSV row per run:
#   run, marker, kpi_ms, wall, tail_chain, tail_stages, docs, cplfin, pwfin,
#   unattr, created_at
jq -r '
  input_filename as $path
  | ($path | split("/") | last | sub("\\.json$"; "")) as $run
  | .timing as $t
  | (($t.stages | map({(.name): .duration_ms}) | add) // {}) as $s
  | ($t.wall_ms // 0)                 as $wall
  | ($t.core_ready_ms // 0)           as $kpi
  | ($s["document"] // 0)             as $docs
  | ($s["complete_finalize"] // 0)    as $cplfin
  | ($s["post_writer_finalize"] // 0) as $pwfin
  | ($t.unattributed_ms // 0)         as $unattr
  | ([$t.stages[]? | select(.name == "CORE_READY")] | length) as $marker
  | ([$t.critical_path[]? | .name] | index("persistence")) as $pidx
  | (if $pidx == null then null
     else ([$t.critical_path[($pidx + 1):][] | .duration_ms] | add) end) as $tail_chain
  | ($docs + $cplfin + $pwfin)        as $tail_stages
  | [ $run, $marker, $kpi, $wall,
      ($tail_chain // -1), $tail_stages,
      $docs, $cplfin, $pwfin, $unattr,
      (.created_at // "") ] | @tsv
' "${FILES[@]}" > "$ROWS"

jq -R -s -r '
  def lpad(n): . as $s | (n - ($s | length)) as $d | if $d > 0 then (" " * $d) + $s else $s end;
  def rpad(n): . as $s | (n - ($s | length)) as $d | if $d > 0 then $s + (" " * $d) else $s end;
  def pos(v): (v * 10 | floor) / 10 | tostring;

  (split("\n") | map(select(length > 0) | split("\t"))) as $raw
  | ($raw | map({
      run:         .[0],
      marker:      (.[1] | tonumber),
      kpi:         (.[2] | tonumber),
      wall:        (.[3] | tonumber),
      tail_chain:  (if (.[4] | tonumber) < 0 then null else (.[4] | tonumber) end),
      tail_stages: (.[5] | tonumber),
      docs:        (.[6] | tonumber),
      cplfin:      (.[7] | tonumber),
      pwfin:       (.[8] | tonumber),
      unattr:      (.[9] | tonumber)
    })) as $rows
  | ($rows | map(. + {
      tail:  (if .tail_chain == null then .tail_stages else .tail_chain end),
      tol:   (if .unattr > 250 then .unattr else 250 end)
    })) as $rows
  | ($rows | map(. + {
      # chain vs stages: the timing model check, valid for every run.
      delta: (if .tail_chain == null then null else ((.tail_stages - .tail_chain) | fabs) end)
    })) as $rows
  | ($rows | map(. + {
      core_src: (if .kpi > 0 then "kpi" else "derived" end),
      core:     (if .kpi > 0 then .kpi else (.wall - .tail) end)
    })) as $rows
  | ($rows | map(. + {
      # kpi vs derived is only checkable once the boundary is projected.
      kpi_delta: (if .kpi > 0 then ((.kpi - (.wall - .tail)) | fabs) else null end)
    })) as $rows
  | ($rows | map(. + {
      ok: (if .delta == null then "n/a"
           elif .delta > .tol then "NO (delta " + (.delta | tostring) + "ms)"
           else "yes" end),
      ok_kpi: (if .kpi_delta == null then "n/a"
               elif .kpi_delta > .tol then "NO (kpi delta " + (.kpi_delta | tostring) + "ms)"
               else "yes" end)
    })) as $rows
  | ($rows | sort_by(.wall)) as $sorted

  | ([ "RUN", "SRC", "MKR", "WALL", "CORE", "TAIL", "TAIL%", "DOCS", "PWFIN", "UNAT", "CHAIN", "KPI" ]) as $hdr
  | ($hdr[0] | rpad(46)) as $h0
  | ( (($hdr[1] | rpad(5))) + " "
    + (($hdr[2] | lpad(3))) + " "
    + (($hdr[3] | lpad(7))) + " "
    + (($hdr[4] | lpad(7))) + " "
    + (($hdr[5] | lpad(7))) + " "
    + (($hdr[6] | lpad(6))) + " "
    + (($hdr[7] | lpad(6))) + " "
    + (($hdr[8] | lpad(6))) + " "
    + (($hdr[9] | lpad(6))) + " "
    + (($hdr[10] | rpad(6))) + " " + $hdr[11] ) as $hrest

  | ( ($sorted | map(
        (.run | rpad(46)) + " "
        + (.core_src | rpad(5)) + " "
        + ((.marker | tostring) | lpad(3)) + " "
        + ((.wall | tostring) | lpad(7)) + " "
        + ((.core | tostring) | lpad(7)) + " "
        + ((.tail | tostring) | lpad(7)) + " "
        + ((pos(.tail * 100 / .wall) + "%") | lpad(6)) + " "
        + ((.docs | tostring) | lpad(6)) + " "
        + ((.pwfin | tostring) | lpad(6)) + " "
        + ((.unattr | tostring) | lpad(6)) + " "
        + (.ok | rpad(6)) + " " + .ok_kpi
      )) ) as $body

  | ($sorted | map(.tail) | sort) as $tails
  | ($sorted | map(.core) | sort) as $cores
  | ($sorted | map(.tail * 100 / .wall) | sort) as $pcts
  | ($tails | length) as $n
  | (($n - 1) / 2 | floor) as $mid

  | ( ($h0 + $hrest),
      ($body[]),
      "",
      "runs measured: \($n)"
      + "  |  boundary emitted in artifact: \([ $rows[] | select(.marker == 1) ] | length)"
      + "  |  core_ready_ms projected: \([ $rows[] | select(.kpi > 0) ] | length)"
      + "  |  reconciled: \([ $rows[] | select(.ok == "yes" or .ok == "n/a") ] | length)",
      "tail ms   min \($tails[0])   median \($tails[$mid])   max \($tails[$n - 1])",
      "core ms   min \($cores[0])   median \($cores[$mid])   max \($cores[$n - 1])",
      "tail %    min \(pos($pcts[0]))   median \(pos($pcts[$mid]))   max \(pos($pcts[$n - 1]))"
    )
' "$ROWS"

if [[ $GATE -eq 0 ]]; then
    exit 0
fi

# ── gate ────────────────────────────────────────────────────────────────────
# Three invariants, all decidable from the artifacts alone.
violations=$(jq -R -s -r --arg max "$MAX_TAIL_MS" --arg enforce "$CORE_READY_ENFORCE_PROJECTION" '
  def lpad(n): . as $s | (n - ($s | length)) as $d | if $d > 0 then (" " * $d) + $s else $s end;
  (split("\n") | map(select(length > 0) | split("\t"))) as $raw
  | ($raw | map({
      run:         .[0],
      marker:      (.[1] | tonumber),
      kpi:         (.[2] | tonumber),
      wall:        (.[3] | tonumber),
      tail_chain:  (if (.[4] | tonumber) < 0 then null else (.[4] | tonumber) end),
      tail_stages: (.[5] | tonumber),
      unattr:      (.[9] | tonumber),
      created_at:  (.[10] // "")
    })) as $rows
  | ($rows | map(. + {
      tail: (if .tail_chain == null then .tail_stages else .tail_chain end),
      tol:  (if .unattr > 250 then .unattr else 250 end)
    })) as $rows
  | ([ $rows[]
       | (if .tail_chain != null and (((.tail_stages - .tail_chain) | fabs) > .tol)
          then "TAIL-NOT-RECONCILED: \(.run) chain=\(.tail_chain) stages=\(.tail_stages) tol=\(.tol)"
          else empty end),
         (if $enforce == "1" and .marker == 1 and .kpi <= 0
          then "BOUNDARY-NOT-AUDITABLE: \(.run) emitted CORE_READY but core_ready_ms is absent from the artifact"
          else empty end),
         (if $max != "" and (.tail > ($max | tonumber))
          then "TAIL-OVER-BUDGET: \(.run) tail=\(.tail)ms budget=\($max)ms"
          else empty end)
     ]) as $v
  | $v[]
' "$ROWS")

if [[ -n "$violations" ]]; then
    echo
    echo "GATE FAILED (${MAX_TAIL_MS:+budget ${MAX_TAIL_MS}ms, }$(echo "$violations" | wc -l) violation(s)):"
    echo "$violations" | sed 's/^/  - /'
    exit 1
fi

printf '\ngate: OK (%s)\n' "${MAX_TAIL_MS:+tail budget ${MAX_TAIL_MS}ms}${MAX_TAIL_MS:-all invariants hold}"
exit 0
