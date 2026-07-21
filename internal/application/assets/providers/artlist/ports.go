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

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providerassets"
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
// It is now the canonical providerassets.ProviderAsset so all provider
// adapters share one rich, provider-agnostic model.
//
// NOTE: the richer SearchRequest (Term/Limit/PreferDB) lives in
// dto_search.go and normalizeSearchTerm lives in run_helpers.go —
// pre-existing call sites already reference them and the ports reuse
// the same types so HTTP transport stays compatible.
type Candidate = providerassets.ProviderAsset

// Searcher performs a live search. Implementations include Node/Playwright
// scraper, Pixabay HTTP, Pexels HTTP, in-DB LIKE.
//
// Searcher MUST NOT mutate the request.
type Searcher interface {
	Search(ctx context.Context, req SearchRequest) ([]Candidate, error)
}

// DetailFetcher fetches rich structured metadata for a single clip page.
// The canonical implementation is the Node.js scraper /detail endpoint;
// other providers may leave it unimplemented.
//
// DetailFetcher MUST NOT mutate the request.
type DetailFetcher interface {
	FetchDetails(ctx context.Context, clipPageURL string) (*Candidate, error)
}

// DownloadRequest describes what to download. SourceRef is the primary URL
// returned by the Searcher; DestinationID is the resolved local-path under
// which the bytes should be staged (application owns the policy; infrastructure
// decides the actual filesystem layout).
//
// PR-ARTLIST-DOWNLOAD-SURFACE-UNIFY (July 2026): ClipPageURL and ClipID are
// optional fields that feed the Node scraper's /download payload
// (clip_page_url + clip_id in the JSON body). When non-empty, the unified
// resolver prefers the Node scraper path (browser-authenticated session)
// over the yt-dlp / HTTP paths. When empty, the resolver detects the
// transport from the SourceRef URL alone (Artlist-shaped → scraper,
// .mp4/.mov → HTTP, .m3u8 → yt-dlp).
type DownloadRequest struct {
	SourceRef     string
	DestinationID string
	Filename      string
	ClipPageURL   string // optional: Artlist clip page URL for Node scraper navigation
	ClipID        string // optional: Artlist numeric clip ID for scraper output naming
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
	// PR-P2-DIAGNOSTICS-REALE (July 2026): per-source count surfaced
	// via /api/artlist/diagnostics. Sourced from the canonical
	// ClipsRepository.CountBySource (clips_statistics.go godlike/06 SSOT).
	// Source is required (non-empty) — empty source returns the typed
	// errors.ErrEmptySource (mirror ErrEmptySource discipline on the
	// artlist infrastructure port; the infra-side ErrEmptySource is
	// the canonical sentinel, callers branch on errors.Is).
	CountBySource(ctx context.Context, source string) (int, error)
	LastUpdatedAtForTerm(ctx context.Context, term string) (*string, error)
	// UpdateSearchTerms keeps the clip_search_terms index in lockstep
	// with semantic enrichment. Called twice per fresh search hit:
	// once after Upsert (raw title + term) and once after the
	// semantic_enricher (rich search_text).
	UpdateSearchTerms(ctx context.Context, clipID string, source string, name string, tags []string, searchText string) error
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

// IsLiveProbe is the canonical port for the runtime liveness probe
// (PR-ARTLIST-LIVE-WIRE, July 2026). Implementations perform an HTTP
// GET against the local /api/artlist/stats endpoint (configurable URL)
// with a configurable timeout and report whether the server is
// reachable + responding 2xx. Return semantics per godlike/07
// no-fake-availability:
//   - (true,  nil)  → service is live (HTTP 2xx within timeout)
//   - (false, nil)  → service responded but not 2xx (4xx/5xx classified
//     as "not live" without surfacing transient details;
//     the diagnostic layer decides escalation)
//   - (false, err)  → transport failure (DNS/TCP/timeout/connection-refused);
//     the caller decides retry policy
//
// Probe MUST NOT mutate the request. Probe MUST NOT cache results across
// calls (compositional callers wrap with their own LRU if desired).
type IsLiveProbe interface {
	Probe(ctx context.Context) (bool, error)
}

// RunRecord is the canonical aggregate stats row for an Artlist run.
// It maps 1:1 onto the artlist_runs table columns in
// migrations/sqlite/001_velox_core.sql:46-62.
//
// PR-ARTLIST-PERSIST-FIX (July 2026): this struct is the typed surface
// for the RunRepository port and replaces the silent-degrade path that
// owned /api/artlist/run orchestration without writing per-run
// aggregates. godlike/06 SSOT: artlist_runs rows have exactly one
// canonical writer (RunRepository.Record) and one canonical reader
// (/api/artlist/stats via the legacy counters).
//
// godlike/06 SSOT (column-level reconciliation): the struct fields
// mirror the canonical schema verbatim. The migration defines 13
// columns: id PK (RunID), term, status, root_folder_id, tag_folder_id,
// requested_count, found_count, processed_count, skipped_count,
// failed_count, error_message, created_at, updated_at. created_at and
// updated_at are filled by the SQLite DEFAULT datetime('now') clause;
// the canonical writer supplies only the 11 explicitly-tracked fields.
// Strategy / DryRun / StartedAt / CompletedAt / LastError are NOT
// persisted in the current schema — they were aspirational fields in
// an earlier draft and the schema never adopted them.
type RunRecord struct {
	RunID        string
	Term         string
	Status       string
	RootFolderID string
	TagFolderID  string
	RequestedN   int
	FoundN       int
	ProcessedN   int
	SkippedN     int
	FailedN      int
	ErrorMessage string
}

// RunRepository is the canonical port for persisting Artlist run
// aggregates (the aggregate stats row behind /api/artlist/run +
// /api/artlist/stats).
//
// godlike/06 SSOT (one canonical owner per fact): RunRepository is the
// SOLE writer of artlist_runs rows; no other code path may insert or
// update that table.
//
// godlike/07 no-fake-availability: NewService fails closed
// (ErrRunRepositoryUnavailable) when this port is nil at composition;
// production composition MUST wire a concrete from
// internal/infrastructure/database/sqlite/assets/artlist_runs_repository.go.
//
// Record MUST upsert on RunID (UNIQUE-by-RunID key) so concurrent
// retry of the same logical run collapses into ONE row rather than
// producing duplicate aggregate stats. Record MUST persist even when
// the orchestrator discovered zero candidates (the per-run aggregate
// "found=0" row is observable truth, not silent-omit).
//
// PR-P2-DIAGNOSTICS-REALE (July 2026): LatestRun returns the most
// recent run summary sorted by created_at DESC + id DESC (id breaks
// ties when multiple rows in the same datetime('now') second). The
// diagnostics endpoint surfaces this as LatestRun / LastError /
// operator grip on past runs without a /runs/:run_id roundtrip.
// Returns (nil, nil) when the artlist_runs table is empty (fresh
// install — operators treat nil as "no runs yet", vs the
// sentinel-zero-value LatestRunSummary shape which would confuse
// them with `run_id="" + status=""`).
type RunRepository interface {
	Record(ctx context.Context, rec RunRecord) error

	// LatestRun returns the most-recent run summary (sorted by
	// created_at DESC, id DESC). Returns (nil, nil) when no runs
	// exist. Returns a non-nil error ONLY on SQL-level failure
	// (transport-level, table missing, permission denied); never
	// returns an error for the empty-table case.
	LatestRun(ctx context.Context) (*LatestRunSummary, error)
}

// LatestRunSummary is the typed read-shape of one artlist_runs row,
// surfaced via DiagnosticsResponse.LatestRun. field names mirror
// the canonical artlist_runs schema columns verbatim (godlike/06
// SSOT column-level reconciliation).
type LatestRunSummary struct {
	RunID     string
	Term      string
	Status    string
	Error     string // mirrors error_message column
	CreatedAt string // ISO-8601 (datetime('now') UTC)
}

// ErrRunRepositoryUnavailable is the typed sentinel NewService returns
// when the canonical RunRepository port is nil at construction time.
// Mirrors the fail-closed discipline of ErrPublisherUnavailable +
// ErrAssetMutationDispatcherUnavailable.
var ErrRunRepositoryUnavailable = errors.New(
	"artlist: RunRepository port unavailable at composition — production must wire artlist_runs_repository (godlike/07 no-fake-availability: aggregate stats write is mandatory for /api/artlist/run honesty)",
)

// ErrAcquisitionModeBlocked is returned by the download path when the
// operator has configured acquisition_mode=manual_import. Automatic
// downloads are not allowed in this mode; users must import files
// manually and the pipeline ingests them afterwards.
//
// Fase 6 / Commit 1 (July 2026) — the sentinel was renamed from
// ErrManualImportActive to match the user-spec literal. The semantics
// are identical: a download attempt in manual_import mode MUST fail
// closed with this typed error, the failure MUST be surfaced in the
// per-item audit (RunTagItem.Status = "blocked_mode") and the run
// state aggregate (resp.Failed++ so EvaluateRunState verdicts
// PARTIAL_SUCCESS / FAILED on partial / total block).
//
// godlike/07 fail-closed: the resolver MUST surface this sentinel
// verbatim via errors.Is so callers (stager_adapter.go, run_orch
// stages, diagnostic handlers) branch on intent. Silent skip is
// forbidden — the block is observable, not speculative.
var ErrAcquisitionModeBlocked = errors.New("artlist: acquisition_mode is manual_import; automatic downloads are not allowed (Fase 6 / Commit 1)")

// ErrDailyDownloadLimitExceeded is returned when an automatic download
// would exceed the configured ArtlistDailyDownloadLimit for the current
// account/day. The operator must raise the limit or wait until tomorrow.
var ErrDailyDownloadLimitExceeded = errors.New("artlist: daily download limit exceeded")

// ErrAutomaticDownloadsDisabled is returned when acquisition_mode is
// authorized_api but the daily download limit is configured as 0,
// which disables automatic downloads until the operator sets a
// positive limit.
var ErrAutomaticDownloadsDisabled = errors.New("artlist: automatic downloads are disabled (daily limit is 0)")

// DownloadAuditStatus is the lifecycle state of an audited download.
// Only non-failed rows count against the daily limit, but every row
// (including failed) is retained for compliance forensics.
type DownloadAuditStatus string

const (
	DownloadAuditStatusPending   DownloadAuditStatus = "pending"
	DownloadAuditStatusSucceeded DownloadAuditStatus = "succeeded"
	DownloadAuditStatusFailed    DownloadAuditStatus = "failed"
)

// DownloadAuditRecord is a single audited download event.
type DownloadAuditRecord struct {
	AssetID      string
	ExternalURL  string
	AccountID    string
	Provider     string
	Status       DownloadAuditStatus
	DownloadedAt string
	LicenseID    string
	ReleaseID    string
	ProjectID    string
	DownloadedBy string
}

// DownloadAuditRepository persists and queries Artlist download audit
// records. It is the canonical port used by the downloader to enforce
// daily per-account limits and to keep an audit trail of every fetch.
type DownloadAuditRepository interface {
	// RecordDownload persists a download audit row and returns its
	// generated ID. The row is created with status pending so the
	// daily-limit check stays race-free.
	RecordDownload(ctx context.Context, rec DownloadAuditRecord) (string, error)
	// UpdateDownloadStatus updates the status of an existing audit row.
	UpdateDownloadStatus(ctx context.Context, id string, status DownloadAuditStatus) error
	// CountDailyDownloads returns the number of non-failed downloads
	// recorded for the given provider/account on the current UTC day.
	// Pending rows are counted so concurrent downloads cannot overshoot
	// the quota; failed rows are excluded.
	CountDailyDownloads(ctx context.Context, provider, accountID string) (int, error)
}

// ArtlistSearchStrategy is the typed strategy for the Pexels/Pixabay fallback
// chain (PR-AUDIT-5, July 2026). The strategy controls which searchers are
// included in the live-search fallback chain at composition time.
//
// Never automatic invisible fallback: the strategy is wired from config at
// boot and the resolver translates it into an ordered []Searcher at chain-
// construction time. Operators choose the strategy explicitly via
// ARTLIST_SEARCH_STRATEGY (default: artlist_only for backward-compat safety —
// the prior implicit fallback chain is now opt-in).
//
// Canonical values:
//
//	artlist_only               — ONLY the Artlist scraper (no Pixabay/Pexels).
//	                              The safest default: no external stock sources.
//	artlist_then_public_fallback — Artlist scraper first, then Pixabay + Pexels
//	                              as fallback (the prior implicit behaviour).
//	public_only_for_dev          — ONLY Pixabay + Pexels (no scraper). Useful
//	                              for dev/testing without a running Node scraper.
