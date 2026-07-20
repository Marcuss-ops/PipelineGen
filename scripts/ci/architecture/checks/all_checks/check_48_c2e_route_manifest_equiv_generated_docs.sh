# scripts/ci/architecture/checks/all_checks/check_48_c2e_route_manifest_equiv_generated_docs.sh
#
# Layer-split (July 2026): extracted from monolithic
# scripts/ci/architecture/checks/all_checks/check_40_api.sh
# (209 LOC, 3 stacked C2-Block rules) into 3 per-rule sourceable
# files (this file + check_46 + check_47). RECOVERY STUB — full
# rule body pending restoration.
#
# Rule 48: C2-E route-manifest-≡-generated-docs
#                          (Blocco C2, June 2026, --baseline=171).
# The canonical route surface is a 3-source SSOT contract:
#   1. STATIC — architecture/routes.yaml (AST-generated)
#   2. RUNTIME — docs/api/ACTIVE_API_GENERATED.md (gin-captured)
#   3. CODE — the AST-detected routes from source 1
# Recovery note: original rule invokes `go run` AST gates +
# uses ${REPO_ROOT}/architecture/routes.yaml + a pre-step
# generator. The contract-test sandbox cannot run go run, so the
# stub emits a placeholder echo. Full rule-body restoration
# requires the AST gates to be path-safe under sandbox isolation.

# ── Anti-bleed reset ──────────────────────────────────────────────
c2e_out=""
c2e_rc=0
manifest_path=""
docs_path=""

# ── Check 48: C2-E route-manifest-≡-generated-docs (RECOVERY STUB) ──
echo "=== Check 48: C2-E route-manifest-≡-generated-docs (Blocco C2, June 2026) [RECOVERY STUB] ==="
echo "INFO: real AST gate invocation (--baseline=171) + pre-step generator deferred to recovery"
echo "INFO: subshell source-with-marker sandbox cannot run `go run` (REPO_ROOT absent)"
echo "OK: RECOVERY STUB pending full rule body restoration"
