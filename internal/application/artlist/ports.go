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
	ErrEmpty            = errors.New("artlist: empty input")
	ErrUnavailable      = errors.New("artlist: source unavailable")
	ErrTimeout          = errors.New("artlist: source timeout")
	ErrInvalidResponse  = errors.New("artlist: invalid response")
	ErrEmptyResult      = errors.New("artlist: empty result")
	ErrNotFound         = errors.New("artlist: not found")
	ErrTransportFallback = errors.New("artlist: transport failure, fall back to next searcher")
)



// Candidate is the application-level representation of a search hit.
// It is intentionally narrower than providers.Candidate so ports stay
// decoupled from the asset registry adapter.
//
// NOTE: the richer SearchRequest (Term/Limit/PreferDB) lives in
// dto_search.go and normalizeSearchTerm lives in run_helpers.go —
// pre-existing call sites already reference them and the ports reuse
// the same types so HTTP transport stays compatible.
//
type Candidate struct {
	ID          string
	Title       string
	SourceRef   string // primary URL (HLS/m3u8 or progressive)
	PageURL     string // human-friendly page link
	SourceName  string
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
	Upsert(ctx context.Context, clip *asset.Asset) error
	// UpsertClip mirrors *assets.ClipsRepository.UpsertClip (legacy
	// dispatch_bridge.go uses it directly). Both Upsert and
	// UpsertClip write the same row + emit the same outbox event;
	// the suffix distinguishes caller intent.
	UpsertClip(ctx context.Context, clip *asset.Asset) error
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
type Uploader interface {
	EnsureFolder(ctx context.Context, parent string, segments ...string) (string, error)
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
	Enrich(ctx context.Context, clip *asset.Asset, term string) error
	EnrichAsync(ctx context.Context, clip *asset.Asset, term string)
}

