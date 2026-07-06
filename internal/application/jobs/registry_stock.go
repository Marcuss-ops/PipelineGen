package jobs

import "time"

// registerStockEntries registers all stock/media-pipeline job types
// into the canonical registry. Called by Compose() after the base
// registry is created.
//
// LONG-FILES-SPLIT-2026-07-06 Band A #7: extracted from registry.go's
// Compose() function per AGENTS.md Pattern 5.
func registerStockEntries(r *Registry) {
	// PR-COMPLETE-WORKER-FIX-TYPE-MEDIA-STOCK (July 2026):
	// ProducesArtifacts=true RETAINED. The entry is closed as
	// "verified-canonical-spine-surface" rather than as a flip.
	// The stock pipeline uses the canonical JobFinalizer.CompleteWithArtifacts
	// SPINE (not a per-item tx like voiceover/YouTube) for the terminal-flip
	// + artifact write: Service.runOrchestratorResilient → Orchestrator.RunResilient
	// step 6 stock.finalize calls BuildFinalizationRequest + ApplyFinalizationSpine
	// → JobFinalizer.CompleteWithArtifacts which does the SINGLE-TX spine write
	// (UpdateJobToSucceededCAS + InsertResultOnConflict + PersistArtifactMap +
	// InsertOutboxEnvelope per Pattern 11 in AGENTS.md). The spine call IS the
	// terminal-flip seam for this job type — NOT the legacy SQLiteStore.Complete.
	r.Register(JobPolicy{Type: TypeMediaStock, Description: "Stock media pipeline (per-run artifacts persisted via the canonical JobFinalizer.CompleteWithArtifacts SPINE inside Service.runOrchestratorResilient → Orchestrator.RunResilient step 6 stock.finalize; the spine call is the terminal-flip + artifact-write seam, NOT the legacy SQLiteStore.Complete)", Timeout: 60 * time.Minute, DefaultMaxRetries: 1, ProducesArtifacts: true})
	r.Register(JobPolicy{Type: TypeMediaGenerate, Description: "Generate missing media asset", Timeout: 30 * time.Minute, DefaultMaxRetries: 2, ProducesArtifacts: true})
	r.Register(JobPolicy{Type: TypeMediaReindex, Description: "Reindex media assets", Timeout: 2 * time.Minute, DefaultMaxRetries: 1})
	r.Register(JobPolicy{Type: TypeMediaEnrich, Description: "Single-asset semantic enrichment + Qdrant-style indexing", Timeout: 3 * time.Minute, DefaultMaxRetries: 2})
	r.Register(JobPolicy{Type: TypeBulUploadYouTubeClips, Description: "Bulk upload YouTube clips", Timeout: 120 * time.Minute, DefaultMaxRetries: 1, ProducesArtifacts: true})
}
