// Package artlist owns orchestration, policy, and ports for the Artlist
// media catalog. Integrations with the outside world (scraper, downloader,
// Drive, clip indexer, semantic tagger) live behind the following port
// interfaces so the application layer can be exercised with fakes and so
// the legacy concretes can move to internal/infrastructure/artlist/*
// without breaking callers.
//
// Rules enforced here:
//
//   - No port method accepts *sql.DB, *drive.Uploader, *clipindexer.Service,
//     os/exec.Cmd, or any other concrete SDK handle.
//   - No port method returns a path composed by the application (infrastructure
//     owns filesystem and process paths).
//   - Errors are application-shaped: ErrEmpty, ErrUnavailable, ErrTimeout,
//     ErrInvalidResponse, ErrEmptyResult let callers branch on intent, not on
//     on the underlying transport.
package artlist

import (
	"context"
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// Sentinel errors that ports must return. Implementations map transport
// errors to these so callers don't leak transport jargon.
//
// ErrTransportFallback is the orchestrator's hint to try the next
// Searcher / Downloader in the fallback chain (network down, server 5xx,
// subprocess unreachable). Distinct from ErrInvalidResponse, which means
// the transport replied with a semantically-broken payload and the
// orchestrator should NOT silently switch sources.
var (
	ErrEmpty             = errors.New("artlist: empty input")
	ErrUnavailable       = errors.New("artlist: source unavailable")
	ErrTimeout           = errors.New("artlist: source timeout")
	ErrInvalidResponse   = errors.New("artlist: invalid response")
	ErrEmptyResult       = errors.New("artlist: empty result")
	ErrNotFound          = errors.New("artlist: not found")
	ErrTransportFallback = errors.New("artlist: transport failure, fall back to next searcher")
	// ErrAssetMutationDispatcherUnavailable is the typed-typed sentinel
	// NewSearchService returns when the canonical outbox dispatcher
	// is nil at construction time. Per the QDRANT-002 contract, every
	// asset mutation MUST route through the atomic
	// media_assets upsert + outbox enqueue (dispatcher); production
	// composition rejects nil in module_sources.go::WireArtlist so
	// this sentinel should never reach the call sites that fire
	// SearchLiveAndSave — it's here for tests that exercise the
	// dispatcher guard without a wired dispatcher, and as a runtime
	// belt-and-suspenders check inside SearchLiveAndSave itself
	// (catches tampering via SetDispatcher post-construction).
	ErrAssetMutationDispatcherUnavailable = errors.New("artlist: asset mutation dispatcher unavailable (production must wire outbox dispatcher at composition)")
	// ErrPublisherUnavailable is the typed sentinel NewService and
	// DestinationService return when the canonical delivery.Publisher
	// port is nil at construction time. Per F2.11 brutal override, every
	// Drive write from artlist routes through Publisher; the legacy
	// fallback path (DriveFolderManager.EnsureFolder + the silent
	// folderID = rootFolderID branch) has been retired. Production
	// composition rejects nil in module_sources.go::WireArtlist so this
	// sentinel should never reach the operators' eyes — it's here as a
	// composition-time fail-closed (mirrors the QDRANT-002 dispatcher
	// guard) and as a runtime belt-and-suspenders check inside
	// DestinationService itself (catches tampering via late-bound
	// service mutation).
	ErrPublisherUnavailable = errors.New("artlist: delivery.Publisher port unavailable at composition — production must wire delivery.Publisher (F2.11: brutal override retired the legacy DriveFolderManager fallback; silent folderID = rootFolderID fallback is gone)")
)

// Candidate is the application-level representation of a search hit.
// It is intentionally narrower than providers.Candidate so ports stay
// decoupled from the asset registry adapter.
//
// NOTE: the richer SearchRequest (Term/Limit/PreferDB) lives in
// dto_search.go and normalizeSearchTerm lives in run_helpers.go —
// pre-existing call sites already reference them and the ports reuse
// the same types so HTTP transport stays compatible.
type Candidate struct {
	ID         string
	Title      string
	SourceRef  string // primary URL (HLS/m3u8 or progressive)
	PageURL    string // human-friendly page link
	SourceName string
}

// Searcher performs a live search. Implementations include Node/Playwright
// scraper, Pixabay HTTP, Pexels HTTP, in-DB LIKE.
//
// Searcher MUST NOT mutate the request.
type Searcher interface {
	Search(ctx context.Context, req SearchRequest) ([]Candidate, error)
}

// DownloadRequest describes what to download. SourceRef is the primary URL
// returned by the Searcher; DestinationID is the resolved local-path under
// which the bytes should be staged (application owns the policy; infrastructure
// decides the actual filesystem layout).
type DownloadRequest struct {
	SourceRef     string
	DestinationID string
	Filename      string
}

// DownloadResult carries the result of a successful Download.
type DownloadResult struct {
	LocalPath string
	Bytes     int64
}

// Downloader performs the binary download. Implementations may use yt-dlp
// for HLS (Artlist CDN serves .m3u8) or plain HTTP for progressive MP4.
//
// Downloader MUST NOT decide asset lifecycle transitions (that's the
// application's job via Indexer/AssetStore); it just stages bytes.
type Downloader interface {
	Download(ctx context.Context, req DownloadRequest) (*DownloadResult, error)
}

// AssetStore reads/writes the canonical media_assets table.
//
// AssetStore MUST NOT publish indexing or upload side-effects — that's
// the application's job via Indexer and Uploader.
//
// PR2.5: ports extended to mirror *assets.ClipsRepository's full
// surface so the legacy repo concretes in application/artlist.Service
// can be swapped for AssetStore without breaking callers. New methods
// (UpsertClip / SearchClips / UpdateSearchTerms) keep the legacy
// signatures so consumers in service_test.go + diagnostics_service.go
// + search_fallback.go continue to compile against the port. Once the
// ProviderRegistry (providers/artlist/adapter) is rebuilt on the new
// searcher port, the SearchByTerms/CountClips/LastUpdatedAtForTerm
// trio can be deleted.
type AssetStore interface {
	Get(ctx context.Context, id string) (*asset.Asset, error)
	// Upsert is the canonical write entry point. Mirrors the
	// application-level upsert contract used by SearchLiveAndSave.
	//
	// QDRANT-asset-mutation isolation (June 2026): UpsertClip was
	// DELETED from this port. Production callers (artlist search +
	// orphaned callers) MUST go through outbox.Dispatcher.EnqueueAndIndex
	// (the canonical outbox-compliant path). The lower-level UpsertClip
	// remains on *assets.ClipsRepository for the dispatcher's exclusive
	// use; see internal/application/assets/mutations/primitives.go for
	// the narrowed surface.
	Upsert(ctx context.Context, clip *asset.Asset) error
	SearchByTerms(ctx context.Context, source string, keywords []string, limit int) ([]*asset.Asset, error)
	// SearchClips is the term-exact variant used by Search /
	// Diagnostics. The mismatch with SearchByTerms (term vs keywords)
	// comes from the legacy split between "term of interest" and
	// "tokens to index"; until search unification lands, both shapes
	// coexist on the port.
	SearchClips(ctx context.Context, source string, term string) ([]*asset.Asset, error)
	CountClips(ctx context.Context) (int, error)
	LastUpdatedAtForTerm(ctx context.Context, term string) (*string, error)
	// UpdateSearchTerms keeps the clip_search_terms index in lockstep
	// with semantic enrichment. Called twice per fresh search hit:
	// once after Upsert (raw title + term) and once after the
	// semantic_enricher (rich search_text).
	UpdateSearchTerms(ctx context.Context, clipID string, source string, name string, tags []string, searchText string) error
}

// Uploader was the narrow port for folder-ensure + upload. F2.11
// (June 2026) retired the interface entirely (override brutal): the
// service's destination_service.go::ResolveDestination now routes
// ONLY through delivery.Publisher (the canonical write canal per
// DRIVE-005 closure), and semantic_enricher.go's
// updateCumulativeMetadataJSON uses drive.Reader for ListByQuery +
// Download and delivery.Publisher.Publish for upload. There is no
// remaining consumer of the Uploader interface (no callers in this
// package, no callers outside this package). The dual surface
// (Uploader + DriveFolderManager) that PR2.7 introduced as a
// layering compromise is gone — all Drive writes fan out through
// delivery.Publisher per the AGENTS.md godlike/06 'one owner per
// fact' rule.

// DriveFolderManager was the wide port (EnsureFolder + ListByQuery +
// Download + Upload) PR2.7 introduced to retire the raw
// *google.golang.org/api/drive/v3 leak from ServiceDeps.DriveClient.
// F2.11 (June 2026) retired the interface entirely:
//
//   - DestinationService uses delivery.Publisher.ResolveFolder only
//     (the canonical folder-resolution path). The legacy `else if
//     driveManager != nil` branch + the silent `folderID = rootFolderID`
//     fallback are gone — a nil Publisher at composition is now the
//     fail-closed wiring error ErrPublisherUnavailable.
//
//   - SemanticEnricher's updateCumulativeMetadataJSON now uses
//     drive.Reader for the read-modify-write path (ListByQuery →
//     SearchFiles; Download → DownloadFile) and delivery.Publisher.Publish
//     for the upload. The TRASH on the previous metadata.json still
//     routes through drive.FileLifecycle (the CARD-3 port split out
//     from DriveFolderManager in PR2.7).
//
// Per AGENTS.md godlike/06 'one owner per fact': drive.Reader owns the
// read surface, drive.FileLifecycle owns the lifecycle surface, and
// delivery.Publisher owns the write surface. The composite
// DriveFolderManager adapter (which conflated those three) is gone.
//
// The DriveFileRef alias to drivepkg.DriveFileRef was RETIRED together
// with the wide DriveFolderManager interface in F2.11 (zero remaining
// callers — the alias only existed to support the now-defunct
// ListByQuery return type). Per AGENTS.md Code Hygiene ("remove unused
// variables, functions, and files as a result of your changes"), the
// alias was deleted from this file in the F2.11 commit.
//
// F3.14 follow-up (June 2026): the underlying drivepkg.DriveFileRef
// type definition in internal/infrastructure/drive/types.go was also
// retired (zero direct callers after the F2.11 + F3.14 brutal-override
// route-through drive.Reader / delivery.Publisher / drive.FileLifecycle
// per godlike/06 'one owner per fact'). The audit-only doc-comment
// references in verifier_adapter.go, artifacts/verifier.go, and this
// file are updated to point at the post-F3.14 surface rather than the
// retired type name.

// Indexer publishes a clip embedding into Qdrant (or whatever vector store).
// Implementations may be no-op in tests.
type Indexer interface {
	IndexClip(ctx context.Context, clipID string) error
	IsEnabled() bool
}

// MetadataWriter is the seam the application uses to enrich a clip with
// semantic metadata (search_text, concept_tags, subjects, mood). The
// implementation may call an LLM and then write back into AssetStore;
// the port realises the contract per use case, the orchestration lives
// in the application.
type MetadataWriter interface {
	// Enrich performs synchronous semantic enrichment and persists the
	// metadata back to AssetStore atomically. The previous fire-and-forget
	// EnrichAsync method was removed in P0.6 (June 2026) — see
	// docs/architecture/godlike/07 for the no-fake-availability reasoning.
	// P0.18 will introduce the structured outbox-driven replacement
	// in a successive wave.
	Enrich(ctx context.Context, clip *asset.Asset, term string) error
}

// Dispatcher is the port for the media_index_outbox that atomically
// combines UpsertClip + IndexClip in a single transaction. PR2.4
// introduces this port so the application layer does not depend on
// the concrete *outbox.Dispatcher from infrastructure.
//
// When nil, dispatchBridge falls back to the legacy UpsertClip +
// IndexClip pair (see dispatch_bridge.go). Production wiring always
// provides a non-nil implementation.
type Dispatcher interface {
	EnqueueAndIndex(ctx context.Context, clip *asset.Asset, hash string) error

	// SaveDiscoveredAsset is the discovery-only upsert path (chip 2,
	// June 2026 fix-FASE9 followups plan). It UPSERTs the clip row in
	// media_assets with the supplied lifecycle_state and index_state
	// (canonical call from SearchLiveAndSave: StateStaging +
	// StateDiscovered) WITHOUT emitting any outbox event.
	//
	// The downstream artlist.run job emits the canonical
	// asset.index.requested event AFTER the post-processing finalizer
	// produces a fully-populated clip (real hash, Drive file id,
	// upload completed). This removes the "premature Qdrant indexing
	// of an incomplete asset" failure mode that the previous
	// EnqueueAndIndex-on-discovery wiring produced (Qdrant saw a
	// half-built asset for some seconds between discovery and
	// upload-commit).
	//
	// Lifecycle + IndexState are explicit args (not read from clip
	// fields) so callers cannot accidentally stamp a state they did
	// not intend — the method explicitly writes both onto the clip
	// before the upsert so production readers see a coherent row.
	SaveDiscoveredAsset(ctx context.Context, clip *asset.Asset, lifecycle asset.LifecycleState, idx asset.IndexState) error
}

// ArtlistConfigPort is the minimal typed port that exposes the
// artlist-defaults the HTTP handler reads for request normalization.
// The handler consumes only one config value (`ArtlistRootFolderID`),
// so the port is intentionally 1-method. Moving the derivation
// behind a port keeps the api layer out of the infrastructure-layer
// imports and gives tests a trivially-mockable seam.
//
// Only ArtlistRootFolderID is exposed because that is the only value
// the handler currently reads from config (see
// internal/api/assets/artlist/artlist_handlers.go::RunTagPipeline).
// New defaults land here as additional methods; do NOT widen this to a
// whole-config accessor — expanding the surface would defeat the
// Pattern 0 minimal-port discipline (AGENTS.md).
type ArtlistConfigPort interface {
	ArtlistRootFolderID() string
}
