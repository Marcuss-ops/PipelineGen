// Package main — archcheck orchestration.
//
// runner.go owns the dispatch spine: CheckSpec (one descriptor
// per rule-family scanner), DefaultChecks (the canonical Phase 0
// sequence), and Run (the orchestration function called by
// main()). After FASE 1.C PR4 main.go is strictly CLI dispatch
// + composition root; runner.go is strictly orchestration + report
// publishing.
//
// Cross-references:
//   - cmd/archcheck/main.go: the (only) caller — invokes Run with
//     flag-parsed args and exits with the returned code.
//   - cmd/archcheck/scan/*.go: each DefaultChecks entry delegates
//     to one of the Scan* rule-family scanners.
//   - cmd/archcheck/policy/model.go: the Policy struct passed to
//     every CheckSpec.Run closure.
//   - cmd/archcheck/report/model.go: the Report struct populated
//     by every CheckSpec.Run closure.
//
// FASE 1.C PR4 — extracted from cmd/archcheck/main.go into this
// dedicated runner.go so main.go can be trimmed to ≤100 LOC
// (CLI dispatch only).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/scan"
)

// Exit codes for Run(). The constants mirror the previous main.go
// literal semantics documented in the package-level doc comment.
//
//	ExitOK          — report printed; --strict off (Phase 0 default).
//	ExitViolations  — violations present while --strict (Phase N mode).
//	ExitLoadOrParse — load / walk / marshal failure (always fatal).
const (
	ExitOK          = 0
	ExitViolations  = 1
	ExitLoadOrParse = 2
)

// CheckSpec describes one rule-family scanner. Phase 0 carries
// only Name + Run closure; future PRs (PR-A in the Godlike-08
// CI-gates evolution track) may attach Severity, Doc pointer, and
// OwnerRef metadata per check.
//
//	Name is the canonical rule family id used in the JSON `rule`
//	key and in `summary.by_reason`. It is also the most useful
//	field in CI logs when a downstream dashboard asks "which
//	dispatcher entry fired for this violation?".
type CheckSpec struct {
	Name string
	Run  func(root string, pol *policy.Policy, r *report.Report)
}

