package policy

// registry_texttracks.go: registers the texttracks job types
// into the canonical registry. Called by Compose() after the
// base registry is created.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 3 (July 2026).

import "time"

func registerTextTrackEntries(r *Registry) {
	// TypeAssetTextMaterialize is the canonical job type
	// for the text-track materialization pipeline. The
	// handler is registered via
	// internal/application/assets/texttracks/jobs.go.
	//
	//   - Timeout: 2h covers a 6-language fan-out × per-language
	//     translation round-trip (3-15s on ollama) + outbox emission.
	//   - DefaultMaxRetries: 1 (transient ollama 5xx → retry once
	//     before dead-letter; the materializer is idempotent at
	//     the per-language level so a retry is safe).
	r.Register(JobPolicy{
		Completion: CompletionDeclaration{
			JobType:              TypeAssetTextMaterialize,
			ArtifactOwnership:    ArtifactOwnershipApplication,
			FinalizationStrategy: FinalizationStrategyLegacyComplete,
		},
		Description:       "Asset text track materialization (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 3: fan-out translation into all configured MaterializeLanguages; idempotent via per-language skip + outbox event_key dedup)",
		Timeout:           2 * time.Hour,
		DefaultMaxRetries: 1,
	})
}
