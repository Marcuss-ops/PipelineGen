# scripts/ci/architecture/checks/all_checks/check_69_no_auto_trigger_live_battery.sh
#
# Layer-split (July 2026): extracted from monolithic
# scripts/ci/architecture/checks/all_checks/check_60_governance.sh
# (857 LOC).
#
# Rule 69: NoAutoTriggerLiveBattery (stock pipeline live battery
#                          MUST be operator-only — never auto-
#                          triggered from push / pull_request /
#                          schedule).
#
# Per godlike/06 SSOT: docs/operations/stock-e2e-runbook.md §10.6 +
# this gate + the canonical workflow YAML
# workflows/test_stock_pipeline_live.yaml form a 3-surface lockstep
# contract.
# Per godlike/07 NO-FAKE-AVAILABILITY: v3 tokenize+sort fix verified.

# ── Check 69: NoAutoTriggerLiveBattery (operator-only-by-design, July 2026) ──
# The stock pipeline live battery (scripts/stock_pipeline_live_test.sh) is
# registered for OPERATOR MANUAL-ONLY invocation. The script hits
# yt-dlp + Drive writes + Qdrant mutations — side-effect-heavy, NEVER
# belonging in the PR/push feedback loop. The canonical workflow
# workflows/test_stock_pipeline_live.yaml MUST declare `triggers:` with
# `workflow_dispatch` only.
echo "=== Check 69: NoAutoTriggerLiveBattery (godlike/07, 2026-07-12 v3 fix) ==="
gh_off=""
gh_workflow_dir="${REPO_ROOT}/.github/workflows"
if [ -d "${gh_workflow_dir}" ]; then
    gh_off=$(rg -l 'stock_pipeline_live_test\.sh' "${gh_workflow_dir}" 2>/dev/null || true)
fi
dsl_off=""
dsl_workflow_dir="${REPO_ROOT}/workflows"
internal_workflow="${dsl_workflow_dir}/test_stock_pipeline_live.yaml"
canon_bad=""
canon_missing=""
if [ -d "${dsl_workflow_dir}" ]; then
    dsl_off=$(rg -l --type yaml \
        --glob '!test_stock_pipeline_live.yaml' \
        'stock_pipeline_live_test\.sh' \
        "${dsl_workflow_dir}" 2>/dev/null || true)
    if [ -f "${internal_workflow}" ]; then
        trigger=$(rg -n '^[[:space:]]*triggers:[[:space:]]' "${internal_workflow}" 2>/dev/null || true)
        if [ -z "${trigger}" ]; then
            canon_missing="explicit triggers: line required (godlike/07 minimum-blast-radius, §10.6)"
        else
            trigger_tokens=$(echo "${trigger}" \
                | grep -Eo '(push|pull_request|schedule|workflow_call|workflow_run|workflow_dispatch)' \
                | sort -u || true)
            if [ -z "${trigger_tokens}" ]; then
                canon_bad="no-recognized-trigger-kind-on-triggers-line"
            elif [ "${trigger_tokens}" != "workflow_dispatch" ]; then
                canon_bad="${trigger_tokens}"
            fi
        fi
    fi
fi
if [ -n "${gh_off}${dsl_off}${canon_bad}${canon_missing}" ]; then
    echo "FAIL: stock_pipeline_live_test.sh referenced outside the manual-only operator surface (godlike/07):"
    [ -n "${gh_off}" ] && {
        echo "  .github/workflows/ hits:"
        echo "${gh_off}" | sed 's/^/    /'
    }
    [ -n "${dsl_off}" ] && {
        echo "  workflows/ non-canonical hits:"
        echo "${dsl_off}" | sed 's/^/    /'
    }
    [ -n "${canon_missing}" ] && {
        echo "  canonical file MISSING triggers: line:"
        echo "    ${canon_missing}"
    }
    [ -n "${canon_bad}" ] && {
        echo "  canonical file has non-conforming trigger kinds:"
        echo "${canon_bad}" | sed 's/^/    /'
    }
    exit 1
fi
echo "OK: no auto-trigger references to stock_pipeline_live_test.sh (operator-only invariant holds)"
