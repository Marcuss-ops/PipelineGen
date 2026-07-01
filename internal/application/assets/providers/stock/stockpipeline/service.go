package stockpipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	youtube "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	downloader "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

var rng = rand.New(rand.NewSource(time.Now().UnixNano()))

// PipelineConfig holds configuration for the stock pipeline run.
type PipelineConfig struct {
	ChunkDuration  int
	MaxResults     int
	EffectInterval int
	EffectsDir     string
}

// DefaultPipelineConfig returns a PipelineConfig with sensible defaults.
func DefaultPipelineConfig() PipelineConfig {
	return PipelineConfig{
		ChunkDuration:  25,
		MaxResults:     25,
		EffectInterval: 4,
		EffectsDir:     "assets/effects/EffettiVisiv",
	}
}

// Sentinel errors returned by NewService validation. Each error names a
// missing dependency so composition-time call sites can forward a single
// error to operators and tests can assert the precise missing dep without
// reading through the wrapped fmt chain.
var (
	ErrStockPipelineNilCfg            = errors.New("stockpipeline.NewService: cfg is required")
	ErrStockPipelineNilLog            = errors.New("stockpipeline.NewService: log is required")
	// F2.10: ErrStockPipelineNilDriveSvc RETIRED. The legacy
	// DriveSvc surface (driveup.Admin + its upload/folder-resolution
	// methods) was dropped entirely (override brutal). Every Drive
	// write from the stock pipeline now routes through
	// delivery.Publisher.Publish + delivery.Publisher.ResolveFolder.
	ErrStockPipelineNilClipsRepo      = errors.New("stockpipeline.NewService: storage.ClipsRepo is required (production path)")
	ErrStockPipelineNilAssetIndex     = errors.New("stockpipeline.NewService: storage.AssetIndex is required (production path)")
	ErrStockPipelineNilDispatcher     = errors.New("stockpipeline.NewService: storage.Dispatcher is required (QDRANT-002 PR7 — production canonical ingest)")
	ErrStockPipelineNilCutter         = errors.New("stockpipeline.NewService: media.Cutter is required (PR6 port)")
	ErrStockPipelineNilRenderer       = errors.New("stockpipeline.NewService: media.Renderer is required (PR6 port)")
	ErrStockPipelineNilClipIndexer    = errors.New("stockpipeline.NewService: media.ClipIndexer is required")
	ErrStockPipelineNilMetadataWriter = errors.New("stockpipeline.NewService: media.MetaWriter is required (semantic enrichment for Drive metadata.json upload)")
	ErrStockPipelineNilYouTube        = errors.New("stockpipeline.NewService: YouTube is required (provider metadata enrichment for direct URL sources)")
	ErrStockPipelineNilJobs           = errors.New("stockpipeline.NewService: Jobs is required (async job tracker for HandleJob / RegisterHandler)")
)

// StorageDeps groups the canonical media_assets + Qdrant + asset-index stack.
// Three fields — under the AGENTS.md 10-per-bundle cap.
type StorageDeps struct {
	ClipsRepo  *assets.ClipsRepository
	AssetIndex *assetindex.Service
	Dispatcher *outbox.Dispatcher
}

// MediaDeps groups the PR6 ports + semantic enrichment. Four fields —
// under the 10-per-bundle cap. The Cutter / Renderer ports are PR6-defined
// (see ports.go); MetaWriter / ClipIndexer are cross-cutting enrichment.
type MediaDeps struct {
	Cutter      VideoCutter
	Renderer    StockRenderer
	ClipIndexer *clipindexer.Service
	MetaWriter  *semantic.MetadataWriter
}

// ────────────────────────────────────────────────────────────────────
// Audit P0 #6 (July 2026): narrow port types so test fakes can satisfy
// them via Go's structural subtyping without mocking the full
// *assetindex.Service (60+ methods), *assets.ClipsRepository (25+ methods),
// or *outbox.Dispatcher surface. Production wiring passes concrete
// pointers which satisfy these interfaces structurally — the
// `Deps` shape above is unchanged and module_sources.go::WireStockPipeline
// is NOT modified.
// ────────────────────────────────────────────────────────────────────

// stockAssetIndexUpserter is the narrow surface the stock pipeline
// uses from *assetindex.Service. Only Upsert is invoked
// (run_upload.go::indexChunkToAssetIndex).
type stockAssetIndexUpserter interface {
	Upsert(ctx context.Context, rec *assetindex.AssetRecord) error
}

