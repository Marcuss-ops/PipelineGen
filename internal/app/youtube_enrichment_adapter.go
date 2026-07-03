// Package app — YouTube enrichment adapter extracted from
// assets_register_adapters.go (PR-GODOBJ-8, July 2026).
package app

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
)

// youtubeEnrichmentAdapter implements youtube.EnrichmentPort by composing
// the legacy EnrichmentAdapter (used for the indexed-detection boolean),
// ConfigAdapter (folder defaults), SearchAdapter (related clips), and an
// optional JobsPort for the post-register media.enrich dispatch.
//
// Per thinker audit:
//   - IndexingEnabled() returns true iff enrichment AND (jobs via this
//     adapter has an internal nil-aware path — equivalent to historical
//     `indexed := s.enrichment != nil && s.jobs != nil`).
//   - DispatchPostRegister no-ops when the internal jobs port is nil
//     (preserves historical test-fixture path which logged at Debug
//     level rather than failing closed).
type youtubeEnrichmentAdapter struct {
	jobs       sourcing.JobsPort // nil today; optional wiring in future composition sites
	enrichment sourcing.EnrichmentPort
	search     sourcing.SearchProviderPort
	config     sourcing.ConfigPort
}

func (a *youtubeEnrichmentAdapter) IndexingEnabled() bool {
	// Mirrors historical `indexed := s.enrichment != nil && s.jobs != nil`.
	// When jobs port is nil (current production composition site), this
	// returns false and the YouTubeRegistrar falls back to "not_configured"
	// indexing_status — matching historical behaviour for the same path.
	return a.enrichment != nil && a.jobs != nil
}

func (a *youtubeEnrichmentAdapter) DispatchPostRegister(ctx context.Context, clipID, source, localPath string) error {
	if a.jobs == nil {
		return nil // matches historical fallback: log.Debug("jobs port not wired...")
	}
	_, err := a.jobs.Enqueue(ctx, sourcing.EnqueueRequest{
		Type:       "media.enrich",
		MaxRetries: 1,
		Payload: sourcing.JobPayload{
			"asset_id":   clipID,
			"source":     source,
			"local_path": localPath,
		},
	})
	return err
}

func (a *youtubeEnrichmentAdapter) SearchRelated(ctx context.Context, query string, limit int) ([]sourcing.SearchCandidate, error) {
	if a.search == nil {
		return nil, nil
	}
	return a.search.Search(ctx, query, limit)
}

func (a *youtubeEnrichmentAdapter) FolderDefaults() (clipsFolder, rootFolder string) {
	if a.config == nil {
		return "", ""
	}
	return a.config.ClipsFolder(), a.config.RootFolder()
}
