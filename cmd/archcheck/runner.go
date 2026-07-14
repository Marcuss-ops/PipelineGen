// Package main — archcheck orchestration.
//
// runner.go owns the dispatch spine: CheckSpec, DefaultChecks and Run.
// Each DefaultChecks entry delegates to a scanner in cmd/archcheck/scan.
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

// CheckSpec describes one rule-family scanner.
type CheckSpec struct {
	Name string
	Run  func(root string, pol *policy.Policy, r *report.Report)
}

// DefaultChecks returns the canonical Phase 0 sequence. Ordering matters.
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
		// Forward-prevention: type-redecl, TxContext-ban, monitor-infra-import.
		{"percheck_type_redecl", scan.ScanTypeRedeclarations},
		{"percheck_txcontext_ban", scan.ScanTxContextBan},
		{"percheck_monitor_infra_import", scan.ScanMonitorInfraImport},
		// Forward-prevention: centralize player_client literal.
		{"percheck_player_client_centralization", scan.ScanPlayerClientCentralization},
		// Forward-prevention: ban dual-mode sync helpers.
		{"percheck_dual_mode_sync", scan.ScanDualModeSync},
		// Forward-prevention: RootFolderOverride in application/api layers.
		// In --production-only mode, the comment-only warning bucket is silenced
		// so the "zero production-code hits" claim is auditable via `len(r.Violations) == 0`.
		{"percheck_root_override_ban", func(root string, pol *policy.Policy, r *report.Report) {
			scan.ScanRootOverrideBan(root, pol, r, productionOnly)
		}},
		// Forward-prevention: /api/script-docs/generate route canonical owner.
		{"percheck_script_docs_route", scan.ScanScriptDocsRoute},
		// Forward-prevention: spec_aliases.go territory.
		{"percheck_spec_aliases", scan.ScanSpecAliasesTerritory},
		// Forward-prevention: retired voiceover.* aliases.
		{"percheck_voiceover_alias_ban", func(root string, pol *policy.Policy, r *report.Report) {
			scan.ScanVoiceoverAliasBan(root, pol, r, productionOnly)
		}},
		// Forward-prevention: closed admin-reindex bypass route.
		{"percheck_direct_indexer_bypass_closed", func(root string, pol *policy.Policy, r *report.Report) {
			scan.ScanDirectIndexerBypassClosed(root, pol, r, productionOnly)
		}},
		// Forward-prevention: API module Dependencies bags > 8 fields.
		{"percheck_api_module_deps_max_8", scan.ScanApiModuleDepsMax8},
		// Forward-prevention: canonical architectural surfaces (73-77).
		{"percheck_assetbinder_ssot", scan.ScanAssetBinderSSOT},
		{"percheck_drive_access_ssot", scan.ScanDriveAccessSSOT},
		{"percheck_metadata_registry", scan.ScanMetadataRegistry},
		// Forward-prevention: Asset.Metadata namespaced key registry.
		{"percheck_metadata_key_registry", scan.ScanMetadataKeys},
		{"percheck_input_immutability", scan.ScanInputImmutability},
		{"percheck_sourcestager_transformer", scan.ScanSourceStagerTransformer},
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
		// Forward-prevention: AssetState canonical 14-count invariant.
		{"percheck_asset_state_canonical_14", scan.ScanAssetStateCanonical14},
		// Forward-prevention: StateAssetX shadow declarations.
		{"percheck_asset_state_no_shadow_enum", scan.ScanAssetStateNoShadowEnum},
		// Forward-prevention: migration 157 DEFAULT literal alignment.
		{"percheck_157_asset_state_migration_default_wire", scan.ScanAssetStateMigration157DefaultWire},
		// Forward-prevention: RightsStatus canonical 6-count invariant.
		{"percheck_rights_status_canonical_6", scan.ScanRightsStatusCanonical6},
		// Forward-prevention: ReviewStatus canonical 4-count invariant.
		{"percheck_review_status_canonical_4", scan.ScanReviewStatusCanonical4},
		// Forward-prevention: ClipIngestPipeline literal surface.
		{"percheck_clip_ingest_pipeline_canonical_1", func(root string, pol *policy.Policy, r *report.Report) {
			// Append-only convention (mirrors scanner-returning-slice patterns
			// elsewhere in the codebase): the scanner returns a typed
			// []report.Violation. Dropping the slice makes the percheck
			// a silent no-op — the godlike/06 SSOT forward-prevention
			// invariant would be invisible to the operator.
			r.Violations = append(r.Violations, scan.ScanClipIngestPipelineCanonical1(root)...)
		}},
		// Forward-prevention: SceneAssetBinder field-write purity.
		{"percheck_binder_scene_field_writes", scan.ScanBinderSceneFieldWrites},
		// Forward-prevention: application-layer direct qdrant imports.
		{"percheck_qdrant_index_import_ban", scan.ScanQdrantIndexImportBan},
		// Forward-prevention: INDEXED state writer SSOT.
		{"percheck_indexed_state_writer_ssot", scan.ScanIndexedStateWriterSSOT},
		// Forward-prevention: MediaTransformer DTO infra fields.
		{"percheck_mediatransformer_no_infra_fields", scan.ScanMediaTransformerNoInfraFields},
		// Forward-prevention: deleted internal/domain/job imports.
		{"percheck_no_domain_job_compatibility_aliases", func(root string, pol *policy.Policy, r *report.Report) {
			scan.ScanNoDomainJobCompatibilityAliases(root, pol, r, productionOnly)
		}},
		// Forward-prevention: generic generation facade re-introduction.
		{"percheck_no_generic_generation_facade", func(root string, pol *policy.Policy, r *report.Report) {
			scan.ScanNoGenericGenerationFacade(root, pol, r, productionOnly)
		}},
		// Forward-prevention: legacy providers.SearchAggregator re-introduction.
		{"percheck_providers_searchaggregator_ban", func(root string, pol *policy.Policy, r *report.Report) {
			scan.ScanProvidersSearchAggregatorBan(root, pol, r, productionOnly)
		}},
		// Forward-prevention: SceneAssetBinder → SceneSynthesizer route.
		{"percheck_assetbinder_no_scenesynthesizer", func(root string, pol *policy.Policy, r *report.Report) {
			scan.ScanAssetBinderNoSynthesizer(root, pol, r, productionOnly)
		}},
		// Forward-prevention: asset.index.requested emitter SSOT.
		{"percheck_asset_committer_event_ssot", func(root string, pol *policy.Policy, r *report.Report) {
			scan.ScanAssetCommitterEventSSOT(root, pol, r, productionOnly)
		}},
		// Forward-prevention: UpsertPoints sole caller.
		{"percheck_upsert_points_sole_owner", func(root string, pol *policy.Policy, r *report.Report) {
			scan.ScanUpsertPointsSoleOwner(root, pol, r, productionOnly)
		}},
		// Forward-prevention: SearchAggregator singleton construction.
		{"percheck_search_aggregator_singleton", scan.ScanSearchAggregatorSingleton},
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
// The ctx parameter is currently a placeholder. The signature is forward-compatible
// for context-aware scanners that respect a deadline.
func Run(ctx context.Context, root, policyPath, phase string, strict bool, productionOnly bool) (int, error) {
	_ = ctx // reserved for context-aware scanners

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
