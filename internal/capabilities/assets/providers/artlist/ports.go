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
type AssetStore interface {
	Get(ctx context.Context, id string) (*asset.Asset, error)
	Upsert(ctx context.Context, clip *asset.Asset) error
	SearchByTerms(ctx context.Context, source string, keywords []string, limit int) ([]*asset.Asset, error)
	SearchClips(ctx context.Context, source string, term string) ([]*asset.Asset, error)
	CountClips(ctx context.Context) (int, error)
	CountBySource(ctx context.Context, source string) (int, error)
	LastUpdatedAtForTerm(ctx context.Context, term string) (*string, error)
	UpdateSearchTerms(ctx context.Context, clipID string, source string, name string, tags []string, searchText string) error
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
