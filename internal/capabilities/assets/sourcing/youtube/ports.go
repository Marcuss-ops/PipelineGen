// Package youtube — narrow port interfaces for the YouTubeRegistrar use case.
//
// Per AGENTS.md Pattern 0 (port abstraction, June 2026): the YouTubeRegistrar
// has its own typed view of asset persistence + post-persist orchestration.
// Six granular legacy ports (AssetTree/IndexDispatch/Jobs/Search/Config/Enrichment)
// are merged into two narrow v2 ports here so the ctor stays at 8 fields.
//
// Composition root (internal/app/assets_register_sourcing.go) builds adapters
// that wrap the legacy granular ports into these v2 surfaces — without
// that adapter layer this package would still hit 13 deps on the YouTube
// sub-service. P0-1 / commit 1 of the architectural plan.
package youtube

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/localized"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/sourcing"
)

// AtomicClipWriterPort is the OPTIONAL atomic terminal-write surface for the
// Register pipeline. When the composition root wires it, Register performs
// media_assets + asset_text_tracks + cue segments + the
// asset.index.requested outbox event in ONE PostgreSQL transaction instead of
// the legacy split (saveClipToDB → index event, then a separate
// textTrackRepo.UpsertBatch).
//
// Why this port exists (2026-09-17, identity/atomicity unification):
// the split path could commit the asset row and emit the index event BEFORE
// the transcript existed, so an index worker could embed a clip with no
// transcript and never revisit it. The atomic writer closes that window: the
// index event and the transcript become visible in the same commit, or not at
// all.
//
// godlike/06 SSOT (one canonical owner per fact): this is a type ALIAS of
// localized.LocalizedClipWriter — the same interface the canonical extraction
// path (youtube/usecase.process_segment) consumes. A second, structurally
// identical interface would be free to drift from the canonical one.
type AtomicClipWriterPort = localized.LocalizedClipWriter

// IndexDispatcherPort merges the historical IndexDispatcher + AssetTree surface.
//
// Pre-P0-1: sourcing.IndexDispatcherPort.EnqueueAndIndex + sourcing.AssetTreePort.UpsertNode
// were called sequentially by sourcing.Service.RegisterFromYouTube. The two
// writes were not atomic across the in-flight ingest — if the asset-tree upsert
// failed (warn-only) the clip was still saved with a stale tree.
//
// P0-1 / commit 1 collapse the two into a single EnqueueAndIndex on the
// IndexDispatcherPort v2: the adapter in composition root performs both
// writes (outbox dispatcher + assettree upsert) and surfaces any failure
// to the caller. Behaviour-equivalent on success; loser in the test fixture
// path where the dispatcher is nil (returns the same QDRANT-asset-mutation
// isolation error as before).
type IndexDispatcherPort interface {
	EnqueueAndIndex(ctx context.Context, clip *sourcing.ExistingClip, contentHash string) error
}

// EnrichmentPort merges Jobs + Search + Config + legacy Enrichment surfaces
// behind a single narrow v2 interface.
//
// IndexingEnabled is the canonical boolean the YouTubeRegistrar needs to
// populate result.IndexingStatus — historically `s.enrichment != nil`.
//
// DispatchPostRegister is the jobs.Enqueue media.enrich wrapper; nil port
// causes the call site to log a debug line and skip the dispatch (preserved
// behaviour from the historical goroutine-detach path that Wave 22
// forbade via context.WithoutCancel).
//
// SearchRelated is the SearchProviderPort.Search wrapper; nil port returns
// nil/empty so the result.RelatedClips map stays empty (best-effort).
//
// FolderDefaults returns (clips_folder, root_folder) from ConfigPort. The
// YouTubeRegistrar's step-6 Drive target folder resolution uses these
// only when cmd.FolderID is empty. Nil port returns ("", "").
type EnrichmentPort interface {
	IndexingEnabled() bool
	DispatchPostRegister(ctx context.Context, clipID, source, localPath string) error
	SearchRelated(ctx context.Context, query string, limit int) ([]sourcing.SearchCandidate, error)
	FolderDefaults() (clipsFolder, rootFolder string)
}
