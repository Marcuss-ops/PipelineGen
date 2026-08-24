package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	api "github.com/Marcuss-ops/PipelineGen/internal/api"
	stockbatches "github.com/Marcuss-ops/PipelineGen/internal/api/assets/stockbatches"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
	"github.com/Marcuss-ops/PipelineGen/internal/application/clips/aistock"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	opsapp "github.com/Marcuss-ops/PipelineGen/internal/application/operations"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/application/search"
	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockplan"
	ollamaclient "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	sqlitejobs "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/jobs"
	sqliteops "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/operations"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/checksum"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"io"
)

// registryCrossStepState carries dependencies produced by the internal-module
// phase and consumed by later registration phases. It is intentionally a
// short-lived value, not part of RegistryWiring: RegistryWiring is the final
// graph result returned to the server, while this value represents the
// dependency edges used during graph construction.
type registryCrossStepState struct {
	SearchFanOut       search.SearchFanOut
	SearchBackends     *search.BackendRegistry
	SearchAggregator   *search.Aggregator
	IdempotencyHandler gin.HandlerFunc
}

// Package app — sourcing hash adapter
// split from youtube_metadata_adapter.go (PR-GODOBJ-Azione-4, July 2026).
//
// 1 adapter: sourcingHashAdapter.
// ── sourcingHashAdapter ───────────────────────────────────────────────

type sourcingHashAdapter struct{}

func (a *sourcingHashAdapter) MD5File(path string) (string, error) {
	return checksum.LegacyMD5File(path)
}

var _ sourcing.HashPort = (*sourcingHashAdapter)(nil)

// searxngWebSearchProviderAdapter wraps the existing Ollama WebSearcher
// (backed by SearXNG) as a WebSearchProvider for the multi-provider
// registry. It satisfies scriptports.WebSearchProvider.
type searxngWebSearchProviderAdapter struct{ searcher *ollamaclient.WebSearcher }

func (a *searxngWebSearchProviderAdapter) Name() string { return "searxng" }

func (a *searxngWebSearchProviderAdapter) Search(ctx context.Context, query string, limit int) ([]scriptports.WebSearchHit, error) {
	if a == nil || a.searcher == nil {
		return nil, nil
	}
	results, err := a.searcher.Search(ctx, query)
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	out := make([]scriptports.WebSearchHit, 0, len(results))
	for _, result := range results {
		out = append(out, scriptports.WebSearchHit{Title: result.Title, URL: result.URL, Content: result.Content})
	}
	return out, nil
}

// Compile-time assertion.
var _ scriptports.WebSearchProvider = (*searxngWebSearchProviderAdapter)(nil)

// Package app — build_stage_drive_bundle.go
//
// Canonical typed sentinel for the Stage→Drive forward-pointer components.
//
// The StageDriveBundle struct and BuildStageDriveBundle func were designed
// as a canonical composition surface for the COMPLETION-CUTOVER-P0 wave.
// They were removed (July 2026) per YAGNI: zero external callers existed
// in production code; the forward-pointer components (ArtifactPreparation +
// WithArtifactsService) have independent composition paths via their own
// build_bundles_* files.
//
// The sentinel below is retained for backward compatibility with
// lifecycle_capability_disabled_test.go (sentinel-identity probe).
// ErrStageDriveInsufficientForCompletion is the typed sentinel for the
// forward-pointer components (P0-COMPL-4-PUBLISH-DEDUPE + P0-COMPL-5-
// SINGLE-BACKBONE). The companion StageDriveBundle struct and typed-nil-
// safe accessors were removed per YAGNI (zero production callers).
//
// P0-COMPL-4-PUBLISH-DEDUPE shipped (commit ca73476d, 2026-07-03).
// P0-COMPL-5-SINGLE-BACKBONE deadline: 2026-08-15.
var ErrStageDriveInsufficientForCompletion = errors.New(
	"stage-drive bundle: forward-pointer components (ArtifactPreparation + WithArtifactsService) require P0-COMPL-5-SINGLE-BACKBONE (deadline 2026-08-15) to ship",
)

type artifactAssetIndexAdapter struct{ service *assetindex.Service }

func newArtifactAssetIndexAdapter(service *assetindex.Service) artifacts.AssetIndexPort {
	if service == nil {
		return nil
	}
	return &artifactAssetIndexAdapter{service: service}
}

func (a *artifactAssetIndexAdapter) Upsert(ctx context.Context, rec *artifacts.AssetIndexRecord) error {
	if a == nil || a.service == nil {
		return errors.New("artifacts: asset index adapter unavailable")
	}
	if rec == nil {
		return errors.New("artifacts: asset index record is nil")
	}
	return a.service.Upsert(ctx, &assetindex.AssetRecord{
		AssetID: rec.AssetID, AssetType: rec.AssetType, Source: rec.Source, SourceID: rec.SourceID,
		GroupName: rec.GroupName, Subfolder: rec.Subfolder, LocalPath: rec.LocalPath,
		DriveLink: rec.DriveLink, DownloadLink: rec.DownloadLink, LegacyFileMD5: rec.LegacyFileMD5,
		ContentHash: rec.ContentHash, Status: rec.Status, Metadata: rec.Metadata,
		CreatedAt: rec.CreatedAt, UpdatedAt: rec.UpdatedAt,
	})
}

