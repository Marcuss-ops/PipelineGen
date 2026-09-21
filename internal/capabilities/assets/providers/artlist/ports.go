// Package artlist owns orchestration, policy, and ports for the Artlist media catalog.
package artlist

import (
	"context"
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providerassets"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

var (
	ErrEmpty             = errors.New("artlist: empty input")
	ErrUnavailable       = errors.New("artlist: source unavailable")
	ErrTimeout           = errors.New("artlist: source timeout")
	ErrRateLimited       = errors.New("artlist: source rate limited")
	ErrInvalidResponse   = errors.New("artlist: invalid response")
	ErrEmptyResult       = errors.New("artlist: empty result")
	ErrNotFound          = errors.New("artlist: not found")
	ErrTransportFallback = errors.New("artlist: transport failure, fall back to next searcher")

	ErrAssetMutationDispatcherUnavailable = errors.New("artlist: asset mutation dispatcher unavailable (production must wire outbox dispatcher at composition)")
	ErrPublisherUnavailable               = errors.New("artlist: delivery.Publisher port unavailable at composition — production must wire delivery.Publisher (F2.11: brutal override retired the legacy DriveFolderManager fallback; silent folderID = rootFolderID fallback is gone)")
)

// Candidate is the canonical provider-agnostic search hit.
type Candidate = providerassets.ProviderAsset

type Searcher interface {
	Search(ctx context.Context, req SearchRequest) ([]Candidate, error)
}

type DetailFetcher interface {
	FetchDetails(ctx context.Context, clipPageURL string) (*Candidate, error)
}

type DownloadRequest struct {
	SourceRef     string
	DestinationID string
	Filename      string
	ClipPageURL   string
	ClipID        string
}

type DownloadResult struct {
	LocalPath string
	Bytes     int64
}

type Downloader interface {
	Download(ctx context.Context, req DownloadRequest) (*DownloadResult, error)
}

// Transcriber extracts a transcript and detected language from staged media.
type Transcriber interface {
	Transcribe(ctx context.Context, audioPath string) (transcript string, languageCode string, err error)
}

// AssetStore is the canonical Artlist-facing media asset persistence port.
//
// MEDIA LEGACY READ-PLANE DEMOLITION (2026-09-21, sub-wave B'): CountClips and
// LastUpdatedAtForTerm are REMOVED from this interface. They are aggregates over
// media_assets, i.e. media-SSOT facts, and keeping them here forced the
// operational SQLite store to own a media_assets read
// (imagesregistry/clip_list_queries.go) purely to satisfy the interface. They now
// live on MediaStats, answered by the engine that owns the rows. Removing them is
// what let that file leave the read-plane inventory; every other consumer of this
// port keeps compiling, because removing methods from an interface does not break
// its implementers.
type AssetStore interface {
	Get(ctx context.Context, id string) (*asset.Asset, error)
	Upsert(ctx context.Context, clip *asset.Asset) error
	SearchByTerms(ctx context.Context, source string, keywords []string, limit int) ([]*asset.Asset, error)
	SearchClips(ctx context.Context, source string, term string) ([]*asset.Asset, error)
	UpdateSearchTerms(ctx context.Context, clipID string, source string, name string, tags []string, searchText string) error
}

// ErrMediaStatsUnavailable is the typed fail-closed sentinel for the aggregate
// surfaces that have no media SSOT handle to answer them. It exists because an
// aggregate like /api/artlist/stats has no per-field way to say "unknown": three
// int/bool fields cannot carry an availability state, so answering 0 would be the
// fabricated availability godlike/07 forbids.
var ErrMediaStatsUnavailable = errors.New("artlist: media statistics port is not wired (no media PostgreSQL SSOT handle at composition) — the aggregate cannot be answered, and must not be reported as 0")

// MediaStats reports the media_assets aggregates the Artlist surfaces need.
//
// WHY IT IS NOT PART OF AssetStore (MEDIA LEGACY READ-PLANE DEMOLITION). These
// are media-SSOT facts, not operational-store ones, and they used to be bolted
// onto AssetStore — which forced the operational SQLite store to keep
// implementing media_assets reads (imagesregistry/clips_statistics.go, then
// clip_list_queries.go) purely to satisfy that interface. A separate port lets the
// operational store stop reading the media table altogether, while the numbers
// are answered by the engine that owns them: pgmedia.MediaStatisticsReader.
//
// The three methods answer three DIFFERENT questions on purpose; that reader's
// live-PostgreSQL tests pin each difference:
//
//   - CountBySource: rows for one source, soft-deleted INCLUDED (an indexed-row
//     metric, not an online one);
//   - CountClips: non-soft-deleted rows across every source, so it is
//     deliberately NOT the sum of CountBySource;
//   - LastUpdatedAtForTerm: the newest artlist created_at whose tags carry the
//     term, nil when nothing matches.
//
// OPTIONAL AT COMPOSITION (godlike/07): it is nil when no media SSOT handle is
// wired. The diagnostics surface then reports each field as unavailable instead
// of fabricating a number, and the aggregate surface fails closed with
// ErrMediaStatsUnavailable.
type MediaStats interface {
	CountBySource(ctx context.Context, source string) (int, error)
	CountClips(ctx context.Context) (int, error)
	LastUpdatedAtForTerm(ctx context.Context, term string) (*string, error)
}

type Indexer interface {
	IndexClip(ctx context.Context, clipID string) error
	IsEnabled() bool
}

type MetadataWriter interface {
	Enrich(ctx context.Context, clip *asset.Asset, term string) error
}

// Dispatcher owns the canonical media_assets mutation + outbox paths.
type Dispatcher interface {
	EnqueueAndIndex(ctx context.Context, clip *asset.Asset, hash string) error
	SaveDiscoveredAsset(ctx context.Context, clip *asset.Asset, lifecycle asset.LifecycleState, idx asset.IndexState) error
}

type ArtlistConfigPort interface {
	ArtlistRootFolderID() string
}

type IsLiveProbe interface {
	Probe(ctx context.Context) (bool, error)
}

// RunRecord mirrors the persisted artlist_runs aggregate columns written by RunRepository.
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

type RunRepository interface {
	Record(ctx context.Context, rec RunRecord) error
	LatestRun(ctx context.Context) (*LatestRunSummary, error)
}

type LatestRunSummary struct {
	RunID     string
	Term      string
	Status    string
	Error     string
	CreatedAt string
}

var ErrRunRepositoryUnavailable = errors.New(
	"artlist: RunRepository port unavailable at composition — production must wire artlist_runs_repository (godlike/07 no-fake-availability: aggregate stats write is mandatory for /api/artlist/run honesty)",
)

var ErrAcquisitionModeBlocked = errors.New("artlist: acquisition_mode is manual_import; automatic downloads are not allowed (Fase 6 / Commit 1)")
var ErrDailyDownloadLimitExceeded = errors.New("artlist: daily download limit exceeded")
var ErrAutomaticDownloadsDisabled = errors.New("artlist: automatic downloads are disabled (daily limit is 0)")

type DownloadAuditStatus string

const (
	DownloadAuditStatusPending   DownloadAuditStatus = "pending"
	DownloadAuditStatusSucceeded DownloadAuditStatus = "succeeded"
	DownloadAuditStatusFailed    DownloadAuditStatus = "failed"
)

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

type DownloadAuditRepository interface {
	RecordDownload(ctx context.Context, rec DownloadAuditRecord) (string, error)
	UpdateDownloadStatus(ctx context.Context, id string, status DownloadAuditStatus) error
	CountDailyDownloads(ctx context.Context, provider, accountID string) (int, error)
}