// stockClipsSearchTermUpdater is the narrow surface the stock pipeline
// uses from *assets.ClipsRepository. Only UpdateSearchTerms is invoked
// (run_upload.go::upsertChunkAndDispatch). Audit P0 #6 mandates the
// failure halts the dispatch path — see run_upload.go::upsertChunkAndDispatch.
type stockClipsSearchTermUpdater interface {
	UpdateSearchTerms(ctx context.Context, clipID, source, name string, tags []string, searchText string) error
}

// stockChunkDispatcher is the narrow surface the stock pipeline uses
// from *outbox.Dispatcher. Only EnqueueAndIndex is invoked
// (run_upload.go::upsertChunkAndDispatch).
type stockChunkDispatcher interface {
	EnqueueAndIndex(ctx context.Context, clip *asset.Asset, fileHash string) error
}

// Deps is the canonical constructor input for stockpipeline.Service
// (PR-D, Wave 22 §D3, June 2026). Sized at 7 top-level fields — well
// under the AGENTS.md 8-per-bundle cap. Sub-dependencies (StorageDeps
// + MediaDeps) group related concerns so the field-name list reads as
// the canonical composition pattern:
//
//	Cfg, Log, Drive         — pure data + Drive SDK
//	Storage                 — media_assets + outbox + asset-index stack
//	Media                   — PR6 ports + semantic enrichment
//	YouTube                 — provider for metadata enrichment
//	Jobs                    — async job tracker
//
// Pattern source: artlist.ServiceDeps (PR2.5, June 2026) — `ServiceDeps`
// embeds `ServicePorts + ServiceDependencies` for terse construction;
// here the sub-struct names carry semantic meaning rather than the
// "ports vs dependencies" split at the artlist boundary (the stock
// pipeline has fewer ports to lift out).
//
// PR-D: setter pattern (SetCutter, SetRenderer, SetClipsRepo,
// SetAssetIndex, SetDispatcher, SetJobsSvc, SetYoutubeService,
// SetClipIndexer, SetMetadataWriter) is REMOVED. All dependencies
// are constructor arguments on Deps — replaces the late-bind ordering
// hazard that swapped the canonical ingestion path on every
// composition-time race in WireStockPipeline.
type Deps struct {
	// F2.10: Drive field dropped — every Drive write routes through
	// delivery.Publisher (Publisher field below). The legacy
	// driveup.Admin surface (UploadFile + GetOrCreateFolder + Trash +
	// Delete etc.) was retired (override brutal). Folder resolution
	// inside the pipeline run uses publisher.ResolveFolder
	// (DestinationStock policy) instead of driveutil.EnsureFolderPath.
	Cfg       *config.Config
	Log       *zap.Logger
	Publisher delivery.Publisher
	Storage   StorageDeps
	Media     MediaDeps
	// DELIBERATELY FLAT — YouTube + Jobs are cross-cutting fields, intentionally
	// NOT nested under a sub-group. They are conceptually distinct from the
	// Storage (DB stack) and Media (PR6 ports + semantic enrichment) buckets
	// even though they share a "cross-cutting" semantic cluster. Grouping
	// them under a CrossCuttingDeps struct would add a third embedded
	// sub-group without any concrete shared-validation benefit (each field
	// has its own per-field sentinel). The Deps doc-comment explicitly
	// enumerates this bucketing; future maintainers should preserve the
	// shape rather than introduce a CrossCuttingDeps for symmetry.
	YouTube *youtube.Service
	Jobs    *appjobs.Service
}

