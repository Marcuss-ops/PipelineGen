#!/usr/bin/env bash
# scripts/ci/architecture/checks/lib/50_jobs_profile.sh
#
# Profile scripts/ci/architecture/checks/50_jobs.sh (2144 lines,
# ~115 KB) into a JSON section map for the planned split that mirrors
# the recent pattern "refactor(ci): split ci-architectural-checks.sh
# (4618 => 77 + 59 lib)":
#
#   - 50_jobs_lib.sh  (shared helpers — awk allowlist pre-pass,
#                      comment-stripping, failure-log formatting)
#   - one sub-check per Check section (50_jobs_register_void.sh
#     covers Check 50, 50_jobs_enqueue_string covers Check 51, ...)
#   - slim 50_jobs.sh orchestrator that sources lib + sub-checks
#
# Output: a JSON section map on stdout (or --output). The NEXT commit
# consumes this JSON to mechanically extract the lib + sub-checks.
# Per AGENTS.md / CANONICAL.md, the JSON IS the split plan as a
# machine-consumed artifact — no narrative markdown in the working
# tree.
#
# Architecture (this rewrite):
#   bash loop  -> emit one TSV line per section to a tempfile
#   python step -> read TSV, validate, build JSON via json.dumps
#
# Why split bash from python: the original monolithic bash printf
# approach had two fragility bugs that the prior reviewer flagged —
# (a) line_end off-by-one when a new Check header reused prev_line as
#     the previous section's last-line, overstating every section
#     except the trailing EOF by +1;
# (b) JSON-escape fragility because bash's printf %s does not escape
#     \" or \\ in titles (the python3 validate step caught it but
#     produced no usable output).
# The TSV handoff lets bash do dumb data-collection and python
# guarantee correctly-escaped JSON via stdlib json.dumps.

set -euo pipefail

usage() {
  cat <<'EOF'
Usage: 50_jobs_profile.sh [--input <path>] [--output <path>] [--self-check|--help]

Options:
  --input <path>   Source file to profile.
                   Default: scripts/ci/architecture/checks/50_jobs.sh
  --output <path>  Write JSON section map to <path> (default: stdout).
  --self-check     Verify the profiler is well-formed and exit 0.
  --help, -h       Print this message and exit 0.

Exit codes:
  0  success (output is valid JSON)
  2  usage / input error / JSON validation failure
EOF
}

INPUT="scripts/ci/architecture/checks/50_jobs.sh"
OUTPUT=""
SELF_CHECK=0

while [ $# -gt 0 ]; do
  case "$1" in
    --input)      INPUT="$2"; shift 2 ;;
    --output)     OUTPUT="$2"; shift 2 ;;
    --self-check) SELF_CHECK=1; shift ;;
    --help|-h)    usage; exit 0 ;;
    *)
      echo "50_jobs_profile: unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

[ -f "$INPUT" ] || { echo "50_jobs_profile: input not found: $INPUT" >&2; exit 2; }

# Self-check: bash syntax sanity + module-loadable Python step.
if [ "${SELF_CHECK}" = 1 ]; then
  bash -n "${BASH_SOURCE[0]}" || {
    echo "50_jobs_profile: self-check FAILED (bash syntax error)" >&2
    exit 2
  }
  python3 -c 'import json,sys; json.loads("[]")' || {
    echo "50_jobs_profile: self-check FAILED (python json module)" >&2
    exit 2
  }
  echo "PASS: 50_jobs_profile.sh self-check (input=${INPUT})"
  exit 0
fi

# ── Section parser ─────────────────────────────────────────────────
# Scan the source line-by-line. Each `# ── Check N: ... ──` header
# starts a new section. The previous section's last line is the line
# IMMEDIATELY BEFORE the new header (i.e. current_line - 1). State is
# tracked separately from prev_line so each emitted record has the
# correct [line_start, line_end] range.

TSV="$(mktemp)"
trap 'rm -f "${TSV}"' EXIT

current_id=""
current_title=""
current_start=0
current_end=0
rg_count=0
awk_count=0
fail_count=0
kinds=()
prev_line=0