var _ artifacts.AssetIndexPort = (*artifactAssetIndexAdapter)(nil)

// Package app — clipindexer job handler late-binding.
// wireClipIndexerJobBinding registers the media_reindex handler into
// jobs.Service.
func wireClipIndexerJobBinding(process *wiring.ProcessBundle, jobs *wiring.JobsBundle) error {
	if process.ClipIndexerService != nil && jobs.Service != nil {
		if err := process.ClipIndexerService.RegisterJobHandler(jobs.Service); err != nil {
			return fmt.Errorf("clipindexer.media_reindex: %w", err)
		}
	}
	return nil
}

// appendClipIndexerCriticalValidator populates the critical-handler
// validators slice with the clipindexer.media_reindex binding.
func appendClipIndexerCriticalValidator(process *wiring.ProcessBundle, jobs *wiring.JobsBundle, validators *[]CriticalHandler) {
	if process.ClipIndexerService != nil && jobs.Service != nil {
		ci := process.ClipIndexerService
		*validators = append(*validators,
			CriticalHandler{
				Name: "clipindexer.media_reindex",
				Bind: func(svc *appjobs.Service) error {
					return ci.RegisterJobHandler(svc)
				},
			},
		)
	}
}

// Package app — images job handler late-binding (extracted from
// composition.go NewComposition per PG-028 capability split, July 2026).
// wireImagesJobBinding registers the image generation handler into
// jobs.Service. Extracted from NewComposition per PG-028.
func wireImagesJobBinding(domains *wiring.DomainBundle, jobs *wiring.JobsBundle) error {
	if domains.ImageService != nil && jobs.Service != nil {
		if err := domains.ImageService.RegisterHandler(jobs.Service); err != nil {
			return fmt.Errorf("images.image_generate_google: %w", err)
		}
	}
	return nil
}

// appendImagesCriticalValidator populates the critical-handler validators
// slice with the images.image_generate_google binding.
// Extracted from NewComposition per PG-028.
func appendImagesCriticalValidator(domains *wiring.DomainBundle, jobs *wiring.JobsBundle, validators *[]CriticalHandler) {
	if domains.ImageService != nil && jobs.Service != nil {
		img := domains.ImageService
		*validators = append(*validators,
			CriticalHandler{
				Name: "images.image_generate_google",
				Bind: func(svc *appjobs.Service) error {
					return img.RegisterHandler(svc)
				},
			},
		)
	}
}

// aistockDriveReaderAdapter adapts drive.Reader to aistock.DriveReaderPort.
type aistockDriveReaderAdapter struct {
	reader drive.Reader
}

// Compile-time assertion.
var _ aistock.DriveReaderPort = (*aistockDriveReaderAdapter)(nil)

func newAistockDriveReaderAdapter(reader drive.Reader) aistock.DriveReaderPort {
	if reader == nil {
		return nil
	}
	return &aistockDriveReaderAdapter{reader: reader}
}

func (a *aistockDriveReaderAdapter) DownloadFile(ctx context.Context, fileID string) (body io.ReadCloser, contentType string, err error) {
	return a.reader.DownloadFile(ctx, fileID)
}

func (a *aistockDriveReaderAdapter) GetFileMeta(ctx context.Context, fileID string) (*aistock.DriveFileMeta, error) {
	meta, err := a.reader.GetFileMeta(ctx, fileID)
	if err != nil {
		return nil, err
	}
	if meta == nil {
		return nil, nil
	}
	return &aistock.DriveFileMeta{Name: meta.Name}, nil
}

