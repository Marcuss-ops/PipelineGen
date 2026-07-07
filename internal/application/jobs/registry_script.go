package jobs

import "time"

// registerScriptEntries registers all script-generation job types
// into the canonical registry. Called by Compose() after the base
// registry is created.
//
// LONG-FILES-SPLIT-2026-07-06 Band A #7: extracted from registry.go's
// Compose() function per AGENTS.md Pattern 5.
func registerScriptEntries(r *Registry) {
	r.Register(JobPolicy{Type: TypeScriptGenerate, Description: "Script generation", Timeout: 60 * time.Minute, DefaultMaxRetries: 2, ProducesArtifacts: true})
	r.Register(JobPolicy{Type: TypeMediaCurate, Description: "Media curation", Timeout: 30 * time.Minute, DefaultMaxRetries: 1})

	// Step 11B sibling types (script.generate -> voiceover / image fan-out).
	// Concurrency=4 per user spec bounds per-worker sibling fan-out. Both
	// sibling classes produce canonical asset rows via JobFinalizer.
	// CompleteWithArtifacts (PR-VO-A3) so ProducesArtifacts=true.
	// PR-COMPLETE-WORKER-BROAD-FIX Path D (July 2026): ProducesArtifacts REMOVED.
	// TypeScriptVoiceoverSibling and TypeScriptImageSibling are orphaned
	// registry entries — no production handler is statically registered
	// (the sibling fan-out is orchestrated by the parent handler directly,
	// not via a separate job dispatch).
	r.Register(JobPolicy{Type: TypeScriptVoiceoverSibling, Description: "Voiceover sibling spawned by script.generate (Step 11B: ParentJobID = script.generate.id, Concurrency=4, AssetRequirements.Required drives parent fail-closed)", Timeout: 30 * time.Minute, DefaultMaxRetries: 2, Concurrency: 4})
	r.Register(JobPolicy{Type: TypeScriptImageSibling, Description: "Image sibling spawned by script.generate (Step 11B: ParentJobID = script.generate.id, Concurrency=4, AssetRequirements.Required drives parent fail-closed)", Timeout: 15 * time.Minute, DefaultMaxRetries: 2, Concurrency: 4})

	// ── P0 #4 script.generate_item child-job ──
	// Per-item retry via broker-emitted child jobs. The parent aggregator
	// reads child outcomes and finalizes the parent.
	r.Register(JobPolicy{Type: TypeScriptGenerateItem, Description: "Script generate per-item child", Timeout: 30 * time.Minute, DefaultMaxRetries: 2, Concurrency: 4})
}