// Service orchestrates the stock video pipeline: search, download, clip extraction,
// effect overlay, chunk rendering, and Drive upload. All video parameters are read
// from config.Video to ensure consistency with other media pipelines.
//
// PR6 (June 2026) port injection: the Service no longer reaches into the
// ffmpeg.Processor directly. Instead it depends on two canonical ports
// declared in internal/application/assets/providers/stock:
//
//   - stock.VideoCutter  (extracted-clips from a single source video)
//   - stock.StockRenderer (cross-clip concatenation + transition/overlay)
//
// PR-D (June 2026): all dependencies — including the PR6 ports —
// arrive via the ctor-injected Deps struct. The 9 legacy setters
// (SetCutter / SetRenderer / SetClipsRepo / SetAssetIndex / SetDispatcher
// / SetJobsSvc / SetYoutubeService / SetClipIndexer / SetMetadataWriter)
// were removed. Production wire-up lives in WireStockPipeline at
// internal/app/module_sources.go (Deps{...} literal).
type Service struct {
	cfg      *config.Config
	log      *zap.Logger
	publisher delivery.Publisher
	ytdlp    *downloader.YTDLPDownloader
	// cutter + renderer are the PR6 ports. Initialised at ctor time so
	// every method sees either a non-nil port or an error from NewService;
	// the per-site nil-guards the setters previously required are gone.
	cutter      VideoCutter
	renderer    StockRenderer
	pcfg        PipelineConfig
	jobsSvc     *appjobs.Service
	assetIndex  stockAssetIndexUpserter
	youtubeSvc  *youtube.Service
	clipIndexer *clipindexer.Service
	metaWriter  *semantic.MetadataWriter
	clipsRepo   stockClipsSearchTermUpdater
	// dispatcher is the canonical media_index_outbox dispatcher,
	// required at ctor time per QDRANT-002 PR7. NewService rejects
	// nil dispatcher with ErrStockPipelineNilDispatcher.
	// Audit P0 #6: narrowed from `*outbox.Dispatcher` to the local
	// `stockChunkDispatcher` interface so test fakes can wire the
	// shape without dragging in the full infra surface.
	dispatcher stockChunkDispatcher
}

// NewService creates a stock pipeline service via the canonical Deps struct
// (PR-D, June 2026). Returns *Service + error (the legacy signature returned
// only *Service + relied on per-call nil guards; the new contract surfaces
// missing deps at composition time, the only safe window).
//
// Validation order: pure data (Cfg, Log, Drive) → transport (Storage) →
// ports (Media) → cross-cutting. Each missing dep surfaces its own
// sentinel error (see ErrStockPipelineNil* above) so production wiring
// can forward a single error verbatim and tests can assert the precise
// field-name without unwrapping the chain.
//
// PR6 ports (Cutter, Renderer) are required — missing either fails ctor
// with ErrStockPipelineNilCutter / ErrStockPipelineNilRenderer. The
// legacy per-call nil-guards are gone; callers can rely on the
// invariants without re-checking.
//
// Production wire-up lives in WireStockPipeline
// (internal/app/module_sources.go::WireStockPipeline). The composition
// root pre-rejects any nil dispatcher at the wire call-site (QDRANT-002
// PR7 precedent on artlist.WireArtlist); NewService is the second
// line of defence so accidental misuse from tests still fails loud.
func NewService(deps Deps) (*Service, error) {
	if deps.Cfg == nil {
		return nil, ErrStockPipelineNilCfg
	}
	if deps.Log == nil {
		return nil, ErrStockPipelineNilLog
	}
	// F2.10: Drive validation dropped — see Deps doc-comment. The
	// legacy DriveSvc plumbing (driveup.Admin.UploadFile + friend
	// methods) is gone; Publisher is the only Drive-write canal.
	if deps.Storage.ClipsRepo == nil {
		return nil, ErrStockPipelineNilClipsRepo
	}
	if deps.Storage.AssetIndex == nil {
		return nil, ErrStockPipelineNilAssetIndex
	}
	if deps.Storage.Dispatcher == nil {
		return nil, ErrStockPipelineNilDispatcher
	}
	if deps.Media.Cutter == nil {
		return nil, ErrStockPipelineNilCutter
	}
	if deps.Media.Renderer == nil {
		return nil, ErrStockPipelineNilRenderer
	}
	if deps.Media.ClipIndexer == nil {
		return nil, ErrStockPipelineNilClipIndexer
	}
	if deps.Media.MetaWriter == nil {
		return nil, ErrStockPipelineNilMetadataWriter
	}
	// PR-D post-review (Wave 22 §D3 reviewer #2): YouTube and Jobs are
	// required at ctor time. Previously nil-tolerant; the silent
	// nil-passthrough was a regression surface — RegisterHandler(bundle.Jobs)
	// resolves the jobs.JobsFacade at handler dispatch, and processSingleVideo
	// touches youtube metadata for direct-URL sources. Validate them
	// like every other required dep so composition-time pre-rejection
	// catches the missing wiring without waiting for the first job.
	if deps.YouTube == nil {
		return nil, ErrStockPipelineNilYouTube
	}
	if deps.Jobs == nil {
		return nil, ErrStockPipelineNilJobs
	}

	v := deps.Cfg.Video.WithDefaults()
	return &Service{
		cfg:       deps.Cfg,
		log:       deps.Log,
		publisher: deps.Publisher,
		ytdlp:    downloader.NewYTDLP(deps.Cfg),
		cutter:   deps.Media.Cutter,
		renderer: deps.Media.Renderer,
		pcfg: PipelineConfig{
			ChunkDuration:  v.ChunkDuration,
			MaxResults:     v.MaxClipsPerSource,
			EffectInterval: v.EffectInterval,
			EffectsDir:     DefaultPipelineConfig().EffectsDir,
		},
		jobsSvc:     deps.Jobs,
		assetIndex:  deps.Storage.AssetIndex,
		youtubeSvc:  deps.YouTube,
		clipIndexer: deps.Media.ClipIndexer,
		metaWriter:  deps.Media.MetaWriter,
		clipsRepo:   deps.Storage.ClipsRepo,
		dispatcher:  deps.Storage.Dispatcher,
	}, nil
}