// Package app — voiceover use case adapters orchestrator
// (PR-VO-ADAPTERS-SPLIT, July 2026).
//
// Bridges production concretes under internal/infrastructure/* to the
// 9 canonical narrow ports declared in
// internal/application/voiceover/ports.go. Per AGENTS.md Pattern 0
// (port abstraction layer, June 2026) each adapter is a thin
// bridge; production wiring lives here, NOT inside the voiceover
// package, so voiceover stays free of *infrastructure and *lifecycle
// imports.
//
// File split (godlike/06 one-canonical-owner-per-fact):
//
//   - adapters_voiceover_use_case.go   → this file (orchestrator landmark: package doc only)
//   - adapters_voiceover_tts.go        → TTSProvider + AudioPostProcessor (AUDIO synthesis cluster)
//   - adapters_voiceover_publisher.go  → VoiceoverPublisher (DRIVE cluster)
//   - adapters_voiceover_repo.go       → VoiceoverRepository + DestinationResolver +
//     VoiceoverDefaultFolderResolver (REPO/RESOLVER cluster;
//     sole canonical owner of heavy *sql.DB / *sql.Tx /
//     BeginTx / ExecContext use per godlike/06 SSOT)
//   - adapters_voiceover_projection.go → LifecycleProjectionUpserter + VoiceoverPostCommitVerifier
//     (FINALIZATION sidecars; imports `database/sql` for the
//     *sql.Tx parameter type required by the canonical port
//     signatures — forward-pointer PR-VO-ADAPTERS-TYPED-PORT
//     abstracts this once a typed envelope lands)
//
// Compile-time pin convention (godlike/06 SSOT): each adapter struct
// on the right side declares `var _ voiceover.<Port> = (*<AdapterStruct>)(nil)`
// in its capability file so drift between the adapter signature and
// the port contract surfaces as a compile error at the file where the
// adapter lives, NOT at the use case Execute call site.
//
// See internal/application/voiceover/ports.go for the canonical 9-port
// surface layout (TTSProvider, AudioPostProcessor, VoiceoverPublisher,
// VoiceoverRepository, DestinationResolver, VoiceoverDefaultFolderResolver,
// LifecycleProjectionUpserter, VoiceoverPostCommitVerifier).
//
// Production wiring sites: internal/app/build_bundles_voiceover.go
// (BuildVoiceoverBundle + VoiceoverUseCaseDeps construction).

// build_stock_batches.go — Gate 5 of BuildStockBundle: the stock
// batch coordinator + /stock-batches module construction. Extracted
// so the BuildStockBundle orchestrator stays a thin gate dispatcher.
// buildStockBatchModule constructs the stock batch coordinator and the
// /stock-batches module. Returns (nil, nil) when no BatchRepository is
// wired (backcompat/test mode — the batch surface is simply absent).
// Error wrapping follows the BuildStockBundle preamble convention
// (`stock.BuildStockBundle: stockbatches.Build: %w`).
func buildStockBatchModule(deps StockBundleDeps, svc *stockpipeline.Service) (api.Module, error) {
	var batchModule api.Module
	if deps.Acquisition.BatchRepository != nil {
		coordinator := stockplan.NewCoordinator(stockplan.CoordinatorDeps{
			Repo:     deps.Acquisition.BatchRepository,
			Enqueuer: deps.Orchestration.Jobs,
			Resolver: nil,
			Stager:   svc,
			Log:      deps.Runtime.Log,
		})
		batchDescriptor, batchErr := stockbatches.Build(stockbatches.Dependencies{
			Coordinator: coordinator,
			EnabledFunc: deps.Feature.StockPipelineEnabled,
			Logger:      deps.Runtime.Log,
		})
		if batchErr != nil {
			return nil, fmt.Errorf("stock.BuildStockBundle: stockbatches.Build: %w", batchErr)
		}
		if d, ok := batchDescriptor.(*stockbatches.StockBatchesDescriptor); ok && d != nil {
			batchModule = d.Module
		}
	}
	return batchModule, nil
}

type sqliteTxManager struct {
	db *sql.DB
}

func (m *sqliteTxManager) BeginTx(ctx context.Context) (*sql.Tx, error) {
	if m == nil || m.db == nil {
		return nil, fmt.Errorf("script submission: tx manager not wired")
	}
	return m.db.BeginTx(ctx, nil)
}

func buildScriptSubmissionService(root *wiring.ComposeRoot, log *zap.Logger) (*opsapp.Service, error) {
	if root == nil || root.DB == nil || root.Jobs == nil || root.Jobs.Repo == nil || root.Outbox == nil || root.Outbox.EventsRepo == nil {
		return nil, fmt.Errorf("script submission: required runtime dependencies are nil")
	}
	opsRepo := sqliteops.NewSQLiteRepository(root.DB.DB)
	txMgr := &sqliteTxManager{db: root.DB.DB}
	// FASE 2 close-out: jobsStore satisfies JobGetter natively
	// (its Get(ctx, id) method matches the port shape). Wired
	// twice — once as JobEnqueuer (CreateInTx use) and once as
	// JobGetter (canonical-state-on-replay read on the HTTP 202
	// idempotency-hit path).
	return opsapp.NewService(opsRepo, root.Jobs.Repo, root.Jobs.Repo, root.Outbox.EventsRepo, txMgr, log), nil
}

// Compile-time assertion: *sqlitejobs.SQLiteStore implements
// BOTH the submission service's JobEnqueuer port AND the
// JobGetter port. Drift in either surface is a build failure,
// not a runtime panic (godlike/06 Pattern 0).
var (
	_ opsapp.TxManager     = (*sqliteTxManager)(nil)
	_ opsapp.JobEnqueuer   = (*sqlitejobs.SQLiteStore)(nil)
	_ opsapp.JobGetter     = (*sqlitejobs.SQLiteStore)(nil)
	_ opsapp.OutboxEmitter = (*outboxevents.Repository)(nil)
)
