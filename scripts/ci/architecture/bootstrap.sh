# scripts/ci-architectural-checks.sh — CI gate for the architectural checker.
#
# PR-A (June 2026): adds the `--future-ratchet` flag so the 5 Phase 0 rules
# (interface{}/any growth, setter detector, cross-package type alias,
# fake route, handler-to-DB) run in baseline-on-baseline ratchet mode.
# During the minor cycle (this script's current state) the gate fails ONLY
# on regressions vs scripts/archcheck/phase0_baseline.json; existing
# entries in the baseline are accepted.
#
# PR-B (June 2026): adds Check 0 — forbid literal job-type strings outside
# the canonical domain/job/job.go decl. Four canonical constants live there
# (TypeBatchScriptGenerate, TypeClipScriptGenerate, TypeCatalogScriptGenerate,
# TypeMediaCurate); every consumer should reference them by name. New
# quoted-string occurrences of those values outside the canonical decl are
# a SSOT regression and fail this gate.
#
# PR-D (June 2026): adds Check 4 — migration version uniqueness lint.
# Fails when two or more files in `migrations/sqlite/` share the leading
# numeric version prefix. Historically observed as the `069_*.sql` × 2
# incident (surface: composition-test panic at server startup) — the
# duplicate-prefix collision silently picks one candidate at runtime.
# Renumbered to Check 4 because Checks 0/1/2/3 were already claimed by
# PR-B, QDRANT-002, QDRANT-001, and PR 6 (engine.Generate SSOT gate)
# respectively.
#
# PR 6 (June 2026): adds Check 3 — forbid `engine.Generate()` outside
# the canonical `GenerateOneUseCase`. Engine access must flow through
# the typed pipeline orchestrator; any direct call is a SSOT regression.
# Check 3 was introduced on origin/main after this branch forked; the
# merge here places PR-D's lint as Check 4 to avoid the collision.
#
# Promote-to-required checklist (separate follow-up PR):
#   1. Drop `--future-ratchet` from the command line below.
#   2. Fold runPhase0Checks() into runRatchetChecks() in
#      scripts/archcheck/main.go.
#   3. Update docs/architecture/godlike/14_INITIAL_BACKLOG.md — mark
#      Block 1 + the 5 Phase 0 rules as verified_zero: true.

