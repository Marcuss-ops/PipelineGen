package main

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/scan/boundaries"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/scan/governance"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/scan/migrations"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/scan/structure"
)

// CheckSpec describes one rule-family scanner ready to execute.
type CheckSpec struct {
	Name string
	Run  func(root string, pol *policy.Policy, r *report.Report)
}

// ruleFunc is the production-only-aware scanner signature shared by the
// data-driven registry below. Scanners that do not care about production-only
// mode are adapted with simple(); the rest are stored directly.
type ruleFunc func(root string, pol *policy.Policy, r *report.Report, productionOnly bool)

// simple adapts a production-only-agnostic scanner to ruleFunc.
func simple(fn func(root string, pol *policy.Policy, r *report.Report)) ruleFunc {
	return func(root string, pol *policy.Policy, r *report.Report, _ bool) { fn(root, pol, r) }
}

// rule is one data-driven registry entry: the rule-family id surfaced in the
// report plus its scanner.
type rule struct {
	name string
	run  ruleFunc
}

// fileSizeRule keeps the two file/package-size scanners sharing one file-line
// map (the mode pass populates it, the command-binaries pass consumes it).
func fileSizeRule(root string, pol *policy.Policy, r *report.Report, productionOnly bool) {
	fileLines := map[string]int{}
	structure.ScanPackagesForMode(root, pol, r, fileLines, productionOnly)
	structure.ScanCommandBinaries(root, pol, r, fileLines)
}

// clipIngestRule adapts the violations-only scanner to the report shape.
func clipIngestRule(root string, _ *policy.Policy, r *report.Report, _ bool) {
	r.Violations = append(r.Violations, boundaries.ScanClipIngestPipelineCanonical1(root)...)
}

