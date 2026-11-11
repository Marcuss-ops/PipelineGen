package wiring

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/artifacts"
	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockplan"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	stockbatches "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/stockbatches"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/clips/aistock"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	opsapp "github.com/Marcuss-ops/PipelineGen/internal/capabilities/operations"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	api "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	ollamaclient "github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assetindex"
	sqlitejobs "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobs"
	sqliteops "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/operations"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)
import (
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	"strings"
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
	StockPrefetcher    scriptports.StockPrefetcher
}

// sourcingHashAdapter was DELETED here on 2026-09-20, together with its
// sourcing.HashPort port (internal/capabilities/assets/sourcing/ports.go).
// The adapter was never constructed anywhere in the tree and HashPort had zero
// consumers — no field, parameter or return value was typed by it. The
// canonical MD5 surface is clips.ClipHashPort, implemented by the clips wiring
// adapter and consumed by clips.BulkUploadWorker.hasher. This preserves one
// owner for the checksum port.

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
	// asset_index carries ONE content-address slot (content_hash); the
	// compatibility bucket is gone (migration 281), so the canonical value
	// wins and the legacy spelling is only a fallback for unpromoted rows.
	contentHash := rec.ContentHash
	if contentHash == "" {
		contentHash = rec.ContentSHA256
	}
	return a.service.Upsert(ctx, &assetindex.AssetRecord{
		AssetID: rec.AssetID, AssetType: rec.AssetType, Source: rec.Source, SourceID: rec.SourceID,
		GroupName: rec.GroupName, Subfolder: rec.Subfolder, LocalPath: rec.LocalPath,
		DriveLink: rec.DriveLink, DownloadLink: rec.DownloadLink,
		ContentHash: contentHash, Status: rec.Status, Metadata: rec.Metadata,
		CreatedAt: rec.CreatedAt, UpdatedAt: rec.UpdatedAt,
	})
}

var _ artifacts.AssetIndexPort = (*artifactAssetIndexAdapter)(nil)

// POSTGRES-MEDIA-CUTOVER (2026-09-12): the `media.reindex` job binding is
// REMOVED. It was the last reachable path from the job broker into the retired
// clipindexer batch reindex (clipindexer.Service.RegisterJobHandler →
// jobmedia.TypeReindex → HandleJob → IndexClip). The canonical media index
// plane is the PostgreSQL outbox: AssetCommitter emits asset.index.requested,
// PostgresIndexWorker drains it (embed → pgvector). Reindex is requested
// through media_reindex-free surfaces (operator reindex API + backfill CLI),
// both of which enqueue the same canonical event.