emit_section() {
  if [ -z "${current_id}" ]; then return 0; fi
  kinds_csv="none"
  if [ "${#kinds[@]}" -gt 0 ]; then
    kinds_csv="$(IFS=,; echo "${kinds[*]}")"
  fi
  safe_title="${current_title//	/ }"
  safe_title="${safe_title//$'\n'/ }"
  printf '%s\t%s\t%d\t%d\t%s\t%d\t%d\t%d\n' \
    "${current_id}" "${safe_title}" \
    "${current_start}" "${current_end}" \
    "${kinds_csv}" \
    "${rg_count}" "${awk_count}" "${fail_count}" \
    >> "${TSV}"
  # Reset per-section accumulators.
  rg_count=0
  awk_count=0
  fail_count=0
  kinds=()
  current_id=""
}

while IFS= read -r line || [ -n "${line}" ]; do
  prev_line=$(( prev_line + 1 ))

  if [[ "${line}" =~ ^#\ ──\ Check\ ([0-9]+[a-z]?):?\ (.+)\ ──$ ]]; then
    # New check header. The previous section's last line is the line
    # just before this header — i.e. prev_line - 1. Emit it now while
    # we still have its accumulators.
    if [ -n "${current_id}" ]; then
      current_end=$(( prev_line - 1 ))
      emit_section
    fi
    current_id="Check ${BASH_REMATCH[1]}"
    current_title="${BASH_REMATCH[2]}"
    current_start="${prev_line}"
    continue
  fi

  # Lines before the first Check header belong to the file preamble;
  # skip accumulator updates until a section is active.
  if [ -z "${current_id}" ]; then continue; fi
  current_end="${prev_line}"

  if [[ "${line}" =~ ^[[:space:]]*(all_void_registers|literals|raw_string_enqueues|raw_wire_calls|raw_complete_calls|all_hits|infra_hits|all_vet|postSetupSetters|nilDispatcher|assetUpserts|all_ips|literal_ips|uuidEventKeys|legacyLifecycleState|placeholderReconcile|legacyStatusKey)= ]]; then
    rg_count=$(( rg_count + 1 ))
  fi

  if [[ "${line}" == *"| awk -F:"* ]]; then awk_count=1; fi
  if [[ "${line}" == *"exit 1"* ]]; then fail_count=1; fi

  if [[ "${line}" =~ ARCH-ALLOWLIST:[[:space:]]*([a-z][-a-z0-9_]+) ]]; then
    kind="${BASH_REMATCH[1]}"
    if [[ ",${kinds[*]}," != *",${kind},"* ]]; then
      kinds+=( "${kind}" )
    fi
  fi
done < "${INPUT}"

# Final emit: prev_line == total lines, so current_end already correct.
if [ -n "${current_id}" ]; then
  emit_section
fi

# ── TSV → JSON (Python) ───────────────────────────────────────────
if ! python3 - "${TSV}" "${OUTPUT:-}" <<'PY'
import json, sys
tsv_path, out_path = sys.argv[1], sys.argv[2]
sections = []
expected_fields = 8
with open(tsv_path, "r", encoding="utf-8") as f:
    for raw in f:
        raw = raw.rstrip("\n")
        if not raw:
            continue
        parts = raw.split("\t")
        if len(parts) != expected_fields:
            print(f"50_jobs_profile: malformed section record: {raw!r}", file=sys.stderr)
            sys.exit(2)
        sid, title, ls, le, allowlist, rg, awk, fail = parts[:expected_fields]
        try:
            sections.append({
                "id": sid,
                "title": title,
                "line_start": int(ls),
                "line_end": int(le),
                "allowlist_kind": allowlist,
                "rg_capture_steps": int(rg),
                "has_awk_allowlist_filter": (awk == "1"),
                "has_hard_fail": (fail == "1"),
            })
        except ValueError as e:
            print(f"50_jobs_profile: bad integer in record: {raw!r}: {e}", file=sys.stderr)
            sys.exit(2)
pretty = json.dumps(sections, indent=2, ensure_ascii=False) + "\n"
if out_path:
    with open(out_path, "w", encoding="utf-8") as f:
        f.write(pretty)
    print(f"50_jobs_profile: wrote section map: {out_path} ({len(sections)} sections)", file=sys.stderr)
else:
    sys.stdout.write(pretty)
PY
then
  echo "50_jobs_profile: JSON emission failed" >&2
  exit 2
fi