// defaultRules is the canonical scanner registry, in execution order.
//
// It is the single source of truth for the rule-family sequence; DefaultChecks
// projects it to the production-only-unaware CheckSpec surface consumed by the
// runner. Each id here is also referenced by the scanner that emits it, which
// is what TestHardGatesAreEmittable checks against architecture/policy.yaml.
var defaultRules = []rule{
	{"constructors", simple(structure.ScanConstructors)},
	{"struct_deps", simple(structure.ScanStructDeps)},
	{"forbidden_dirs", simple(structure.ScanForbiddenDirs)},
	{"kernel_subzone_hints", simple(structure.ScanKernelSubzoneHints)},
	{"kernel_subzone_integrity", simple(structure.ScanKernelSubzoneIntegrity)},
	{"percheck_kernel_boundary", simple(boundaries.ScanKernelBoundary)},
	{"unknown_internal_roots", simple(structure.ScanUnknownInternalRoots)},
	{"percheck_legacy_root_new_code", simple(migrations.ScanLegacyRootNewCode)},
	{"ownership_doc", simple(structure.ScanOwnershipDoc)},
	{"legacy_policy_doc", simple(structure.ScanLegacyPolicyDoc)},
	{"ci_gates_doc", simple(structure.ScanCIGatesDoc)},
	{"agent_playbook_doc", simple(structure.ScanAgentPlaybookDoc)},
	{"removal_doc", simple(structure.ScanRemovalDoc)},
	{"stale_prose_paths", simple(structure.ScanStaleProsePaths)},
	{"percheck_canon_index_drift", simple(structure.ScanCanonIndexDrift)},
	{"percheck_type_redecl", simple(governance.ScanTypeRedeclarations)},
	{"percheck_txcontext_ban", simple(governance.ScanTxContextBan)},
	{"percheck_monitor_infra_import", simple(governance.ScanMonitorInfraImport)},
	{"percheck_player_client_centralization", simple(boundaries.ScanPlayerClientCentralization)},
	{"percheck_dual_mode_sync", simple(governance.ScanDualModeSync)},
	{"percheck_video_encoder_policy", simple(governance.ScanVideoEncoderPolicy)},
	{"percheck_root_override_ban", governance.ScanRootOverrideBan},
	{"percheck_spec_aliases", simple(governance.ScanSpecAliasesTerritory)},
	{"percheck_voiceover_alias_ban", boundaries.ScanVoiceoverAliasBan},
	{"percheck_api_module_deps_max_8", simple(governance.ScanApiModuleDepsMax8)},
	{"percheck_assetbinder_ssot", simple(structure.ScanAssetBinderSSOT)},
	{"percheck_drive_access_ssot", simple(boundaries.ScanDriveAccessSSOT)},
	{"percheck_metadata_key_registry", simple(governance.ScanMetadataKeys)},
	{"percheck_input_immutability", simple(structure.ScanInputImmutability)},
	{"percheck_sourcestager_transformer", simple(boundaries.ScanSourceStagerTransformer)},
	{"file_size_pkg_size_thin_command", fileSizeRule},
	{"file_size_strict", simple(structure.ScanFileLinesStrict)},
	{"percheck_media_identity_no_location_fields", simple(governance.ScanMediaIdentityNoLocationFields)},
	{"percheck_media_assets_writer_canonical", simple(boundaries.ScanMediaAssetsWriterCanonical)},
	{"percheck_media_txn_boundary", simple(boundaries.ScanMediaTxBoundary)},
	{"percheck_sqlite_media_reader_ban", simple(boundaries.ScanSQLiteMediaReaderBan)},
	{"percheck_pg_dual_write_contract", simple(boundaries.ScanPGDualWriteContract)},
	{"percheck_media_write_bridge_ban", simple(boundaries.ScanMediaWriteBridgeBan)},
	{"percheck_asset_state_no_shadow_enum", simple(governance.ScanAssetStateNoShadowEnum)},
	{"percheck_157_asset_state_migration_default_wire", simple(migrations.ScanAssetStateMigration157DefaultWire)},
	{"percheck_rights_status_canonical_6", simple(governance.ScanRightsStatusCanonical6)},
	{"percheck_review_status_canonical_4", simple(governance.ScanReviewStatusCanonical4)},
	{"percheck_clip_ingest_pipeline_canonical_1", clipIngestRule},
	{"percheck_binder_scene_field_writes", simple(structure.ScanBinderSceneFieldWrites)},
	{"percheck_qdrant_index_import_ban", simple(boundaries.ScanQdrantIndexImportBan)},
	{"percheck_pipeline_map_carrier_ban", boundaries.ScanPipelineMapCarrierBan},
	{"percheck_no_pipeline_mapstr", structure.ScanNoPipelineMapStr},
	{"percheck_indexed_state_writer_ssot", simple(governance.ScanIndexedStateWriterSSOT)},
	{"percheck_slot_strings_ban", simple(governance.ScanSlotStringsBan)},
	{"percheck_searchmode_forced_ban", simple(boundaries.ScanSearchModeForcedBan)},
	{"percheck_digest_sha256_ban", simple(governance.ScanDigestSHA256Ban)},
	{"percheck_digest_md5_ban", simple(governance.ScanDigestMD5Ban)},
	{"percheck_version_strings_ban", simple(governance.ScanVersionStringsBan)},
	{"percheck_stopword_maps_in_app", simple(governance.ScanStopwordMapsInApp)},
	{"percheck_provider_policy_single_owner", simple(governance.ScanProviderPolicySingleOwner)},
	{"percheck_index_pending_writer_ban", simple(governance.ScanIndexPendingWriterBan)},
	{"percheck_mediatransformer_no_infra_fields", simple(boundaries.ScanMediaTransformerNoInfraFields)},
	{"percheck_no_generic_generation_facade", governance.ScanNoGenericGenerationFacade},
	{"percheck_assetbinder_no_scenesynthesizer", structure.ScanAssetBinderNoSynthesizer},
	{"percheck_asset_committer_event_ssot", governance.ScanAssetCommitterEventSSOT},
	{"percheck_control_plane_sql_writes", boundaries.ScanControlPlaneSQLWrites},
	{"percheck_upsert_points_sole_owner", governance.ScanUpsertPointsSoleOwner},
	{"percheck_embedding_constants_ssot", governance.ScanEmbeddingConstantsSSOT},
	{"percheck_frame_concept_projection_writer", governance.ScanFrameConceptProjectionWriter},
	{"percheck_search_aggregator_singleton", simple(boundaries.ScanSearchAggregatorSingleton)},
	{"percheck_api_infrastructure_imports", simple(boundaries.ScanAPIInfrastructureImports)},
	{"percheck_canonical_application_infrastructure_imports", simple(boundaries.ScanCanonicalApplicationInfrastructureImports)},
	{"percheck_legacy_root_ban", simple(governance.ScanLegacyRootImportBan)},
	{"percheck_sqlite_assets_clips_duplicate", simple(boundaries.ScanSQLiteAssetsClipsDuplicateBan)},
	{"percheck_job_ownership", simple(structure.ScanJobOwnership)},
	{"percheck_legacy_hotspot_growth", simple(structure.ScanLegacyHotspotGrowth)},
	{"percheck_handler_generate_fields", simple(structure.ScanHandlerGenerateFields)},
	{"percheck_brain_infra_ban", simple(boundaries.ScanBrainInfraBan)},
	{"percheck_brain_single_impl", simple(governance.ScanBrainSingleImpl)},
	{"percheck_duration_probe_ssot", simple(governance.ScanDurationProbeSSOT)},
	{"percheck_observability_operation_ssot", simple(governance.ScanObservabilityOperationSSOT)},
	{"percheck_speech_timing_ssot", simple(governance.ScanSpeechTimingSSOT)},
	{"percheck_project_derivation_ssot", simple(governance.ScanProjectDerivationSSOT)},
	{"percheck_evidence_precedence_ssot", simple(governance.ScanEvidencePrecedenceSSOT)},
	{"percheck_identity_ssot", simple(governance.ScanIdentitySSOT)},
	{"percheck_governance_artifacts", simple(governance.ScanGovernanceArtifacts)},
}

// DefaultChecks returns the canonical scanner sequence for the requested mode.
func DefaultChecks(productionOnly bool) []CheckSpec {
	specs := make([]CheckSpec, 0, len(defaultRules))
	for _, ru := range defaultRules {
		ru := ru
		specs = append(specs, CheckSpec{
			Name: ru.name,
			Run: func(root string, pol *policy.Policy, r *report.Report) {
				ru.run(root, pol, r, productionOnly)
			},
		})
	}
	return specs
}
