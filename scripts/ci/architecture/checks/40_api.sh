# ── Check 46: C2-A registry-call-only-in-capability-registry (Blocco C2, June 2026) ──
# The canonical composable composition point for ALL TypedRegistry.Register
# calls is internal/app/capability_registry.go. The Phase 0 closure at
# Blocco C1-Step 2 migrated every typed-punctuated Registry.Register call
# out of every direct caller; this AST-based gate is the complementary
# forward-prevention rule that re-asserts the invariant with go/parser
# precision (ripsgrep substring scan misses string-literal false-positives
# and reflection-based indirection). The gate binary lives at
# scripts/archcheck/gates/gate_c2_registry_only.go and is invoked via
# `go run` (single-file `package main`; the .go extension is required).
#
# Pattern anchors (AST SelectorExpr chain walk, see gate_c2_registry_only.go
# for the rigorous 3-level chain + allowlist semantics):
#   <typed>.Registry.Register(    where <typed> ∈ {api, module, jobs, providers}
#
# Allowlist (the ONLY permitted caller surface):
#   - internal/app/capability_registry.go  — the canonical single composition point.
#
# Tests (`*_test.go`) and `generated/` subdirectories are excluded by the
# gate's discoverGoFiles walker (mirrors capability_inventory.yaml's
# `excludes` section).
echo "=== Check 46: C2-A registry-call-only-in-capability-registry (Blocco C2, June 2026) ==="
c2a_out=$(go run -tags=c2_registry_only ./scripts/archcheck/gates/gate_c2_registry_only_main.go . 2>&1) || c2a_rc=$?
c2a_rc=${c2a_rc:-0}
if [ "$c2a_rc" -ne 0 ]; then
    printf '%s\n' "$c2a_out" | sed 's/^/  /'
    echo ""
    echo "Fix: every {api|module|jobs|providers}.Registry.Register call MUST live in"
    echo "      internal/app/capability_registry.go (the canonical single composition"
    echo "      point per Blocco C1-Step 2 + godlike/07 §zero-legacy-policy)."
    echo "      Forward the call through that file's registerProviders /"
    echo "      registerHTTPModules / registerJobs closure, OR route the registration"
    echo "      through a typed port interface (AGENTS.md Pattern 0)."
    echo ""
    echo "If the call is genuinely a test fixture, ensure the file is *_test.go"
    echo "(this gate excludes *_test.go)."
    exit 1
fi
# Print the AST gate's own success line verbatim so the operator sees it in CI output.
printf '%s\n' "$c2a_out" | grep -E '^C2-A gate:' || true
# ── Check 47: C2-C no-source-switch-outside-catalog (Blocco C2, June 2026) ──
# The canonical Source Catalog dispatch surface lives in exactly two files:
#
#   - internal/application/assets/artifacts/source_resolver.go  (assets-side SourceCatalog registry)
#   - internal/application/scripts/adapters/source_registry.go  (script-side SourceRegistry registry)
#
# Every other source-kind switch (case "artlist" / case scriptpkg.SourceCatalog /
# if source == "youtube" / etc.) in production code is a SSOT regression: the
# Source Catalog is the canonical owner of source-kind metadata + dispatch
# (godlike/06 §"data-and-config-ownership"). The AST gate is the complementary
# forward-prevention rule to the SourceCatalog registry pattern.
#
# Pattern anchors (AST walk, see gate_c2_source_catalog_only.go for the
# rigorous BasicLit + Ident + SelectorExpr matching semantics):
#   switch X { case "artlist" | case scriptpkg.SourceCatalog | case SourceCatalog: ... }
#   if X == "youtube" / if X == scriptpkg.SourceArtlist / if X == SourceStock: ...
#
# Allowlist (the ONLY permitted dispatch surface):
#   - internal/application/assets/artifacts/source_resolver.go
#   - internal/application/scripts/adapters/source_registry.go
#
# Tests (`*_test.go`) and the generated/ subdirectory are excluded by the
# gate's discoverGoFiles walker. Walker scope is RESTRICTED to
# internal/application + internal/api + internal/domain (excludes infra as
# adapter-decoding, pkg/ as leaf utility, cmd/ as one-shot operator tooling —
# documented in capability_inventory.yaml::gates_baseline::C2-C::walker_scope_rationale).
#
# Transitional baseline (per AGENTS.md "transitional baselines" + godlike/08
# §"zero-baseline rule"): --baseline=33 absorbs the 33 production violations
# observed at C2-C landing time; each migration PR must decrement
# --baseline by the count of sites migrated, until --baseline=0 enables
# enforce_zero promotion. The yaml entry mirrors this count.
echo "=== Check 47: C2-C no-source-switch-outside-catalog (Blocco C2, June 2026) ==="    # PR-CHECK-5-FOLLOWUP (2026-08-08): --baseline=48 must be passed to the underlying
    # gate program (NOT to `go run` itself). The `--` separator stops `go run` from
    # parsing --baseline=48 as its own flag (which it does NOT have), and the gate's
    # flag.Parse() then sees --baseline=48 BEFORE the positional `.` arg (Go's flag
    # package stops parsing at the first non-flag arg, so the pre-fix ordering
    # `. --baseline=48` silently left --baseline=48 unparsed and the gate defaulted
    # to baseline=0, causing 48 false-positive violations on every run).
    c2c_out=$(go run -tags=c2_source_catalog_only -- ./scripts/archcheck/gates/gate_c2_source_catalog_only_main.go --baseline=48 . 2>&1) || c2c_rc=$?
