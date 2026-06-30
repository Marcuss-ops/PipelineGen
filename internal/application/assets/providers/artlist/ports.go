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
	"io"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	drivepkg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
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

// Uploader uploads a local file to a destination folder. The application
// decides *where* to upload (which Drive folder); infrastructure owns the
// Drive SDK.
//
// PR2.7 cross-reference: the wider DriveFolderManager port (above) also
// satisfies this interface — *DriveFolderManagerAdapter implements both
// Uploader (EnsureFolder + Upload) and DriveFolderManager (the full
// 5-method Drive surface). When a consumer needs only upload/folder
// ensure (e.g. composition-root bundle assembly), prefer Uploader for
// the narrower contract. When a consumer needs List/Trash/Download as
// well (e.g. semantic_enricher metadata.json flow, destination_service
// folder resolution with pre-existence checks), prefer
// DriveFolderManager. Kept as separate interfaces to avoid forcing
// callers to stub methods they don't use — the adapter satisfies both,
// so wiring at the composition root can hand out the wider port and
// the narrower consumers simply ignore the extra methods. In practice
// the composition root instantiates one DriveFolderManager and passes
// it to consumers needing narrower views via a compile-time widening
// (the adapter satisfies Uploader too).
type Uploader interface {
	EnsureFolder(ctx context.Context, parent string, segments ...string) (string, error)
	Upload(ctx context.Context, localPath, folderID, filename string) (string, error)
}

// DriveFileRef is the application-level reference to a Drive file.
// PR2.7 declares it as a Go type alias to the canonical struct in
// infrastructure/drive (drivepkg.DriveFileRef) so the DriveFolderManager
// port can return []DriveFileRef without the infrastructure adapter
// importing the application package. Callers continue to write
// artlist.DriveFileRef — the alias is transparent; method sets, struct
// fields, and interface-style return values stay interchangeable.
//
// Why an alias and not a parallel struct: a parallel struct would either
// (a) duplicate the field set and require conversion at every seam
// (expensive to maintain, easy to drift) or (b) be imported into
// ports.go anyway (no cycle benefit). The alias keeps a single source of
// truth (drive.DriveFileRef) while preserving the developer-facing
// name callers expect. The "Name" field is currently used only by
// diagnostic logging; semantic_enricher reads .ID — kept broad enough
// for future callers that need to identify siblings by filename.
type DriveFileRef = drivepkg.DriveFileRef

// DriveFolderManager is the wide port covering all Drive folder/file
// operations the application needs. PR2.7 introduced this port to
// replace (a) the raw *google.golang.org/api/drive/v3 Service previously
// held in ServiceDeps.DriveClient (a concrete SDK leak) and (b) the
// narrow *drive.Uploader concrete previously held by SemanticEnricher.
//
// Wave D Commit 1 (June 2026) updates the surrounding composition:
// the *google.golang.org/api/drive/v3 concrete never lived directly
// in the artlist package — it lived only on ArtlistBundle.DriveClient
// as the plumbing channel that fed the *drive.DriveFolderManagerAdapter
// in WireArtlist. ServiceDeps.DriveClient was retired by PR2.7 and
// remains retired (the application package no longer holds the raw
// SDK concrete); the bundle-level DriveClient is the back-compat
// affordance Wave D Commit 1 retained per operator signal pending
// the Wave D Commit 2/3 retirement decision.
//
// Application decides WHAT (which file, which folder); infrastructure
// owns HOW (the SDK, retries, transport). Domain shape (DriveFileRef +
// io.ReadCloser) hides the SDK types from callers.
//
// All errors must map to the package's sentinel errors (ErrEmpty,
// ErrUnavailable, ErrTimeout, ErrInvalidResponse) so callers can't leak
// transport jargon. Where SDK errors don't fit a sentinel cleanly, the
// wrapped error chain keeps the SDK message accessible via errors.Is/
// errors.As for diagnostic logging.
type DriveFolderManager interface {
	// EnsureFolder creates (or reuses) a folder whose path is
	// composed from parent + segments. Idempotent: when a folder
	// already exists at any level of the path, it is reused rather
	// than creating a duplicate. Returns the resolved folder ID.
	EnsureFolder(ctx context.Context, parent string, segments ...string) (string, error)

	// ListByQuery returns DriveFileRef entries matching the supplied
	// raw query string (e.g. "'XYZ' in parents and trashed = false and
	// name = 'metadata.json'"). Server-side filtering of trashed
	// entries is the caller's responsibility (include
	// "and trashed = false" in the query). The domain result shape
	// avoids leaking *assetsapi.File into business logic.
	ListByQuery(ctx context.Context, query string) ([]DriveFileRef, error)
	// Download fetches a file's content as a stream. The caller MUST
	// close the returned io.ReadCloser. Content-type is returned as a
	// convenience for callers that branch on MIME (rare; metadata.json
	// downloads don't need this).
	Download(ctx context.Context, fileID string) (io.ReadCloser, string, error)

	// Upload uploads a local file to a Drive folder. Returns the
	// webViewLink of the new/updated file (callers that just want
	// "did it work" discard it). When a file with the same name
	// already exists in the folder, the implementation MUST update
	// in place rather than create a duplicate (matches the legacy
	// upload-on-conflict behaviour callers depended on).
	Upload(ctx context.Context, localPath, folderID, filename string) (string, error)
}

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