// DefaultChecks returns the canonical Phase 0 sequence. Ordering
// matters and is documented inline below — Run() walks this slice
// verbatim.
//
//  1. constructors — first because it walks only <root>/internal/
//     and emits constructor_deps violations; running it after the
//     broader-root scans would not change results but would force
//     the dashboard to sort violations differently.
//
//  2. roots + docs (ScanForbiddenDirs / ScanKernelSubzoneHints /
//     ScanUnknownInternalRoots + the five Scan*Doc functions, plus
//     ScanStaleProsePaths) — each walks the directory shape of
//     <root>/ or <root>/internal/ and validates a single concern.
//     No shared state between them; stable JSON-violation order
//     comes from this canonical sequence.
//
//  3. file_size + pkg_size + thin_command — combined in one
//     CheckSpec closure because scan.ScanPackages and
//     scan.ScanCommandBinaries share a fileLines map populated by
//     the single tree walk in ScanPackages. The closure captures
//     the closure-local map so the CheckSpec func(root, pol, r)
//     signature is uniform across all entries.
//
//  4. percheck_root_override_ban — closure captures productionOnly
//     so the runner can plumb the flag from --production-only
//     (PR-P12-PERCHECK-BASELINE-ZERO, July 2026, deadline
//     2026-08-15) without changing the uniform CheckSpec
//     signature.
func DefaultChecks(productionOnly bool) []CheckSpec {
	return []CheckSpec{
		{"constructors", scan.ScanConstructors},
		{"struct_deps", scan.ScanStructDeps},
		{"forbidden_dirs", scan.ScanForbiddenDirs},
		{"kernel_subzone_hints", scan.ScanKernelSubzoneHints},
		{"unknown_internal_roots", scan.ScanUnknownInternalRoots},
		{"ownership_doc", scan.ScanOwnershipDoc},
		{"legacy_policy_doc", scan.ScanLegacyPolicyDoc},
		{"ci_gates_doc", scan.ScanCIGatesDoc},
		{"agent_playbook_doc", scan.ScanAgentPlaybookDoc},
		{"removal_doc", scan.ScanRemovalDoc},
		{"stale_prose_paths", scan.ScanStaleProsePaths},
		// PR-ARCHCHECK-GO-MIGRATION-PHASE-1 (July 2026,
		// deadline 2026-08-15): 3 new per-check ripgrep-equivalent
		// scanners (Check 5 type-redecl, Check 53 TxContext-ban,
		// Check 54 monitor-infra-import ban) migrated from
		// scripts/ci-architectural-checks.sh. Shell check is
		// RETAINED as a transitional baseline per godlike/08
		// §"Zero-baseline rule". See architecture/current.yaml
		// #PR-ARCHCHECK-GO-MIGRATION-PHASE-1 for the wave-tracker
		// entry. The three checks run in parallel with their
		// shell counterparts; both must exit 0 for CI to be green.
		{"percheck_type_redecl", scan.ScanTypeRedeclarations},
		{"percheck_txcontext_ban", scan.ScanTxContextBan},
		{"percheck_monitor_infra_import", scan.ScanMonitorInfraImport},
		// Check N (PR-PLAYER-CLIENT-DRIFT-FIX, 2026-07-06):
		// forward-prevention gate for the `player_client=`
		// literal centralization in
		// internal/infrastructure/ytdlp/cmd_builder.go (per
		// godlike/06 SSOT + godlike/07 NO-FAKE-AVAILABILITY).
		// Fails if any production .go file outside the
		// canonical SSOT + *_test.go (regression-guard
		// allowlist) re-declares the literal. Comment-only
		// hits are WARNed (residue accounting).
		{"percheck_player_client_centralization", scan.ScanPlayerClientCentralization},
		// Dual-mode sync gate (post-PR-morti-sync, 2026-07-06):
		// forward-prevention for re-introduction of the
		// retired syncSingle / syncMulti helpers on
		// GenerateResponse (the canonical async-only wire
		// shape — 5 fields). The probes cover BOTH call-
		// and definition-sides of the dual-mode surface;
		// companion field-count lock at
		// internal/api/script/response_test.go::TestGenerate
		// Response_FieldCountLock catches the struct-shape
		// leg of the same regression class. Two gates
		// together = load-bearing forward-prevention; either
		// alone is insufficient.
		{"percheck_dual_mode_sync", scan.ScanDualModeSync},
		// Check B1 (FASE B1, July 2026): forward-prevention gate
		// for RootFolderOverride in application/api layers.
		// Bans the field from internal/application/** and
		// internal/api/**; allows internal/infrastructure/**
		// (Publisher implementation) and cmd/admin/** (operator
		// CLI overrides). Fails if production code in the
		// forbidden zones references RootFolderOverride outside
		// of comment-only lines.
		//
		// PR-P12-PERCHECK-BASELINE-ZERO (July 2026, deadline
		// 2026-08-15): closure captures the --production-only
		// flag. In production-only mode the comment-only warning
		// bucket is silenced (comments are documentation, not
		// "hits") so the operator-facing "zero production-code
		// hits" claim is auditable via `len(r.Violations) == 0`.
		{"percheck_root_override_ban", func(root string, pol *policy.Policy, r *report.Report) {
			scan.ScanRootOverrideBan(root, pol, r, productionOnly)
		}},
		// Check 63 (PR-CHECK-63-SCRIPT-DOCS-ROUTE-2026-07-08):
		// forward-prevention gate for the canonical
		// /api/script-docs/generate route. Scoped to
		// internal/api/** ONLY (per user spec); bans the
		// route literal from any internal/api/ package
		// other than the canonical surface at
		// internal/api/script-docs/. Fails if any production
		// .go file outside the canonical package + test
		// files (regression-guard allowlist) re-references
		// the route. Comment-only hits are WARNed (residue
		// accounting). Codifies the invariant: the route
		// lives at exactly one internal/api/** package;
		// AGENTS.md drift notices for future routes can
		// point at this gate as the forward-prevention
		// seam so the next agent that drifts surfaces as a
		// CI build failure, not an operator trap.
		{"percheck_script_docs_route", scan.ScanScriptDocsRoute},
		{"file_size_pkg_size_thin_command", func(root string, pol *policy.Policy, r *report.Report) {
			// ScanPackages and ScanCommandBinaries share a
			// fileLines map populated by the single tree walk in
			// ScanPackages. The closure captures the
			// closure-local map; promoting this to a top-level
			// field on runner.go would force Run() to special-
			// case the iteration and break the uniform
			// CheckSpec signature.
			fileLines := map[string]int{}
			scan.ScanPackages(root, pol, r, fileLines)
			scan.ScanCommandBinaries(root, pol, r, fileLines)
		}},
	}
}