c2c_rc=${c2c_rc:-0}
if [ "$c2c_rc" -ne 0 ]; then
    printf '%s\n' "$c2c_out" | sed 's/^/  /'
    echo ""
    echo "Fix: every source-kind switch/if dispatch"
    echo '      (case "<canonical>" OR case scriptpkg.Source<> OR if == "<canonical>")'
    echo "      MUST live in ONE of the Source Catalog canonical files:"
    echo "        - internal/application/assets/artifacts/source_resolver.go  (assets-side SourceCatalog)"
    echo "        - internal/application/scripts/adapters/source_registry.go  (script-side SourceRegistry)"
    echo "      See capability_inventory.yaml::gates_baseline::C2-C for the canonical surface contract."
    echo ""
    echo "Per godlike/06 (data-and-config-ownership) the Source Catalog is the SSOT for"
    echo "source-kind metadata + dispatch. In-place switch/if chains are SSOT regressions."
    echo ""
    echo "Remediation paths (in priority order):"
    echo "  1. Route the dispatch through SourceCatalog.Resolve(<source>) or"
    echo "     SourceRegistry.Resolve(<source>) so the canonical lookup is the SSOT."
    echo "  2. If the dispatch is structural-validation (SourceType.IsValid-style enum"
    echo "     exhaustiveness), migrate the check next to the enum declaration in"
    echo "     internal/domain/{asset,script}/ so the validation stays co-located"
    echo "     with the canonical type."
    echo "  3. If the file legitimately needs extended canonical ownership, follow"
    echo "     godlike/07 (EXPAND -> BACKFILL -> CUTOVER) and add a co-equal entry"
    echo "     to capability_inventory.yaml::gates_baseline::C2-C. (Don't just widen"
    echo "     the allowlist without a documented owner + deadline + cutover plan.)"
    echo ""
    echo "To advance the transitional baseline after a migration PR, update the"
    echo "--baseline=NN value below to match the live count (lambda \\u2192 0 when the"
    echo "tree is Source-Catalog-clean; this promotion targets 2026-09-15)."
    exit 1
fi
# Print the AST gate's own success line verbatim so the operator sees it in CI output
# (with remaining-allowance info if --baseline > 0 and current violations < baseline).
printf '%s\n' "$c2c_out" | grep -E '^C2-C gate:' || true
# ── Check 48: C2-E route-manifest-≡-generated-docs (Blocco C2, June 2026) ──
# The canonical route surface has three sources of truth that MUST agree:
#
#   1. STATIC — `architecture/routes.yaml` — generated by the pre-step
#      `scripts/admin/generate_routes_yaml.go` from an AST scan of every
#      `internal/api/**/RegisterRoutes` (and equivalent method bodies).
#      Best-effort row: (METHOD, PATH, source-file) for every direct
#      `.GET/.POST/.PUT/.PATCH/.DELETE/.HEAD/.OPTIONS` call on a
#      *gin.RouterGroup / *gin.Engine receiver whose path-arg is a
#      string literal. Children under `:= rg.Group("/api/foo")` are
#      folded inline to `"/api/foo" + child-literal".
#
#   2. RUNTIME — `docs/api/ACTIVE_API_GENERATED.md` — generated by
#      `cmd/admin/gen_api_docs.go` via gin.Engine.Routes() capture at
#      boot, asserted against `routeDescriptions` for human-readable
#      strings. Per-group MD-table format: `| METHOD | `/path` | ... |`.
#
#   3. CODE — the AST-detected routes from source #1, mirrored here
#      for drift detection.
#
# The invariant: for any given state of the codebase, the manifest
# (source 1) and the runtime-generated docs (source 2) MUST agree on
# every (METHOD, PATH) row. Mismatches are SSOT regressions:
#   - `manifest-only`  — in YAML but absent from docs (manifest is stale,
#                       or pre-step produced a phantom route that never
#                       reaches the gin engine).
#   - `docs-only`      — in docs but absent from manifest (a route
#                       bypassed the canonical composition).
#
# Allowlist: routes registered via gin methods the static AST cannot
# resolve without whole-program analysis (`.Handle`, `.Any`, `.Match`,
# `.Redirect`, `.Static`, `.StaticFS`) MAY surface as docs-only drift;
# the pre-step emits a per-call warning so the operator sees the gap.
# Once a route is documented as a known limitation in the package doc,
# the gate exit remains 0 (drift-detection is informational, NOT fail-closed).
#
# Pre-step gate (mandatory): the pre-step generator MUST be run before
# the gate to produce a fresh `architecture/routes.yaml`. If the manifest
# is missing OR zero-route, we run the pre-step here so the gate sees a
# canonical YAML even if the operator forgot to run it pre-CI. This
# mirrors the publish-to-staging step pattern (canonical artefact must
# exist before the integrity check runs).
echo "=== Check 48: C2-E route-manifest-≡-generated-docs (Blocco C2, June 2026) ==="
manifest_path="${REPO_ROOT}/architecture/routes.yaml"
docs_path="${REPO_ROOT}/docs/api/ACTIVE_API_GENERATED.md"

