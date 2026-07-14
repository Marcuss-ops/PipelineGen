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
		// Check PR-AUDIT-8 (July 2026, P2): forward-prevention gate
		// that bans `spec_aliases.go` files outside the two
		// approved territories (internal/application/images/
		// generated/ + internal/application/images/retrieved/).
		// spec_aliases.go is the user-spec surface that exposes
		// type aliases + sentinel errors + compile-time assertions.
		// Per godlike/06 SSOT, only the two canonical directories
		// may host this file; a copy-paste into a new module
		// creates a silent drift (godlike/07 NO-FAKE-AVAILABILITY
		// regression). The gate catches new spec_aliases.go at the
		// filename level (no content-based matching — simple
		// basename check).
		{"percheck_spec_aliases", scan.ScanSpecAliasesTerritory},
		// PR-VOICEOVER-ALIASES-RETIRE Sub-PR C (ship_date
		// 2026-07-10): forward-prevention gate for the 6
		// retired voiceover.* type aliases. Each alias has
		// a canonical owner per godlike/06 SSOT (persistence.
		// VoiceoverRecord, ports.VoiceoverRepository, workflow/
		// promo.{Request,Result,Response}, translation.
		// DefaultPromoLanguages); the voiceover.* prefix was
		// a proxy re-export godlike/06 drift. Production-code
		// references to ANY of the 6 retired aliases are
		// SeverityError violations; comment-only references
		// are WARNed (residue accounting). productionOnly
		// mode silences the WARN bucket so the operator-
		// facing "zero production-code hits" claim is
		// auditable via `len(r.Violations) == 0`.
		{"percheck_voiceover_alias_ban", func(root string, pol *policy.Policy, r *report.Report) {
			scan.ScanVoiceoverAliasBan(root, pol, r, productionOnly)
		}},
		// Check PR-API-MODULE-DEPS-MAX-8 (July 2026):
		// forward-prevention gate that enforces the canonical
		// maximum-8-fields invariant on every API module's
		// `Dependencies` (or `Deps`) bag. Scans ONLY the
		// canonical Build-entrypoint location
		// (`internal/api/**/module.go`) via Go AST
		// (`go/parser`+`go/ast`+`go/token`) — strictly more
		// robust than regex for grouped multi-decl fields,
		// embedded fields, and multiline field declarations.
		// The bypass list encodes the already-split clips
		// surface from PR-CLIPS-7-MODULES-UPPER-CLIPSMODULE
		// (upper + 7 sub-descriptors); the upper's wide
		// Dependencies is the WIRING surface for the 7 sub-
		// descriptors, not the route-installer surface, so
		// it is exempted by canonical-operator intent rather
		// than by field-count. EVERY other module.go under
		// internal/api/ whose `Dependencies` bag carries > 8
		// fields trips the gate. Bypass-list hits are
		// WARNed (godlike/07 residue accounting) instead of
		// violated so the running audit lane stays residue-
		// honest even when the bypass list needs later cleanup.
		{"percheck_api_module_deps_max_8", scan.ScanApiModuleDepsMax8},
		// Check 73-77 (Wave 5, July 2026): forward-prevention gates for
		// canonical architectural surfaces:
		//   - SceneAssetBinder SSOT
		//   - Drive access through delivery.Publisher
		//   - typed metadata registry (reduce map[string]any)
		//   - input immutability
		//   - SourceStager + MediaTransformer usage
		{"percheck_assetbinder_ssot", scan.ScanAssetBinderSSOT},
		{"percheck_drive_access_ssot", scan.ScanDriveAccessSSOT},
		{"percheck_metadata_registry", scan.ScanMetadataRegistry},
		// Check PR-79 (PR-METADATA-REGISTRY-FOUNDATION, July 2026):
		// forward-prevention gate for the Asset.Metadata
		// name-spaced KEY alphabet (separate concern from the
		// Wave 5 `percheck_metadata_registry` map[string]any
		// ban). Canonical SSOT is
		// internal/domain/asset/metadata_registry.go which
		// declares every provider.* style key with Owner + Type.
		// Severity=error: an unregistered name-spaced key
		// surfaces as a CI build failure.
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
		// Check PR-CATALOG-MULTILINGUA step 7 (July 2026):
		// forward-prevention gate that pins
		// AssetState's canonical-14-count invariant. The
		// canonical file (internal/domain/asset/asset_state.go)
		// MUST declare exactly 14 StateAssetX const
		// entries; a future agent who adds a 15th without
		// updating the type surface, the matrix test, AND
		// this gate surfaces as a CI build failure rather
		// than a silent schema drift. Comment-only
		// references to the canonical surface are residue-
		// accounted (godlike/07).
		{"percheck_asset_state_canonical_14", scan.ScanAssetStateCanonical14},
		// Check PR-CATALOG-MULTILINGUA step 7 (July 2026):
		// forward-prevention gate that bans `StateAssetX
		// AssetState = "..."` const declarations OUTSIDE
		// the canonical SOLE owner. Mirrors the image-asset
		// literal-ban shape: the canonical SSOT is the
		// ONLY file permitted to declare the alphabet; a
		// shadow declaration anywhere else is a godlike/06
		// SSOT violation risking alphabet drift
		// (godlike/07 NO-FAKE-AVAILABILITY regression).
		// Test files (`_test.go` suffix) and the scanner's
		// own package (cmd/archcheck/scan/**) are exempt.
		{"percheck_asset_state_no_shadow_enum", scan.ScanAssetStateNoShadowEnum},
		// Check PR-CATALOG-MULTILINGUA step 7+ GAMMA (July 2026):
		// forward-prevention gate for the migration 157
		// column DEFAULT literal wire alignment. The column
		// DEFAULT is the runtime companion of the typed
		// initial-sentinel string declared in
		// internal/domain/asset/asset_state.go; drift
		// between the two surfaces (e.g., a future agent
		// renames the typed initial sentinel but leaves
		// the migration DEFAULT stale, OR vice versa)
		// surfaces as SeverityError. SQL line comments
		// inside the migration file are residue-accounted
		// as WARN; a missing migration 157 file surfaces
		// as a typed violation (godlike/07 fail-closed).
		// Production-canary end-to-end sanity lives in
		// percheck_157_asset_state_migration_default_wire_test.go::TestScanAssetStateMigration157DefaultWire_ProductionCanary.
		{"percheck_157_asset_state_migration_default_wire", scan.ScanAssetStateMigration157DefaultWire},
		// Check PR-CLIPINGEST-PIPELINE step 10 (July 2026):
		// forward-prevention gate that pins RightsStatus's
		// canonical-6-count invariant. The canonical file
		// (internal/domain/asset/rights_state.go) MUST declare
		// exactly 6 RightsStatusX const entries; a future agent
		// who adds a 7th without updating the type surface,
		// the membership test, AND this gate surfaces as a CI
		// build failure rather than a silent alphabet drift.
		// Mirrors percheck_asset_state_canonical_14.
		{"percheck_rights_status_canonical_6", scan.ScanRightsStatusCanonical6},
		// Check PR-CLIPINGEST-PIPELINE step 10 (July 2026):
		// forward-prevention gate that pins ReviewStatus's
		// canonical-4-count invariant. Companion to
		// percheck_rights_status_canonical_6; the rights
		// surface is two-dimensional (RightsStatus +
		// ReviewStatus) and the gates enforce each dimension's
		// count independently. Mirrors the second half of the
		// asset_state canonical-14 pattern.
		{"percheck_review_status_canonical_4", scan.ScanReviewStatusCanonical4},
		// Check PR-CLIPINGEST-PIPELINE step 8 (July 2026):
		// forward-prevention gate for the canonical
		// `ClipIngestPipeline` literal surface. Per godlike/06
		// SSOT, ONLY internal/application/assets/ingest/
		// clip_ingest_pipeline.go (and its _test sibling) may
		// declare `type ClipIngestPipeline struct` or use a
		// `ClipIngestPipeline{...}` / `*ClipIngestPipeline{...}` /
		// package-qualified `<pkg>.ClipIngestPipeline{...}`
		// composite literal. Percheck lifts the max_struct_deps
		// ceiling via policy.max_clip_ingest_pipeline_fields: 9
		// for this struct family; production lifts for any
		// future structurally-significant struct follow the
		// same pattern (file-local max_*_fields key + Policy
		// field + registered check).
		{"percheck_clip_ingest_pipeline_canonical_1", func(root string, pol *policy.Policy, r *report.Report) {
			// Append-only convention (mirrors scanner-returning-slice patterns
			// elsewhere in the codebase): the scanner returns a typed
			// []report.Violation. Dropping the slice makes the percheck
			// a silent no-op — the godlike/06 SSOT forward-prevention
			// invariant would be invisible to the operator.
			r.Violations = append(r.Violations, scan.ScanClipIngestPipelineCanonical1(root)...)
		}},
		// Wave 1.3 (July 2026): forward-prevention gate that
		// pins the SceneAssetBinder-purity invariant. The
		// canonical ScenePlanner (internal/application/scripts/
		// scene/scene_planner.go) is the SOLE owner of every
		// scene.Text / scene.Title / scene.Kind / scene.Index
		// write (Wave 1.1 SSOT — per user request, scope is
		// exactly the four fields the user listed, NOT scene.ID
		// to honor AGENTS.md "no features beyond explicit
		// request"). Any file inside the scene/ package that
		// carries a literal assignment to a banned field OUTSIDE
		// the canonical owner (and outside the test-file
		// allowlist) surfaces as a CI build failure rather than
		// a silent Wave 1.1 drift. The gate does NOT trip on
		// permitted scene.Bindings.Clip / scene.Bindings.Stock
		// writes (the binder's canonical responsibility).
		// Comment-only references are residue-accounted
		// (godlike/07).
		{"percheck_binder_scene_field_writes", scan.ScanBinderSceneFieldWrites},
		// Bulk YouTube uploader + image ingest drift-fix
		// (July 2026): forward-prevention gate that bans the
		// import of `internal/infrastructure/qdrant` from
		// `internal/application/**` packages. Per godlike/06
		// SSOT, the canonical qdrant-write surface is the
		// outbox worker (`internal/application/jobs/outbox/`),
		// which receives `asset.index.requested` /
		// `asset.points.upserted` events emitted by
		// CommitAsset. Direct application-layer qdrant imports
		// force-couple the write to the wire-shape contract
		// and bypass the transactional outbox (godlike/07
		// NO-FAKE-AVAILABILITY). The bulk YouTube uploader
		// (Wave 2) and image ingest (Wave 2) already route the
		// write through CommitAsset → outbox; this gate freezes
		// the contract so a future contributor reintroducing
		// direct qdrant imports surfaces as a CI build failure.
		// Exempt zones per user directive: cmd/admin/**
		// (operator tooling) + internal/application/jobs/outbox/**
		// (canonical outbox→qdrant emitter). _test.go files are
		// exempt (regression-guard allowlist).
		{"percheck_qdrant_index_import_ban", scan.ScanQdrantIndexImportBan},
		// Check PR-INDEXED-STATE-SSOT (July 2026):
		// forward-prevention gate that pins the godlike/06 SSOT contract:
		// media_assets.index_state='INDEXED' transitions ONLY via the
		// canonical outbox consumer (IndexingHandler -> clipindexer.IndexClip
		// -> setIndexedAt). Per user directive: "Fare in modo che lo stato
		// asset.index.state=INDEXED passi solo dal consumer outbox
		// dedicato."
		{"percheck_indexed_state_writer_ssot", scan.ScanIndexedStateWriterSSOT},
		// Check PR-MEDIATRANSFORMER-RENAME step 1 (July 2026):
		// forward-prevention gate that bans Drive/Qdrant/SQLite
		// fields in the MediaTransformer DTOs (TransformSpec +
		// RenditionSet). Per godlike/06 SSOT, MediaTransformer is
		// a local-only transformer — it takes a StagedSource (already
		// on local disk via the canonical SourceStager port) and
		// produces a local RenditionSet. Drive/DB/Qdrant concerns
		// are out of scope and belong to the orchestrator +
		// finalizer + commit layers downstream. Step 1 of the rename
		// is ONLY a type rename (Processor → MediaTransformer,
		// ProcessInput → TransformSpec, ProcessResult → RenditionSet);
		// the forbidden fields STAY in the DTOs for now and this
		// gate WILL trip on them as a forward-pointer to step 2,
		// which deletes the fields. Comment-only references are
		// WARNed (residue accounting, godlike/07).
		{"percheck_mediatransformer_no_infra_fields", scan.ScanMediaTransformerNoInfraFields},
		// Check PR-COMPATIBILITY-ALIASES-REMOVE-DOMAIN-JOB step 1 (July 2026):
		// forward-prevention gate that bans imports of the deleted
		// `internal/domain/job` package. Per godlike/06 SSOT the
		// kernel is the SOLE owner of job-mechanism types and does
		// NOT carry feature-specific job names (`job.TypeScriptGenerate`,
		// `images.JobGenerate`, `job.TypeVoiceoverGenerate`, etc.).
		// The legacy `internal/domain/job` package was a back-compat
		// alias layer (`Job`/`Status`/`Event`/`Filter`/`Store`)
		// shadowing the canonical kernel surface — deletion enforces
		// the dual-source-of-truth ban. Comment-only references are
		// WARNed (residue accounting, silenced under productionOnly).
		// _test.go is included in the scan surface because a test
		// importing the deleted path cannot compile. Family
		// precedent: percheck_no_generic_generation_facade.
		{"percheck_no_domain_job_compatibility_aliases", func(root string, pol *policy.Policy, r *report.Report) {
			scan.ScanNoDomainJobCompatibilityAliases(root, pol, r, productionOnly)
		}},
		// Check PR-GENERATION-FACADE-REMOVE (commit 7, July 2026):
		// forward-prevention gate that BANS any re-introduction of
		// the application-zone or domain-zone generic generation
		// facade. The two packages were git-rm'd because zero
		// production callers remained and the canonical proprietary
		// APIs (book/lesson/script/batch) did not exist on disk (the
		// internal/api/content/ surface is a doc-only shell). Per
		// godlike/06 SSOT, the per-domain packages own their handler
		// wiring — a generic inter-domain facade creates a godlike/07
		// NO-FAKE-AVAILABILITY regression. Comment-only references
		// to the banned import paths are WARNed (residue accounting,
		// godlike/07).
		{"percheck_no_generic_generation_facade", func(root string, pol *policy.Policy, r *report.Report) {
			scan.ScanNoGenericGenerationFacade(root, pol, r, productionOnly)
		}},
		// Check 78 (PR-DIAGNOSI-FINALE rule 1, July 2026):
		// forward-prevention gate for the canonical
		// SceneAssetBinder's interaction with the canonical
		// SceneSynthesizer. Per godlike/06 SSOT, the
		// SceneAssetBinder (internal/application/scripts/scene/
		// binder.go) is the SOLE owner of scene-binding
		// mutations, AND it MUST NOT round-trip through the
		// SceneSynthesizer (which lives in the same package
		// for pre-binder orchestration). A binder ->
		// synthesizer route either re-runs synthesis
		// (idempotent cost) OR overwrites already-shaped
		// scene.Text with synthesized prose (silent P0 #2
		// invariant regression). The gate pins this
		// invariant by inspection of the canonical binder
		// file alone; comment-only references to
		// SceneSynthesizer are residue-accounted (godlike/07).
		{"percheck_assetbinder_no_scenesynthesizer", func(root string, pol *policy.Policy, r *report.Report) {
			scan.ScanAssetBinderNoSynthesizer(root, pol, r, productionOnly)
		}},
		// Check 79 (PR-DIAGNOSI-FINALE rule 3, July 2026):
		// forward-prevention gate for the canonical
		// `asset.index.requested` outbox-event emission
		// surface. Per godlike/06 SSOT + QDRANT-002 atomicity
		// invariant, the AssetCommitter.CommitAsset pathway
		// (internal/application/assets/persistence/committer.go
		// or internal/application/assets/processing/
		// asset_committer.go) is the SOLE legitimate emitter
		// for the canonical `asset.index.requested.v1`
		// envelope. Production-code emission of the literal
		// outside the canonical AssetCommitter chain (and
		// outside the documented exempt zones) surfaces as a
		// CI build failure. Comment-only references are
		// residue-accounted (godlike/07); productionOnly
		// mode silences the WARN bucket so the operator-
		// facing "zero production-code hits" claim is
		// auditable via len(r.Violations) == 0.
		{"percheck_asset_committer_event_ssot", func(root string, pol *policy.Policy, r *report.Report) {
			scan.ScanAssetCommitterEventSSOT(root, pol, r, productionOnly)
		}},
		// Check 80 (PR-DIAGNOSI-FINALE rule 4, July 2026):
		// forward-prevention gate for the canonical sole
		// caller of `transport.Client.UpsertPoints(`. Per
		// godlike/06 SSOT, the IndexingHandler outbox
		// consumer in
		// internal/infrastructure/qdrant/indexing/ is the
		// sole production caller of qdrant.UpsertPoints.
		// The regex requires a dot-receiver before the
		// function name so the transport-package function
		// declaration line (`func (c *Client) UpsertPoints(
		// ...)`) is naturally exempt. Test fixtures are
		// exempt; the residue is documented in
		// migrations/api/archcheck-strict-baseline.json
		// (godlike/07 migration-window permit).
		{"percheck_upsert_points_sole_owner", func(root string, pol *policy.Policy, r *report.Report) {
			scan.ScanUpsertPointsSoleOwner(root, pol, r, productionOnly)
		}},
		// Check 81 (PR-DIAGNOSI-FINALE rule 6, July 2026):
		// forward-prevention gate for the canonical
		// SearchAggregator singleton invariant. Per
		// godlike/06 SSOT, the canonical SearchAggregator
		// is constructed EXACTLY ONCE at the composition
		// root (internal/app/registry_search.go or
		// internal/app/search_backends.go). The gate
		// counts production-code construction sites of
		// `search.NewAggregator(` and asserts the count
		// is exactly 1; 0 callers trips a "singleton-missing"
		// violation (godlike/07 NO-FAKE-AVAILABILITY
		// regression); > 1 callers trips a
		// "singleton-duplicated" violation (godlike/06
		// SSOT singleton-divergence). The workerdoctor
		// .Aggregator is a separate-domain helper and is
		// naturally exempt by the regex (requires the
		// search. qualifier). Test files are exempt.
		{"percheck_search_aggregator_singleton", scan.ScanSearchAggregatorSingleton},
		// Wave-22 forward-prevention gate (godlike/08 evolution
		// PR2, post-Wave 22 per architecture/current.yaml):
		// bans low-level infrastructure imports from the API
		// transport layer. Go-side mirror of
		// scripts/ci/architecture/checks/check_19_api_infrastructure_imports.sh;
		// reads the canonical allowlist at
		// docs/migrations/api-infrastructure-imports-allowlist.txt
		// for grandfathered surfaces (godlike/06 SSOT-marker
		// discipline with owner + deadline). The rule id is
		// promoted to a Phase-N hard gate via the
		// pol.HardGates evaluate-and-escalate pass in Run().
		{"percheck_api_infrastructure_imports", scan.ScanAPIInfrastructureImports},
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

	// Wave-22 hard-gate promotion (godlike/08 evolution PR2).
	// Build the lookup set once; for every violation whose
	// Rule is in pol.HardGates, escalate Severity to
	// report.SeverityError and set hasHardGate. Recompute
	// BySeverity from scratch (the prior rollup inserted the
	// unaware severity; we now want operators to see the
	// escalated count in by_severity.error). Exit decision
	// below extends to `hasHardGate || (strict && len>0)` so
	// the gate fires regardless of --strict.
	hasHardGate := false
	if len(pol.HardGates) > 0 {
		hgSet := make(map[string]bool, len(pol.HardGates))
		for _, id := range pol.HardGates {
			hgSet[id] = true
		}
		for i := range r.Violations {
			if hgSet[r.Violations[i].Rule] {
				r.Violations[i].Severity = string(report.SeverityError)
				hasHardGate = true
			}
		}
		if hasHardGate {
			r.Summary.BySeverity = map[string]int{}
			for _, v := range r.Violations {
				r.Summary.BySeverity[v.Severity]++
			}
		}
	}
	r.HasHardGateHits = hasHardGate

	// Sort violations for deterministic JSON output (Go map
	// iteration in scan functions produces non-deterministic
	// ordering; the golden-file snapshot test needs byte-stable
	// output across runs). Sort by package → file → line → rule →
	// note, mirroring the logical grouping operators see in CI logs.
	// The note tie-breaker is required for percheck_type_redecl
	// violations, which carry multi-site diagnostics in the Note
	// field while File/Line are empty.
	sort.Slice(r.Violations, func(i, j int) bool {
		a, b := r.Violations[i], r.Violations[j]
		if a.Package != b.Package {
			return a.Package < b.Package
		}
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Rule != b.Rule {
			return a.Rule < b.Rule
		}
		return a.Note < b.Note
	})

	out, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return ExitLoadOrParse, fmt.Errorf("marshal report: %w", err)
	}
	fmt.Println(string(out))

	if (strict && len(r.Violations) > 0) || hasHardGate {
		return ExitViolations, nil
	}
	return ExitOK, nil
}