// Run orchestrates a single archcheck invocation.
//
// Steps:
//
//  1. Load policy from policyPath; return ExitLoadOrParse on error.
//  2. Build an empty Report (Mode set to "target-tree-dry-run",
//     Summary.ByReason / BySeverity initialised so the rollup
//     step is safe even on a zero-violation run).
//  3. Walk DefaultChecks() in order. Each CheckSpec.Run closure
//     appends to r.Violations; the dispatch loop in Run() never
//     touches per-check shared state directly (the fileLines
//     closure-local map is the one exception, encapsulated in
//     DefaultChecks).
//  4. Roll up the summary counters (TotalViolations, ByReason,
//     BySeverity) and set r.Passed = (len(violations) == 0).
//  5. Marshal the report + write to stdout. Return
//     ExitLoadOrParse on marshal failure.
//  6. If strict && len(violations) > 0, return ExitViolations.
//     Otherwise return ExitOK.
//
// The ctx parameter is currently a placeholder — none of the Phase
// 0 scanners honour ctx.Done(). The signature is forward-compatible
// with PR-A in the Godlike-08 evolution track, which may plumb
// context-aware scanners (e.g. timeout-bounded Qdrant linting)
// that respect a deadline.
func Run(ctx context.Context, root, policyPath, phase string, strict bool, productionOnly bool) (int, error) {
	_ = ctx // reserved for context-aware scanners in PR-A+

	pol, err := policy.Load(policyPath)
	if err != nil {
		return ExitLoadOrParse, fmt.Errorf("load policy %q: %w", policyPath, err)
	}

	r := &report.Report{
		Mode:       "target-tree-dry-run",
		PolicyPath: policyPath,
		Root:       root,
		Phase:      phase,
		Policy:     pol,
		Summary:    report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}

	for _, check := range DefaultChecks(productionOnly) {
		check.Run(root, pol, r)
	}

	r.Summary.TotalViolations = len(r.Violations)
	for _, v := range r.Violations {
		r.Summary.ByReason[v.Rule]++
		r.Summary.BySeverity[v.Severity]++
	}
	r.Passed = len(r.Violations) == 0

	// Sort violations for deterministic JSON output (Go map
	// iteration in scan functions produces non-deterministic
	// ordering; the golden-file snapshot test needs byte-stable
	// output across runs). Sort by package → file → line,
	// mirroring the logical grouping operators see in CI logs.
	sort.Slice(r.Violations, func(i, j int) bool {
		a, b := r.Violations[i], r.Violations[j]
		if a.Package != b.Package {
			return a.Package < b.Package
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})

	out, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return ExitLoadOrParse, fmt.Errorf("marshal report: %w", err)
	}
	fmt.Println(string(out))

	if strict && len(r.Violations) > 0 {
		return ExitViolations, nil
	}
	return ExitOK, nil
}