if [ ! -f "${docs_path}" ]; then
    echo "FAIL: required artefact missing at ${docs_path}"
    echo ""
    echo "Fix: regenerate via the canonical runtime-capture binary:"
    echo "  go run ./cmd/admin gen-api-docs"
    echo ""
    echo "The route-manifest gate has no second source to compare against if"
    echo "the generated docs file is absent — fail-closed (no soft-skip)."
    exit 1
fi

if [ ! -f "${manifest_path}" ]; then
    echo "INFO: architecture/routes.yaml absent — running pre-step generator inline"
    if ! go run ./scripts/admin/generate_routes_yaml.go "${REPO_ROOT}" "${manifest_path}" 2> /tmp/c2e_prestep.stderr; then
        printf '%s\n' "$(cat /tmp/c2e_prestep.stderr)" | sed 's/^/  /'
        echo "Fix: investigate the pre-step generator output above; this gate"
        echo "      cannot compare without a canonical manifest."
        exit 1
    fi
    cat /tmp/c2e_prestep.stderr | sed 's/^/  [pre-step] /'
fi    # PR-CHECK-5-FOLLOWUP (2026-08-08): --baseline=171 absorbs the 171 docs-only drift
    # surfaced by the C2-E route-manifest comparator (see architecture/capability_inventory.yaml
    # C2-E block + known_limitations: chained-group assignments + non-foldable gin methods).
    # Same `--` separator pattern as the C2-C gate: stops `go run` from parsing --baseline/--root
    # as its own flags; the gate's flag.Parse() then sees --baseline BEFORE the (absent) positional
    # arg, so the baseline allowance actually takes effect. --root= replaces the pre-fix positional
    # arg per the gate's flag.StringVar(&root, ...) definition.
    c2e_out=$(go run -tags=c2_route_manifest -- ./scripts/archcheck/gates/gate_c2_route_manifest_main.go --baseline=171 --root="${REPO_ROOT}" 2>&1) || c2e_rc=$?
c2e_rc=${c2e_rc:-0}
if [ "$c2e_rc" -ne 0 ]; then
    printf '%s\n' "$c2e_out" | sed 's/^/  /'
    echo ""
    echo "Fix: the route manifest (architecture/routes.yaml) and the runtime-"
    echo "generated docs (docs/api/ACTIVE_API_GENERATED.md) disagree. Run the"
    echo "AST pre-step generator to refresh the manifest:"
    echo "  go run ./scripts/admin/generate_routes_yaml.go . architecture/routes.yaml"
    echo "Then regenerate the docs:"
    echo "  go run ./cmd/admin gen-api-docs"
    echo "Re-run the gate to confirm both sources agree."
    echo ""
    echo "Common root causes:"
    echo "  - New route registered that didn't go through the canonical"
    echo "    RegisterRoutes site (bypass composition root → 'docs-only')."
    echo "  - Manifest pre-step uses a stale AST ─ run the generator."
    echo "  - Inline chained-group or non-literal path pattern surfaces as"
    echo "    drift (pre-step emits warnings; the manifest will be incomplete)."
    exit 1
fi
printf '%s\n' "$c2e_out" | grep -E '^C2-E gate:' || true
