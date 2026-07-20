package main

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/scan"
)

// CheckSpec describes one rule-family scanner.
type CheckSpec struct {
	Name string
	Run  func(root string, pol *policy.Policy, r *report.Report)
}

// DefaultChecks returns the canonical scanner sequence.
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
		{"percheck_type_redecl", scan.ScanTypeRedeclarations},
		{"percheck_txcontext_ban", scan.ScanTxContextBan},
		{"percheck_monitor_infra_import", scan.ScanMonitorInfraImport},
		{"percheck_player_client_centralization", scan.ScanPlayerClientCentralization},
		{"percheck_dual_mode_sync", scan.ScanDualModeSync},
		{"percheck_root_override_ban", func(root string, pol *policy.Policy, r *report.Report) {
			scan.ScanRootOverrideBan(root, pol, r, productionOnly)
		}},
		{"percheck_script_docs_route", scan.ScanScriptDocsRoute},
		{"percheck_spec_aliases", scan.ScanSpecAliasesTerritory},
		{"percheck_voiceover_alias_ban", func(root string, pol *policy.Policy, r *report.Report) {
			scan.ScanVoiceoverAliasBan(root, pol, r, productionOnly)
		}},
		{"percheck_api_module_deps_max_8", scan.ScanApiModuleDepsMax8},
		{"percheck_assetbinder_ssot", scan.ScanAssetBinderSSOT},
		{"percheck_drive_access_ssot", scan.ScanDriveAccessSSOT},
		{"percheck_metadata_registry", scan.ScanMetadataRegistry},
		{"percheck_metadata_key_registry", scan.ScanMetadataKeys},
		{"percheck_input_immutability", scan.ScanInputImmutability},
		{"percheck_sourcestager_transformer", scan.ScanSourceStagerTransformer},
		{"file_size_pkg_size_thin_command", func(root string, pol *policy.Policy, r *report.Report) {
			fileLines := map[string]int{}
			scan.ScanPackagesForMode(root, pol, r, fileLines, productionOnly)
			scan.ScanCommandBinaries(root, pol, r, fileLines)
		}},
		{"percheck_asset_state_canonical_14", scan.ScanAssetStateCanonical14},
		{"percheck_asset_state_no_shadow_enum", scan.ScanAssetStateNoShadowEnum},
		{"percheck_157_asset_state_migration_default_wire", scan.ScanAssetStateMigration157DefaultWire},
		{"percheck_rights_status_canonical_6", scan.ScanRightsStatusCanonical6},
		{"percheck_review_status_canonical_4", scan.ScanReviewStatusCanonical4},
		{"percheck_clip_ingest_pipeline_canonical_1", func(root string, _ *policy.Policy, r *report.Report) {
			r.Violations = append(r.Violations, scan.ScanClipIngestPipelineCanonical1(root)...)
		}},
		{"percheck_binder_scene_field_writes", scan.ScanBinderSceneFieldWrites},
		{"percheck_qdrant_index_import_ban", scan.ScanQdrantIndexImportBan},
		{"percheck_api_policy_literals", func(root string, pol *policy.Policy, r *report.Report) {
			scan.ScanAPIPolicyLiterals(root, pol, r, productionOnly)
		}},
		{"percheck_pipeline_map_carrier_ban", func(root string, pol *policy.Policy, r *report.Report) {
			scan.ScanPipelineMapCarrierBan(root, pol, r, productionOnly)
		}},
		{"percheck_no_pipeline_mapstr", func(root string, pol *policy.Policy, r *report.Report) {
			scan.ScanNoPipelineMapStr(root, pol, r, productionOnly)
		}},
		{"percheck_indexed_state_writer_ssot", scan.ScanIndexedStateWriterSSOT},
		{"percheck_mediatransformer_no_infra_fields", scan.ScanMediaTransformerNoInfraFields},
		{"percheck_no_domain_job_compatibility_aliases", func(root string, pol *policy.Policy, r *report.Report) {
			scan.ScanNoDomainJobCompatibilityAliases(root, pol, r, productionOnly)
		}},
		{"percheck_domain_job_import_baseline", func(root string, pol *policy.Policy, r *report.Report) {
			scan.ScanDomainJobBaselineRatchet(root, pol, r, productionOnly)
		}},
		{"percheck_no_generic_generation_facade", func(root string, pol *policy.Policy, r *report.Report) {
			scan.ScanNoGenericGenerationFacade(root, pol, r, productionOnly)
		}},
		{"percheck_assetbinder_no_scenesynthesizer", func(root string, pol *policy.Policy, r *report.Report) {
			scan.ScanAssetBinderNoSynthesizer(root, pol, r, productionOnly)
		}},
		{"percheck_asset_committer_event_ssot", func(root string, pol *policy.Policy, r *report.Report) {
			scan.ScanAssetCommitterEventSSOT(root, pol, r, productionOnly)
		}},
		{"percheck_upsert_points_sole_owner", func(root string, pol *policy.Policy, r *report.Report) {
			scan.ScanUpsertPointsSoleOwner(root, pol, r, productionOnly)
		}},
		{"percheck_search_aggregator_singleton", scan.ScanSearchAggregatorSingleton},
		{"percheck_api_infrastructure_imports", scan.ScanAPIInfrastructureImports},
		{"percheck_handler_generate_fields", scan.ScanHandlerGenerateFields},
	}
}
