# scripts/ci/architecture/checks/all_checks/check_48_c2e_route_manifest_equiv_generated_docs.sh
#
# Rule 48: C2-E route-manifest-≡-generated-docs (Blocco C2, June 2026).
# The runtime generator is the sole authority for both committed artifacts:
#   - architecture/routes.yaml (deduplicated runtime route manifest)
#   - docs/api/ACTIVE_API_GENERATED.md (runtime route documentation)
# The legacy AST helper is diagnostic-only and is not invoked here.

# ── Anti-bleed reset ──────────────────────────────────────────────
c2e_out=""
c2e_rc=0
REPO_ROOT="${REPO_ROOT:-$(pwd)}"
manifest_path="${REPO_ROOT}/architecture/routes.yaml"
docs_path="${REPO_ROOT}/docs/api/ACTIVE_API_GENERATED.md"

echo "=== Check 48: C2-E route-manifest-≡-generated-docs (Blocco C2, June 2026) ==="
if [ ! -f "${manifest_path}" ] || [ ! -f "${docs_path}" ]; then
    echo "FAIL: paired runtime route artifacts are missing"
    echo "Fix: run `go run ./cmd/admin gen-api-docs` from the repository root"
    exit 1
fi

c2e_out=$(go run -tags=c2_route_manifest -- ./scripts/archcheck/gates/gate_c2_route_manifest_main.go --baseline=171 --root="${REPO_ROOT}" 2>&1) || c2e_rc=$?
c2e_rc=${c2e_rc:-0}
if [ "$c2e_rc" -ne 0 ]; then
    printf '%s\n' "$c2e_out" | sed 's/^/  /'
    echo "Fix: regenerate the paired runtime artifacts with `go run ./cmd/admin gen-api-docs`"
    exit 1
fi
printf '%s\n' "$c2e_out" | grep -E '^C2-E gate:' || true