// Package app — images job handler late-binding (extracted from
// composition.go NewComposition per PG-028 capability split, July 2026).
// wireImagesJobBinding registers the image generation handler into
// jobs.Service. Extracted from NewComposition per PG-028.
func wireImagesJobBinding(domains *DomainBundle, jobs *JobsBundle) error {
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
func appendImagesCriticalValidator(domains *DomainBundle, jobs *JobsBundle, validators *[]CriticalHandler) {
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
// Bridges production concretes under internal/platform/* to the
// 9 canonical narrow ports declared in
// internal/capabilities/voiceover/ports.go. Per AGENTS.md Pattern 0
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
// See internal/capabilities/voiceover/ports.go for the canonical 9-port
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
	return m.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
}

func buildScriptSubmissionService(root *ComposeRoot, log *zap.Logger) (*opsapp.Service, error) {
	if root == nil || root.DB == nil || root.Jobs == nil || root.Jobs.Repo == nil || root.Outbox == nil || root.Outbox.EventsRepo == nil {
		return nil, fmt.Errorf("script submission: required runtime dependencies are nil")
	}
	// operations + jobs + outbox_events form one atomic submission unit and
	// therefore must use the same execution-plane database connection.
	if root.Jobs.DB == nil || root.Jobs.DB.DB == nil {
		return nil, fmt.Errorf("script submission: jobs database is not configured")
	}
	opsRepo := sqliteops.NewSQLiteRepository(root.Jobs.DB.DB)
	txMgr := &sqliteTxManager{db: root.Jobs.DB.DB}
	svc := opsapp.NewService(opsRepo, root.Jobs.Repo, root.Jobs.Repo, root.Outbox.EventsRepo, txMgr, log)
	svc.SetKeyedLocker(newOpsKeyedLockerAdapter())
	return svc, nil
}

func newOpsKeyedLockerAdapter() opsapp.KeyedLocker {
	return opsapp.NewKeyedLocker()
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

// CleanupFunc is the type returned by initialization functions for teardown.
type CleanupFunc func()

// databases is the composition-root view of `storage.DatabaseSet`.
// Exists only to keep the consumer-facing API of composition.go stable
// (every Build*Bundle() takes `*Databases`); the inner state delegates
// to the canonical DatabaseSet opened by `storage.OpenSet` (rule: no
// `sql.Open` outside `internal/platform/sqlite/**`).
//
// `main` and `logs` fields are kept for back-compat with the dozens of
// `dbs.Main.<X>` references in `composition.go` / `shutdown.go` /
// `registry.go` / `dependencies.go`. They are populated from the
// DatabaseSet at construction time; the canonical source of truth is
// `dbs.Set.Primary` / `dbs.Set.Observability`.
//
// Jobs is a separate execution-plane database when the canonical split is
// enabled; Main remains the media control-plane database and Logs remains the
// observability axis.
//
// Compatibility field population rules (godlike/06 SSOT):
//   - dbs.Set: always non-nil after a successful OpenSet.
//   - dbs.Main: dbs.Set.Primary.
//   - dbs.Logs: dbs.Set.Observability.
//   - dbs.DualPool: a true reader/writer pool over the canonical primary file.
//   - dbs.Jobs: non-nil only when the explicit jobs split is enabled.
type Databases struct {
	Set      *storage.DatabaseSet
	Main     *storage.SQLiteDB
	Logs     *storage.SQLiteDB
	Cache    *storage.SQLiteDB
	DualPool *storage.DualPool

	// Jobs is the execution-plane handle when the canonical split is enabled;
	// it remains nil only for explicit rollback/staged deployments.
	Jobs *storage.SQLiteDB
}

func (d *Databases) Close() {
	// Close the compatibility adapter before DatabaseSet. Attached adapters do
	// not own the primary handle; DatabaseSet remains the sole owner.
	if d.DualPool != nil {
		_ = d.DualPool.Close()
	}
	if d.Set != nil {
		_ = d.Set.Close()
	}
}

// InitDatabases opens the primary + observability DBs and, when enabled, the
// jobs + cache planes via the
// canonical `storage.OpenSet` (codex/db-set-and-paths). No `sql.Open`
// remains outside `internal/platform/sqlite/**`.
//
// Bundle constructors may still consume the compatibility DualPool shape,
// but it is attached to the DatabaseSet primary below. No alternative
// primary opening is allowed here.
//
// The jobs.db.sqlite split is opt-in and opens an independent execution-plane
// database. It is never used for media transactions or cross-DB atomicity.
func InitDatabases(ctx context.Context, cfg *config.Config, log *zap.Logger) (*Databases, error) {
	if cfg == nil {
		return nil, fmt.Errorf("init databases: config is nil")
	}
	if strings.TrimSpace(cfg.Jobs.JobsDBPath) != "" && !cfg.Jobs.SplitDBEnabled {
		return nil, fmt.Errorf("init databases: jobs_db_path requires jobs.split_db_enabled=true")
	}

	setCfg := storage.StorageConfig{
		DataDir:             cfg.Storage.DataDir,
		ObservabilityDBPath: cfg.Storage.ObservabilityDBFullPath(),
		CacheDBPath:         cfg.Storage.CacheDBFullPath(),
		WorkspaceDir:        cfg.Storage.WorkspaceDir,
		CacheDir:            cfg.Storage.CacheDir,
		ExportDir:           cfg.Storage.ExportDir,
	}
	if cfg.Jobs.SplitDBEnabled {
		setCfg.JobsDBPath = cfg.Storage.JobsDBFullPath()
		if strings.TrimSpace(cfg.Jobs.JobsDBPath) != "" {
			setCfg.JobsDBPath = cfg.Jobs.JobsDBPath
		}
	}
	set, err := storage.OpenSet(setCfg, log)
	if err != nil {
		return nil, fmt.Errorf("init databases: %w", err)
	}
	dbs := &Databases{
		Set:   set,
		Main:  set.Primary,
		Logs:  set.Observability,
		Cache: set.Cache,
		Jobs:  set.Jobs,
	}

	// Transitional bundle constructors consume the reader/writer-shaped field.
	// Open a real reader pool over the same media file; unlike AttachDualPool,
	// this prevents reads from queueing behind the primary's single connection.
	dualPool, err := storage.NewDualPool(ctx, set.Primary.Path(), cfg.Storage.SQLiteReaderCount())
	if err != nil {
		dbs.Close()
		return nil, fmt.Errorf("init databases: open media dual pool: %w", err)
	}
	dbs.DualPool = dualPool

	return dbs, nil
}

// ValidateControlPlaneIdentity verifies the durable identity of the primary
// control plane and the configured single-writer topology. The observability
// database is intentionally excluded: it is an operational log store, not a
// control-plane writer. A split jobs database is a second writable control
// plane and therefore fails closed until it is assigned a non-writer role by
// an explicit migration/cutover.
func (d *Databases) ValidateControlPlaneIdentity(ctx context.Context) error {
	if d == nil || d.Main == nil || d.Main.DB == nil {
		return fmt.Errorf("control plane identity: primary database is not configured")
	}
	meta, err := storage.ReadControlPlaneMeta(ctx, d.Main.DB)
	if err != nil {
		return err
	}
	if meta.InstanceRole != storage.ControlPlaneRoleCanonical {
		return fmt.Errorf("control plane identity: primary database_id=%q has role %q, want %q", meta.DatabaseID, meta.InstanceRole, storage.ControlPlaneRoleCanonical)
	}

	databases := []storage.ConfiguredDatabase{{
		Name:         "primary",
		Path:         d.Main.Path(),
		Role:         meta.InstanceRole,
		Writable:     true,
		ControlPlane: true,
	}}
	if d.Logs != nil {
		// Observability is a separately writable operational log store,
		// not a control-plane writer. Keeping it in the inventory makes
		// that boundary explicit instead of silently omitting a known DB.
		databases = append(databases, storage.ConfiguredDatabase{
			Name:         "observability",
			Path:         d.Logs.Path(),
			Role:         storage.ControlPlaneRoleReadOnly,
			Writable:     true,
			ControlPlane: false,
		})
	}
	if d.Jobs != nil {
		databases = append(databases, storage.ConfiguredDatabase{
			Name:         "jobs",
			Path:         d.Jobs.Path(),
			Role:         storage.ControlPlaneRoleReadOnly,
			Writable:     true,
			ControlPlane: false,
		})
	}
	if err := storage.ValidateConfiguredControlPlaneWriters(databases); err != nil {
		return err
	}
	return nil
}

func RunAllMigrations(dbs *Databases, log *zap.Logger) error {
	if err := dbs.Set.Migrate(log); err != nil {
		return err
	}
	if err := dbs.ValidateControlPlaneIdentity(context.Background()); err != nil {
		return fmt.Errorf("validate control plane identity: %w", err)
	}
	return nil
}