# ── Wave-tracker allowlist consultation (PR-CI-WAVE-ALLOWLIST, July 2026) ──
# Text-based extraction of `id: PR-*` lines from architecture/current.yaml
# whose entry has `status: pending` or `status: in_progress` OR whose entry
# is a `PRE-EXISTING-*-2026-07-04` parent. Per godlike/07 no-fake-availability:
# the wave-tracker file is currently UNPARSEABLE (3+ indent cascade bugs at
# lines ~1582, ~2852, ~2996), so we use a TEXT-BASED FALLBACK rather than
# `yaml.safe_load`. The fallback is the canonical extraction strategy
# (mirrors how the rest of the script uses awk / rg / sed line-by-line).
#
# This is an ADDITIVE informational layer (godlike/07 minimum-blast-radius):
# no existing check's exit-1 semantics change. The baseline log becomes a
# stable pass-rate count instead of an all-violations dump. Future per-check
# integration can opt-in by calling `is_known_acceptable <PR_ID>`.
KNOWN_ACCEPTABLE_IDS=""
extract_known_acceptable_ids_from_yaml() {
    # PR-REMOVE-CI-AWK-SED-FALLBACKS (2026-07-04): replaced the 132-line
    # bash state-machine fallback (which compensated for the broken YAML)
    # with a ~15-line Python heredoc that uses yaml.safe_load. After
    # PR-FIX-YAML-PARSE-LINE-1551 shipped, the YAML is fully parseable,
    # so the text-based fallback is no longer needed.
    #
    # The replacement preserves the canonical external contract:
    #   - sets KNOWN_ACCEPTABLE_IDS to newline-separated ID list
    #   - empty on missing file OR parse error (godlike/07 fail-closed default)
    #   - accepts:
    #     - top-level parents: PRE-EXISTING-* (always) OR status pending/in_progress
    #     - children of PRE-EXISTING-* parents (always)
    #     - children of pending/in_progress parents
    #
    # Per godlike/06 SSOT: the wave-tracker is the SOLE source of truth for
    # PR/issue status. Per godlike/07 minimum-blast-radius: the Python
    # heredoc is ~15 lines (vs 132 lines of bash state-machine); the
    # output format (newline-separated sorted deduped IDs) is byte-identical
    # to the previous implementation, so callers (is_known_acceptable +
    # WAVE_BASELINE_SIZE) need no changes.
    local yaml_path="${REPO_ROOT:-$(pwd)}/architecture/current.yaml"
    KNOWN_ACCEPTABLE_IDS=""
    if [ ! -f "${yaml_path}" ]; then
        return 0
    fi
    KNOWN_ACCEPTABLE_IDS=$(python3 -c '
import sys, yaml
try:
    with open(sys.argv[1], "r", encoding="utf-8") as f:
        docs = yaml.safe_load(f)
    accepted = set()
    for p in (docs if isinstance(docs, list) else []):
        if not isinstance(p, dict):
            continue
        pid, pst = str(p.get("id", "")), p.get("status", "")
        if pid.startswith("PRE-EXISTING-") or pst in ("pending", "in_progress"):
            if pid:
                accepted.add(pid)
            for child in (p.get("linked_issues") or []):
                if isinstance(child, dict) and child.get("id"):
                    accepted.add(str(child["id"]))
    for val in sorted(accepted):
        print(val)
except (yaml.YAMLError, OSError, UnicodeDecodeError):
    pass
' "${yaml_path}" 2>/dev/null || true)
    # godlike/07 no-fake-availability: even if the YAML is unparseable,
    # the silent failure path is preserved (empty KNOWN_ACCEPTABLE_IDS
    # means all violations become "new" until the wave-tracker is
    # re-introduced). The previous bash state-machine had the same
    # silent-fallback contract; this Python replacement preserves it
    # 1:1. NOTE: do NOT `export` — the variable is read in the same
    # shell process (the function-call below + Check 61); child
    # processes don't need it.
}
is_known_acceptable() {
    local pr_id="${1:-}"
    [ -z "${pr_id}" ] && return 1
    [ -z "${KNOWN_ACCEPTABLE_IDS}" ] && return 1
    # Pure bash membership check on the newline-separated allowlist.
    if printf '%s\n' "${KNOWN_ACCEPTABLE_IDS}" | grep -qxF "${pr_id}"; then
        return 0
    fi
    return 1
}
# Populate the global once at script start. Any per-check opt-in can
# call is_known_acceptable <PR_ID> to consult.
extract_known_acceptable_ids_from_yaml
WAVE_BASELINE_SIZE=0
if [ -n "${KNOWN_ACCEPTABLE_IDS}" ]; then
    WAVE_BASELINE_SIZE=$(printf '%s\n' "${KNOWN_ACCEPTABLE_IDS}" | wc -l | awk '{print $1+0}')
fi

# The canonical entrypoint resolves these paths, but assigns them here so
# the wave-tracker lookup above keeps its original pre-REPO_ROOT behavior.
SCRIPT_DIR="${ARCH_CI_ENTRYPOINT_DIR}"
REPO_ROOT="${ARCH_CI_REPO_ROOT}"

# ── Self-check mode (TODO 16, June 2026) ──────────────────────────────
# When invoked with `--self-check`, the script runs each check's regex
# against its corresponding fixture in tests/fixtures/zero_legacy/ and
# verifies the regex catches the forbidden pattern. Exits 0 only if
# every check's pattern still matches its fixture; exits 1 if any
# fixture is missing or any pattern is broken.
#
# Self-check is a UNIT TEST FOR THE REGEXES — it does NOT scan the
# production tree. The standard mode (no flag) is the production gate.
if [ "${1:-}" = "--self-check" ]; then
    FIXTURE_DIR="${REPO_ROOT}/tests/fixtures/zero_legacy"
    if [ ! -d "${FIXTURE_DIR}" ]; then
        echo "FAIL: fixture dir ${FIXTURE_DIR} does not exist (run from repo root)" >&2
        exit 1
    fi

    # Format: name|pattern|fixture_file. Pattern uses ripgrep -E syntax.
    # The fixture MUST contain the forbidden pattern; rg must match.
    check_defs=(
        "Check 8 (SetOutboxHandler/SetMediasearchHandler after construction)|\\.SetOutboxHandler\\(|check_08_setter.go"
        "Check 8 (SetOutboxHandler/SetMediasearchHandler after construction)|\\.SetMediasearchHandler\\(|check_08_setter.go"
        "Check 9 (nil-dispatcher silent fallback)|dispatcher\\s*==\\s*nil\\s*\\{[^}]*return\\s+nil\\b|check_09_nil_dispatcher.go"
        "Check 10 (asset-repo Upsert outside allowlist)|\\.Upsert\\(ctx,|check_10_upsert.go"
        "Check 11 (event_key constructed with random UUID, inline)|eventKey[^\\n]*uuid\\.NewString|check_11_uuid_event_key.go"
        "Check 11 (event_key constructed with random UUID, multiline reverse)|eventID[^\\n]*=\\s*uuid\\.NewString[^\\n]*\\n(?:[^\\n]*\\n){0,3}[^\\n]*eventKey[^\\n]*=[^\\n]*\\beventID\\b|check_11_uuid_event_key.go"
        "Check 11 (event_key constructed with random UUID, multiline forward)|eventKey[^\\n]*\\n(?:[^\\n]*\\n){0,3}[^\\n]*eventID[^\\n]*=\\s*uuid\\.NewString|check_11_uuid_event_key.go"
        "Check 12 (payload_mapper legacy lifecycle_state fallback)|\"lifecycle_state\":\\s*\\w+\\.Status|check_12_payload_mapper_status.go"
        "Check 13 (ListAssetsForReconcile placeholder)|wired as build-time placeholder|check_13_listassets_placeholder.go"
        "Check 14 (BuildPayload legacy status key)|\"status\":\\s*\\w+\\.|check_14_buildpayload_status_key.go"
        "Check 15 (qdrant.NewClient construction)|qdrant\\.NewClient\\(&qdrant\\.Config\\{|check_15_qdrant_config_apikey.go"
        "Check 50 (forbid void Register* signature)|func \\(\\w+ \\*?\\w+\\) [A-Z][A-Za-z0-9_]*[Rr]egister\\([^)]*\\bjobs\\.?Service[^)]*\\)[[:space:]]*\\{|check_50_void_register.go"
        "Check 57 (forbid ports.ScriptRecord literal outside canonical allowlist)|ports\\.ScriptRecord\\{|check_57_scriptrecord_prod_literal.go"
        "Check 55 (forbid legacy Template writes outside canonical allowlist)|Template:\\s|check_55_template_timeline_literal.go"
        "Check 55 (forbid legacy TimelineJSON writes outside canonical allowlist)|TimelineJSON:\\s|check_55_template_timeline_literal.go"
        "Check 60 (forbid t.Skip without godlike/07 honest-limitation comment)|t\\.Skip[a-zA-Z]*\\(|check_60_t_skip.go"
        "Check 62 (forbid RequireAdminToken inline in feature routes)|RequireAdminToken|check_62_inline_middleware.go"
        "Check 62 (forbid extractHeaderToken inline in feature routes)|extractHeaderToken|check_62_inline_middleware.go"
        "Check 62 (forbid EnableAuth inline in feature routes)|EnableAuth|check_62_inline_middleware.go"
        "Check 62 (forbid AdminTokenProvider inline in feature routes)|AdminTokenProvider|check_62_inline_middleware.go"
    )

    failed=0
    seen_names=""
    for def in "${check_defs[@]}"; do
        IFS='|' read -r name pattern fixture <<< "${def}"
        fixture_path="${FIXTURE_DIR}/${fixture}"
        if [ ! -f "${fixture_path}" ]; then
            echo "FAIL: ${name} — fixture ${fixture} missing" >&2
            failed=1
            continue
        fi
        if rg -qU -- "${pattern}" "${fixture_path}" 2>/dev/null; then
            # De-duplicate per check (Check 8 has 2 patterns for 2 methods).
            if [[ "${seen_names}" != *"${name}"* ]]; then
                echo "PASS: ${name} — caught fixture ${fixture}"
                seen_names="${seen_names}|${name}|"
            fi
        else
            echo "FAIL: ${name} — pattern did NOT catch fixture ${fixture} (regex is broken)" >&2
            failed=1
        fi
    done

    if [ "${failed}" -gt 0 ]; then
        echo "" >&2
        echo "Self-check FAILED: at least one regex does not catch its fixture." >&2
        echo "Fix the regex OR update the fixture so the forbidden pattern is present." >&2
        exit 1
    fi
    # Count unique check names (Check 8 has 2 patterns for 2 methods; Check 11
    # has 3 patterns for 3 shapes; etc.) so the summary is accurate. Use
    # bash parameter expansion (${def%%|*}) instead of awk to avoid breakage
    # if a check name ever contains a `|` character.
    unique_names=()
    for def in "${check_defs[@]}"; do
        name="${def%%|*}"
        if [[ " ${unique_names[*]} " != *" ${name} "* ]]; then
            unique_names+=("$name")
        fi
    done
    unique_count=${#unique_names[@]}
    pattern_count=${#check_defs[@]}
    # Count fixture files dynamically (ls instead of hardcoded "7") so the
    # summary stays accurate when new fixtures are added.
    fixture_count=$(ls -1 "${FIXTURE_DIR}"/*.go 2>/dev/null | wc -l)
    echo "All self-checks passed (${pattern_count} patterns / ${unique_count} unique checks / ${fixture_count} fixtures)."
    exit 0
fi

