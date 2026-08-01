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
	r.Register(JobPolicy{Type: TypeMediaStock, Description: "Stock media pipeline (per-run artifacts persisted via the canonical JobFinalizer.CompleteWithArtifacts SPINE inside Service.runOrchestratorResilient → Orchestrator.RunResilient step 6 stock.finalize; the spine call is the terminal-flip + artifact-write seam, NOT the legacy SQLiteStore.Complete)", Timeout: 60 * time.Minute, DefaultMaxRetries: 1, Concurrency: 4, ProducesArtifacts: true})
	// PR-COMPLETE-WORKER-BROAD-FIX Path D (July 2026): ProducesArtifacts REMOVED.
	// TypeMediaGenerate is an orphaned registry entry — no production handler
	// is statically registered.
	r.Register(JobPolicy{Type: TypeMediaGenerate, Description: "Generate missing media asset", Timeout: 30 * time.Minute, DefaultMaxRetries: 2})
	r.Register(JobPolicy{Type: TypeMediaReindex, Description: "Reindex media assets", Timeout: 2 * time.Minute, DefaultMaxRetries: 1})
	r.Register(JobPolicy{Type: TypeMediaEnrich, Description: "Single-asset semantic enrichment + Qdrant-style indexing", Timeout: 3 * time.Minute, DefaultMaxRetries: 2})
	// PR-COMPLETE-WORKER-BROAD-FIX Path D (July 2026): ProducesArtifacts REMOVED.
	// TypeBulkUploadYouTubeClips is an orphaned registry entry — no production
	// handler is statically registered.
	r.Register(JobPolicy{Type: TypeBulkUploadYouTubeClips, Description: "Bulk upload YouTube clips", Timeout: 120 * time.Minute, DefaultMaxRetries: 1})

	// PR-011A (July 2026): post-publish RLM/LLM enrichment pass.
	//
	// Per chunk: handler reads media_assets row, calls
	// EnrichmentLLMClient.Enrich (currently a stub in PR-011A — PR-011B
	// wires the real ollama call + UPDATE), then re-emits the
	// asset.published v1 outbox event (PR-011C) so the IndexingHandler
	// re-upserts the chunk to Qdrant with the enriched fields.
	//
	// Timeout: 2 minutes per chunk (LLM call + UPDATE + outbox emit).
	// The 2-minute bound matches the ollama default timeout (cfg.External.OllamaTimeoutSeconds
	// = 600s budget split across N parallel chunks per worker).
	// DefaultMaxRetries: 3 — the canonical retry budget for transient
	// LLM unavailability (ollama overload, network blip). The typed
	// sentinel ErrEnrichmentLLMUnavailable is the canonical retry
	// signal; ErrEnrichmentInvalidLLMResponse is the canonical
	// re-think-after-3-failures signal (terminal, not retried).
	// ProducesArtifacts=false (the enrichment pass persists
	// media_assets.metadata_json inside the per-chunk tx; no separate
	// finalizer needed).
	r.Register(JobPolicy{Type: TypeMediaStockRLMEnrich, Description: "PR-011 post-publish RLM/LLM enrichment pass (per-chunk: read media_assets -> ollama.Enrich -> UPDATE media_assets.metadata_json -> emit asset.published v1 outbox -> IndexingHandler re-upsert). Wired ONLY when cfg.External.StockEnrichmentEnabled=true; default = false (godlike/07 fail-closed composition). ProducesArtifacts=false (enrichment tx owns its own media_assets write; broker legacy Complete is the canonical mark-SUCCEEDED seam).", Timeout: 2 * time.Minute, DefaultMaxRetries: 3})
}
