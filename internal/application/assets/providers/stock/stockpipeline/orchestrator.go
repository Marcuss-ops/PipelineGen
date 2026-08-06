// Package stockpipeline — orchestrator.go (Stock Cutover, July 2026).
//
// Split-topology landing page. The canonical surfaces live in:
//
//   - orchestrator_types.go: OrchestratorConfig, Orchestrator struct,
//     DefaultMaxConcurrentJobs, DefaultOrchestratorJobId,
//     StockArtifactId*, ErrOrchestratorNilDeps
//   - orchestrator_manifest.go: buildStockManifest (C12 5-artifact envelope)
//   - orchestrator_constructor.go: NewTestStockOrchestrator, NewOrchestratorWithResilience,
//     WithAssetPreparation, WithJobFinalizer, stepInputFingerprint, firstSource
//   - orchestrator_run.go: Run, RunResilient, executorLogOrNop
//   - orchestrator_defaults.go: DefaultStockSteps, compile-time Step assertions
//   - orchestrator_fingerprint.go: ChunkArtifactID, ChunkArtifactFilename,
//     MetadataArtifactID
//   - orchestrator_metadata.go: StockRunMetadata, ChunkMetadataEntry,
//     buildStockRunMetadata, writeAndHashMetadata, buildChunkedStockManifest
//   - orchestrator_steps.go: Step interface + 6 pipeline step types
//   - orchestrator_step_errors.go: typed step-error sentinels
//
// STATO ATTUALE: Orchestrator is the code-driven pipeline entrypoint
// canonico. Usa ClipPlanner + SourceStager + VideoCutter + StockRenderer
// + ArtifactPreparation + JobFinalizer per produrre chunk reali, upload
// Drive, e single-TX spine write.
//
// PROSSIMO STEP: buildStockManifest emette 5 entry Required:false;
// il chunk-rendering ladder (già wired) flippa Required:true quando
// LocalPath è hydrated. La projection Qdrant è best-effort con
// fallback INDEX_PENDING.
//
// DEPRECATO: Service.Run (legacy path) coesiste per back-compat
// ServiceRunner interface; il traffico produzione va via
// Service.HandleJob → runOrchestratorResilient.
package stockpipeline
