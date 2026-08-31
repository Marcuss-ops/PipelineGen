package main

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/scan/boundaries"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/scan/governance"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/scan/migrations"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/scan/structure"
)

// CheckSpec describes one rule-family scanner.
type CheckSpec struct {
	Name string
	Run  func(root string, pol *policy.Policy, r *report.Report)
}

// DefaultChecks returns the canonical scanner sequence.
func DefaultChecks(productionOnly bool) []CheckSpec {
	return []CheckSpec{
		{"constructors", structure.ScanConstructors},
		{"struct_deps", structure.ScanStructDeps},
		{"forbidden_dirs", structure.ScanForbiddenDirs},
		{"kernel_subzone_hints", structure.ScanKernelSubzoneHints},
		{"kernel_subzone_integrity", structure.ScanKernelSubzoneIntegrity},
		{"percheck_kernel_boundary", boundaries.ScanKernelBoundary},
		{"unknown_internal_roots", structure.ScanUnknownInternalRoots},
		{"percheck_legacy_root_new_code", migrations.ScanLegacyRootNewCode},
		{"ownership_doc", structure.ScanOwnershipDoc},
		{"legacy_policy_doc", structure.ScanLegacyPolicyDoc},
		{"ci_gates_doc", structure.ScanCIGatesDoc},
		{"agent_playbook_doc", structure.ScanAgentPlaybookDoc},
		{"removal_doc", structure.ScanRemovalDoc},
		{"stale_prose_paths", structure.ScanStaleProsePaths},
		{"percheck_canon_index_drift", structure.ScanCanonIndexDrift},
		{"percheck_type_redecl", governance.ScanTypeRedeclarations},
		{"percheck_txcontext_ban", governance.ScanTxContextBan},
		{"percheck_monitor_infra_import", governance.ScanMonitorInfraImport},
		{"percheck_player_client_centralization", boundaries.ScanPlayerClientCentralization},
		{"percheck_dual_mode_sync", governance.ScanDualModeSync},
		{"percheck_video_encoder_policy", governance.ScanVideoEncoderPolicy},
		{"percheck_root_override_ban", func(root string, pol *policy.Policy, r *report.Report) {
			governance.ScanRootOverrideBan(root, pol, r, productionOnly)
		}},
		{"percheck_spec_aliases", governance.ScanSpecAliasesTerritory},
		{"percheck_voiceover_alias_ban", func(root string, pol *policy.Policy, r *report.Report) {
			boundaries.ScanVoiceoverAliasBan(root, pol, r, productionOnly)
		}},
		{"percheck_api_module_deps_max_8", governance.ScanApiModuleDepsMax8},
		{"percheck_assetbinder_ssot", structure.ScanAssetBinderSSOT},
		{"percheck_drive_access_ssot", boundaries.ScanDriveAccessSSOT},
		{"percheck_metadata_registry", governance.ScanMetadataRegistry},
		{"percheck_metadata_key_registry", governance.ScanMetadataKeys},
		{"percheck_input_immutability", structure.ScanInputImmutability},
		{"percheck_sourcestager_transformer", boundaries.ScanSourceStagerTransformer},
		{"file_size_pkg_size_thin_command", func(root string, pol *policy.Policy, r *report.Report) {
			fileLines := map[string]int{}
			structure.ScanPackagesForMode(root, pol, r, fileLines, productionOnly)
			structure.ScanCommandBinaries(root, pol, r, fileLines)
		}},
		{"file_size_strict", structure.ScanFileLinesStrict},
		{"percheck_finalizer_no_direct_sql", boundaries.ScanFinalizerNoDirectSQL},
		{"percheck_media_assets_writer_canonical", boundaries.ScanMediaAssetsWriterCanonical},
		{"percheck_asset_state_no_shadow_enum", governance.ScanAssetStateNoShadowEnum},
		{"percheck_157_asset_state_migration_default_wire", migrations.ScanAssetStateMigration157DefaultWire},
		{"percheck_rights_status_canonical_6", governance.ScanRightsStatusCanonical6},
		{"percheck_review_status_canonical_4", governance.ScanReviewStatusCanonical4},
		{"percheck_clip_ingest_pipeline_canonical_1", func(root string, _ *policy.Policy, r *report.Report) {
			r.Violations = append(r.Violations, boundaries.ScanClipIngestPipelineCanonical1(root)...)
		}},
		{"percheck_binder_scene_field_writes", structure.ScanBinderSceneFieldWrites},
		{"percheck_qdrant_index_import_ban", boundaries.ScanQdrantIndexImportBan},
		{"percheck_pipeline_map_carrier_ban", func(root string, pol *policy.Policy, r *report.Report) {
			boundaries.ScanPipelineMapCarrierBan(root, pol, r, productionOnly)
		}},
		{"percheck_no_pipeline_mapstr", func(root string, pol *policy.Policy, r *report.Report) {
			structure.ScanNoPipelineMapStr(root, pol, r, productionOnly)
		}},
		{"percheck_indexed_state_writer_ssot", governance.ScanIndexedStateWriterSSOT},
		{"percheck_slot_strings_ban", governance.ScanSlotStringsBan},
		{"percheck_searchmode_forced_ban", boundaries.ScanSearchModeForcedBan},
		{"percheck_digest_sha256_ban", governance.ScanDigestSHA256Ban},
		{"percheck_digest_md5_ban", governance.ScanDigestMD5Ban},
		{"percheck_version_strings_ban", governance.ScanVersionStringsBan},
		{"percheck_stopword_maps_in_app", governance.ScanStopwordMapsInApp},
		{"percheck_index_pending_writer_ban", governance.ScanIndexPendingWriterBan},
		{"percheck_mediatransformer_no_infra_fields", boundaries.ScanMediaTransformerNoInfraFields},
		{"percheck_no_generic_generation_facade", func(root string, pol *policy.Policy, r *report.Report) {
			governance.ScanNoGenericGenerationFacade(root, pol, r, productionOnly)
		}},
		{"percheck_assetbinder_no_scenesynthesizer", func(root string, pol *policy.Policy, r *report.Report) {
			structure.ScanAssetBinderNoSynthesizer(root, pol, r, productionOnly)
		}},
		{"percheck_asset_committer_event_ssot", func(root string, pol *policy.Policy, r *report.Report) {
			governance.ScanAssetCommitterEventSSOT(root, pol, r, productionOnly)
		}},
		{"percheck_control_plane_sql_writes", func(root string, pol *policy.Policy, r *report.Report) {
			boundaries.ScanControlPlaneSQLWrites(root, pol, r, productionOnly)
		}},
		{"percheck_upsert_points_sole_owner", func(root string, pol *policy.Policy, r *report.Report) {
			governance.ScanUpsertPointsSoleOwner(root, pol, r, productionOnly)
		}},
		{"percheck_embedding_constants_ssot", func(root string, pol *policy.Policy, r *report.Report) {
			governance.ScanEmbeddingConstantsSSOT(root, pol, r, productionOnly)
		}},
		{"percheck_frame_concept_projection_writer", func(root string, pol *policy.Policy, r *report.Report) {
			governance.ScanFrameConceptProjectionWriter(root, pol, r, productionOnly)
		}},
		{"percheck_search_aggregator_singleton", boundaries.ScanSearchAggregatorSingleton},
		{"percheck_api_infrastructure_imports", boundaries.ScanAPIInfrastructureImports},
		{"percheck_canonical_application_infrastructure_imports", boundaries.ScanCanonicalApplicationInfrastructureImports},
		{"percheck_legacy_root_ban", governance.ScanLegacyRootImportBan},
		{"percheck_sqlite_assets_clips_duplicate", boundaries.ScanSQLiteAssetsClipsDuplicateBan},
		{"percheck_job_ownership", structure.ScanJobOwnership},
		{"percheck_legacy_hotspot_growth", structure.ScanLegacyHotspotGrowth},
		{"percheck_handler_generate_fields", structure.ScanHandlerGenerateFields},
		{"percheck_brain_infra_ban", boundaries.ScanBrainInfraBan},
		{"percheck_brain_single_impl", governance.ScanBrainSingleImpl},
		{"percheck_duration_probe_ssot", governance.ScanDurationProbeSSOT},
		{"percheck_observability_operation_ssot", governance.ScanObservabilityOperationSSOT},
		{"percheck_speech_timing_ssot", governance.ScanSpeechTimingSSOT},
		{"percheck_project_derivation_ssot", governance.ScanProjectDerivationSSOT},
		{"percheck_evidence_precedence_ssot", governance.ScanEvidencePrecedenceSSOT},
	}
}