// RegisterHandler registers the stock pipeline job handler with the jobs
// system.
//
// Register propagates wiring errors — composition root MUST fail-closed on non-nil return.
//
// P1 #1 (July 2026): wraps appjobs.ErrMissingDeps via %w so the
// composition root + tests can assert via errors.Is(err, appjobs.ErrMissingDeps)
// regardless of which handler-specific prefix the future maintainer
// adds or removes. The handler-specific diagnostic prefix is preserved
// for operator logs. The error-return signature (refactored in
// Audit P0 #2 cont. — PR-VALIDATOR-LITERAL-REGISTER, July 2026)
// closes the silent-success class of "if jobsSvc != nil { log.Info }"
// that pre-P0 #2 swallowed nil-typed-dispatcher + duplicate-bind
// failures.
func (s *Service) RegisterHandler(jobsSvc *appjobs.Service) error {
	if jobsSvc == nil {
		return fmt.Errorf("stockpipeline.Service.RegisterHandler: jobsSvc is nil (composition root must wire jobs.Service before calling Register): %w", appjobs.ErrMissingDeps)
	}
	if err := jobsSvc.RegisterHandler(appjobs.TypeMediaStock, s.HandleJob); err != nil {
		return fmt.Errorf("stockpipeline.Service.RegisterHandler: bind %q to dispatcher: %w", appjobs.TypeMediaStock, err)
	}
	s.log.Info("registered media.stock job handler", zap.String("type", appjobs.TypeMediaStock))
	return nil
}

// HandleJob handles a stock pipeline job from the job queue.
func (s *Service) HandleJob(ctx context.Context, job *appjobs.Job, tools *appjobs.JobTools) (map[string]any, error) {
	var payload StockRunPayload
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return nil, fmt.Errorf("failed to unmarshal stock payload: %w", err)
		}
	}

	s.log.Info("stock job payload received",
		zap.String("job_id", job.ID),
		zap.Int("search_queries", len(payload.SearchQueries)),
		zap.Int("direct_urls", len(payload.DirectURLs)),
		zap.Int("total_minutes", payload.TotalMinutes),
		zap.Int("chunk_duration", payload.ChunkDuration),
		zap.String("subfolder", payload.Subfolder),
		zap.String("folder_name", payload.FolderName),
		zap.String("folder_id", payload.FolderID),
	)

	input := &RunInput{
		SearchQueries: payload.SearchQueries,
		DirectURLs:    payload.DirectURLs,
		TotalMinutes:  payload.TotalMinutes,
		ChunkDuration: payload.ChunkDuration,
		ClipDuration:  payload.ClipDuration,
		NoAudio:       payload.NoAudio,
		NoEffects:     payload.NoEffects,
		NoTransitions: payload.NoTransitions,
		MaxVideos:     payload.MaxVideos,
		Subfolder:     payload.Subfolder,
		FolderName:    payload.FolderName,
		FolderID:      payload.FolderID,
	}
	if payload.Metadata != nil {
		input.Metadata = &ChunkMetadataInput{
			Title:       payload.Metadata.Title,
			Description: payload.Metadata.Description,
			Tags:        payload.Metadata.Tags,
			Category:    payload.Metadata.Category,
			Author:      payload.Metadata.Author,
			Extra:       payload.Metadata.Extra,
		}
	}

	if tools.Progress != nil {
		input.Progress = tools.Progress
		tools.Progress(5, "Starting stock pipeline")
	}

	result, err := s.Run(ctx, input)
	if err != nil {
		return nil, err
	}

	if tools.Progress != nil {
		tools.Progress(100, "Stock pipeline complete")
	}

	return map[string]any{
		"total_clips":      result.TotalClips,
		"total_chunks":     result.TotalChunks,
		"chunks":           result.Chunks,
		"metadata_link":    result.MetadataLink,
		"metadata_file_id": result.MetadataFileID,
	}, nil
}

