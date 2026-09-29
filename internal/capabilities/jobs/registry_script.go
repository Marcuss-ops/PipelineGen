package jobs

import "time"

// registerScriptEntries registers all script-generation job types
// into the canonical registry. Called by Compose() after the base
// registry is created.
//
// LONG-FILES-SPLIT-2026-07-06 Band A #7: extracted from registry.go's
// Compose() function per AGENTS.md Pattern 5.
func registerScriptEntries(r *Registry) {
	// TypeScriptGenerate is the ONE job type that is both a worker-spine
	// artifact producer and a parent of children. It publishes its own spine
	// artifacts (the durable CORE_READY snapshot) and then defers the terminal
	// flip to the script parent aggregator, which owns it until the deferred
	// `script.docs_publish` child has published every requested document.
	// ArtifactOwnership is unchanged, so ProducesArtifacts (ownership-derived)
	// and the spine publication path are unaffected — only the terminal
	// ownership moves (TICKET-CORE-READY-DURABLE-DAG steps 2-4).
	r.Register(JobPolicy{Completion: CompletionDeclaration{JobType: TypeScriptGenerate, ArtifactOwnership: ArtifactOwnershipWorkerSpine, FinalizationStrategy: FinalizationStrategyCompleteWithArtifactsThenDefer}, Description: "Script generation", Timeout: 60 * time.Minute, DefaultMaxRetries: 2})
	r.Register(JobPolicy{Completion: CompletionDeclaration{JobType: TypeMediaCurate, ArtifactOwnership: ArtifactOwnershipNone, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "Media curation", Timeout: 30 * time.Minute, DefaultMaxRetries: 1})

	// Step 11B sibling types (script.generate -> voiceover / image fan-out).
	// Concurrency=4 per user spec bounds per-worker sibling fan-out. Both
	// sibling classes produce canonical asset rows via JobFinalizer.
	// CompleteWithArtifacts (PR-VO-A3) so ProducesArtifacts=true.
	// PR-COMPLETE-WORKER-BROAD-FIX Path D (July 2026): ProducesArtifacts REMOVED.
	// TypeScriptVoiceoverSibling and TypeScriptImageSibling are orphaned
	// registry entries — no production handler is statically registered
	// (the sibling fan-out is orchestrated by the parent handler directly,
	// not via a separate job dispatch).
	r.Register(JobPolicy{Completion: CompletionDeclaration{JobType: TypeScriptVoiceoverSibling, ArtifactOwnership: ArtifactOwnershipNone, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "Voiceover sibling spawned by script.generate (Step 11B: ParentJobID = script.generate.id, Concurrency=4, AssetRequirements.Required drives parent fail-closed)", Timeout: 30 * time.Minute, DefaultMaxRetries: 2, Concurrency: 4})
	r.Register(JobPolicy{Completion: CompletionDeclaration{JobType: TypeScriptImageSibling, ArtifactOwnership: ArtifactOwnershipNone, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "Image sibling spawned by script.generate (Step 11B: ParentJobID = script.generate.id, Concurrency=4, AssetRequirements.Required drives parent fail-closed)", Timeout: 15 * time.Minute, DefaultMaxRetries: 2, Concurrency: 4})

	// ── P0 #4 script.generate_item child-job ──
	// Per-item retry via broker-emitted child jobs. The parent aggregator
	// reads child outcomes and finalizes the parent.
	r.Register(JobPolicy{Completion: CompletionDeclaration{JobType: TypeScriptGenerateItem, ArtifactOwnership: ArtifactOwnershipNone, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "Script generate per-item child", Timeout: 30 * time.Minute, DefaultMaxRetries: 2, Concurrency: 4})

	// ── Durable-DAG deferred Docs leg (TICKET-CORE-READY-DURABLE-DAG step 2) ──
	// One `script.docs_publish` child per script.generate run that has documents
	// enabled. It is the deferred post-core leg: the parent publishes its spine
	// and hands the terminal flip to the aggregator, which owns it until this
	// child has published every requested document.
	//
	// ArtifactOwnershipApplication (not WorkerSpine): the child persists its own
	// document references to the durable run, exactly like the voiceover child
	// that also uploads to Drive — it does NOT stage manifest artifacts for the
	// worker spine, so ProducesArtifacts stays false and the broker's legacy
	// Complete remains the canonical mark-SUCCEEDED seam.
	//
	// DefaultMaxRetries=2: the leg is a bounded number of Drive/Docs round
	// trips, and a transient Drive failure must not fail a run whose core is
	// already durable. It fails typed rather than silently succeeding.
	r.Register(JobPolicy{Completion: CompletionDeclaration{JobType: TypeScriptDocsPublish, ArtifactOwnership: ArtifactOwnershipApplication, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "Script document publication (deferred CORE_READY post-core leg: run_id + durable snapshot reference, never a copy of the result)", Timeout: 30 * time.Minute, DefaultMaxRetries: 2, Concurrency: 4})

	// ── Spina Dorsale Fase 2 (July 2026): downstream artifact jobs ──
	// These are spawned by the script.generate pipeline cutover
	// (SCRIPT-DOWNSTREAM-CUTOVER-2026-07-09) as async sibling jobs
	// instead of running inline in the postprocessor chain.

	// TypeImagesGenerate: AI image generation via Chrome/Playwright →
	// slides.new → Nano Banana Pro. DefaultMaxRetries=2 covers transient
	// failures (Chrome crash, network blip, LLM overload) before the
	// broker routes the job to the dead-letter path. Mirrors the
	// TypeScriptGenerate envelope (same timeout + retry budget) since
	// image generation is the costliest downstream operation.
	r.Register(JobPolicy{Completion: CompletionDeclaration{JobType: TypeImagesGenerate, ArtifactOwnership: ArtifactOwnershipNone, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "AI image generation for script scenes (Chrome + Playwright — Spina Dorsale Fase 2 downstream cutover)", Timeout: 60 * time.Minute, DefaultMaxRetries: 2})

	// TypeAssetsResolve: semantic asset resolution via Qdrant hybrid
	// search. DefaultMaxRetries=1 because resolution is lightweight and
	// deterministic (a single Qdrant query per scene); 1 retry covers
	// transient Qdrant connection blips without wasting broker resources
	// on repeated lookups that would yield the same result set.
	r.Register(JobPolicy{Completion: CompletionDeclaration{JobType: TypeAssetsResolve, ArtifactOwnership: ArtifactOwnershipNone, FinalizationStrategy: FinalizationStrategyLegacyComplete}, Description: "Semantic asset resolution via Qdrant (script scene → clip/stock matching — Spina Dorsale Fase 2 downstream cutover)", Timeout: 10 * time.Minute, DefaultMaxRetries: 1})

}