// RunInput holds the parameters for a stock pipeline run.
type RunInput struct {
	SearchQueries []string
	DirectURLs    []string
	TotalMinutes  int
	MaxVideos     int
	ChunkDuration int
	ClipDuration  int
	NoAudio       bool
	NoEffects     bool
	NoTransitions bool
	Subfolder     string
	FolderName    string
	FolderID      string
	Metadata      *ChunkMetadataInput
	Progress      func(percent int, message string)
}

// ChunkMetadataInput holds user-provided metadata for chunks.
type ChunkMetadataInput struct {
	Title       string            `json:"title,omitempty"`
	Description string            `json:"description,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Category    string            `json:"category,omitempty"`
	Author      string            `json:"author,omitempty"`
	Extra       map[string]string `json:"extra,omitempty"`
}

// PipelineMetadata is the single metadata JSON uploaded at the end with all chunks.
type PipelineMetadata struct {
	Title       string            `json:"title"`
	Description string            `json:"description,omitempty"`
	Source      SourceInfo        `json:"source"`
	Pipeline    PipelineInfo      `json:"pipeline"`
	Tags        []string          `json:"tags,omitempty"`
	Category    string            `json:"category,omitempty"`
	Author      string            `json:"author,omitempty"`
	Extra       map[string]string `json:"extra,omitempty"`
	Chunks      []ChunkMeta       `json:"chunks"`
}

// ChunkMeta describes a single chunk within the pipeline metadata.
type ChunkMeta struct {
	Index         int        `json:"index"`
	TimelineStart float64    `json:"timeline_start"`
	TimelineEnd   float64    `json:"timeline_end"`
	DriveLink     string     `json:"drive_link,omitempty"`
	DownloadLink  string     `json:"download_link,omitempty"`
	Clips         []ClipInfo `json:"clips"`
}

// SourceInfo describes the source video.
type SourceInfo struct {
	URL      string  `json:"url"`
	Title    string  `json:"title,omitempty"`
	Duration float64 `json:"duration_sec,omitempty"`
}

// ClipInfo describes a single clip within a chunk.
type ClipInfo struct {
	Index int    `json:"index"`
	Start string `json:"start"`
	End   string `json:"end"`
	Title string `json:"title,omitempty"`
}

// PipelineInfo describes pipeline settings used.
type PipelineInfo struct {
	ClipDuration  int  `json:"clip_duration"`
	ChunkDuration int  `json:"chunk_duration"`
	NoAudio       bool `json:"no_audio"`
	NoEffects     bool `json:"no_effects"`
	NoTransitions bool `json:"no_transitions"`
}

// PipelineResult holds the results of a stock pipeline run.
type PipelineResult struct {
	SearchTerms    []string      `json:"search_terms"`
	TotalClips     int           `json:"total_clips"`
	TotalChunks    int           `json:"total_chunks"`
	Chunks         []ChunkResult `json:"chunks"`
	MetadataLink   string        `json:"metadata_link,omitempty"`
	MetadataFileID string        `json:"metadata_file_id,omitempty"`
}

// ChunkResult represents a single rendered and uploaded video chunk.
//
// Blocco 1b (July 2026): added Rendered / Uploaded / Indexed outcome
// bools so callers can distinguish which stages completed. Pre-fix
// callers had no way to know whether the chunk's DriveLink was real
// or empty-because-upload-failed.
type ChunkResult struct {
	Index         int      `json:"index"`
	TimelineStart float64  `json:"timeline_start"`
	TimelineEnd   float64  `json:"timeline_end"`
	LocalPath     string   `json:"local_path"`
	DriveLink     string   `json:"drive_link"`
	DownloadLink  string   `json:"download_link"`
	DriveFileID   string   `json:"drive_file_id"`
	Title         string   `json:"title"`
	SourceIDs     []string `json:"source_ids,omitempty"`
	// Rendered is true when the FFmpeg render step completed and the
	// chunk file exists on disk.
	Rendered bool `json:"rendered"`
	// Uploaded is true when Publisher.Publish wrote the chunk to Drive.
	Uploaded bool `json:"uploaded"`
	// Indexed tracks the post-upload indexing lifecycle of the chunk.
	// Audit P0 #6 (July 2026): replaced `bool` with the typed
	// IndexingStatus enum (see types_status.go). The pre-fix bool
	// was both a silent false-positive (assetIndex nil ⇒
	// `indexed := true` default-zero ⇒ verified-by-operator false
	// completion signal) AND a silent false-negative (UpdateSearchTerms
	// failure logged a Warn + continued, letting the outbox dispatch a
	// row with stale tags_norm that the worker cannot backfill).
	//
	// Wire backwards-compat preserved: `IndexingStatus.MarshalJSON`
	// emits `true|false` for the "indexed" JSON field depending on
	// whether the value is `IndexingCompleted`. Operators reading
	// logs / dashboards should pivot on the typed enum internally;
	// external API consumers see no schema change.
	Indexed IndexingStatus `json:"indexed"`
}

// VideoSource represents a single video to be downloaded and processed.
type VideoSource struct {
	URL         string
	Title       string
	Source      string
	DurationSec float64
}

// StagedSource is the result of a lightweight StageSource call.
// It contains only the downloaded file — no render, upload, or indexing.
// The caller owns the file at LocalPath and is responsible for cleanup.
//
// Blocco 2a (July 2026): created to separate the "fetch" contract from
// the full pipeline (render → upload → index). Adapter.Fetch uses this
// instead of Run so the staged file survives the return.
type StagedSource struct {
	LocalPath string
	Bytes     int64
}

// StageSource downloads a video from a URL and returns the staged file.
// It creates a temp directory, downloads via yt-dlp, verifies the output
// file exists and is non-empty, and returns a StagedSource with a Cleanup
// function. Does NOT run the render/upload/index pipeline.
//
// Blocco 2a (July 2026): lightweight alternative to Run for the FetchProvider
// contract. The file is NOT cleaned up until the caller invokes Cleanup().
func (s *Service) StageSource(ctx context.Context, url string) (*StagedSource, error) {
	tempDir, err := os.MkdirTemp(s.cfg.Storage.TempPath(), "stock_stage_")
	if err != nil {
		return nil, fmt.Errorf("stage source: create temp dir: %w", err)
	}

	outputBase := filepath.Join(tempDir, "source")
	outputTemplate := outputBase + ".%(ext)s"

	dlReq := &downloader.DownloadRequest{
		URL:        url,
		OutputPath: outputTemplate,
		NoPlaylist: true,
		Timeout:    10 * time.Minute,
	}

	if err := s.ytdlp.Download(ctx, dlReq); err != nil {
		os.RemoveAll(tempDir)
		return nil, fmt.Errorf("stage source: download %q: %w", url, err)
	}

	// Resolve the actual output file (yt-dlp may change the extension).
	// Uses the canonical downloader.ResolveDownloadedSegmentPath so the
	// stat+size verification stays in one place (Blocco 1c).
	localPath, err := downloader.ResolveDownloadedSegmentPath(outputTemplate)
	if err != nil {
		os.RemoveAll(tempDir)
		return nil, fmt.Errorf("stage source: resolve output file: %w", err)
	}

	fi, statErr := os.Stat(localPath)
	if statErr != nil {
		os.RemoveAll(tempDir)
		return nil, fmt.Errorf("stage source: stat downloaded file %q: %w", localPath, statErr)
	}
	if fi.Size() == 0 {
		os.RemoveAll(tempDir)
		return nil, fmt.Errorf("stage source: downloaded file is empty: %s", localPath)
	}

	s.log.Info("stage source: video downloaded",
		zap.String("url", url),
		zap.String("local_path", localPath),
		zap.Int64("bytes", fi.Size()))

	return &StagedSource{
		LocalPath: localPath,
		Bytes:     fi.Size(),
	}, nil
}
