// build_stock_deps.go — StockBundleDeps and the 8 purpose-grouped
// sub-bundle Deps structs (PR-NEST-FLAT-DEPS-STOCK), plus the
// godlike/07 symmetric production gate.
//
// godlike/06 SSOT: the Deps structs mirror stockpipeline.Deps
// field-for-field where the fields live canonically (Publisher + Finalizer
// are top-level fields at service_types.go). The 3 flat-typed
// sub-Deps (`SourceStager`, `Cutter`, `Renderer`) collapse StorageDeps +
// MediaDeps into the bundle deps shape so the caller's literal is
// flat (calls adding a typed field never need to wrap into the
// stockpipeline.Deps sub-struct). ChannelLister is optional per
// §F.1 governance — when nil, query.go's resolveQuery fails-closed at
// first search.
package app

import (
	"context"
	"database/sql"
	"io"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	stockenrich "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/enrichment"
	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	stocksteps "github.com/Marcuss-ops/PipelineGen/internal/application/execution/steps"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	assetindex "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// StockBundleDeps is the typed narrow input to BuildStockBundle (PR-2 of
// PR-STOCK-ATLASTORCH-DISPATCH).
//
// godlike/06 SSOT: the Deps struct mirrors stockpipeline.Deps
// field-for-field where the fields live canonically (Publisher + Finalizer
// are top-level fields at service_types.go). The 3 flat-typed
// sub-Deps (`SourceStager`, `Cutter`, `Renderer`) collapse StorageDeps +
// MediaDeps into the bundle deps shape so the caller's literal is
// flat (calls adding a typed field never need to wrap into the
// stockpipeline.Deps sub-struct). ChannelLister is optional per
// §F.1 governance — when nil, query.go's resolveQuery fails-closed at
// first search.
//
// PR-NEST-FLAT-DEPS-STOCK (July 2026): the previous flat shape had
// 14 mandatory fields, tripping the `max_struct_deps=8` archcheck
// gate (warn-severity struct_deps violation). The struct now nests
// the 14 fields into 7 purpose-grouped sub-bundles (each ≤4 fields,
// all ≤8):
//
//   - Runtime         (3): Cfg, Log, DB — runtime environment.
//   - Delivery        (2): Publisher, Finalizer — the production pair.
//   - Acquisition     (4): SourceStager, ClipsRepo, AssetIndex,
//     Dispatcher — the storage + dispatch layer.
//   - Media           (2): Cutter, Renderer — the ffmpeg-mediated
//     media processing layer.
//   - Orchestration   (2): Jobs, ChannelLister — the dispatcher-side
//     control layer.
//   - Feature         (1): StockPipelineEnabled — the capability
//     gate closure.
//   - Enrichment      (3): EnrichmentLLMClient, EnrichmentEnabled,
//     EnrichmentEmitter — the PR-011A/B/C RLM/LLM
//     enrichment pass surface.
//
// StockBundleDeps itself carries 7 sub-bundle fields → 7 fields, well
// below the 8-field cap. The nesting follows the canonical godlike/06
// SSOT pattern established by PR-NEST-FLAT-DEPS-ARLIST
// (build_bundles_artlist.go::ServiceDeps{ServicePorts + ServiceDependencies}).
//
// Mandatory fields return an error when BuildStockBundle is called with
// nil; optional fields fall through to the existing type's nil-tolerance
// (Publisher + Finalizer + DB + ChannelLister are optional per
// stockpipeline.NewService's lenient gate — the symmetric gate above
// adds the load-bearing pairing check on Publisher/DepositFinalizer).
type StockBundleDeps struct {
	Runtime       StockRuntimeDeps
	Delivery      StockDeliveryDeps
	Acquisition   StockAcquisitionDeps
	Media         StockMediaDeps
	Orchestration StockOrchestrationDeps
	Feature       StockFeatureGate
	Enrichment    StockEnrichmentDeps
	SourceCache   StockSourceCacheDeps
}

// StockSourceCacheDeps groups the cross-run source download cache
// ports. Reader and Writer are OPTIONAL — nil means no cache
// (every download is fresh). LocalFS is the Pattern 0 typed port
// (PR-REFACTOR-P0-IO-BINDER) the application layer uses to read,
// write, and stat cached files; it is injected at composition time
// so the application layer never calls os.* directly.
// Field count: 3.
type StockSourceCacheDeps struct {
	Reader  stockpipeline.SourceCacheReader
	Writer  stockpipeline.SourceCacheWriter
	LocalFS stockpipeline.LocalFSPort
}

// StockRuntimeDeps groups the runtime environment the stock bundle
// needs (Cfg, Log, DB). Field count: 3.
type StockRuntimeDeps struct {
	Cfg        *config.Config
	Log        *zap.Logger
	DB         *sql.DB // optional (nil → in-memory)
	JobCreator stockpipeline.JobCreator
	StepStore  stocksteps.Store
}

// StockDeliveryDeps groups the asymmetric production-pair surface
// (Publisher, PublisherPort, Finalizer). The StockSymmetricGate validates
// this pair is both-nil or both-non-nil. PublisherPort is the pre-constructed
// finalization.PublisherPort adapter (drive.NewArtifactPublisherAdapter)
// created at the composition root so the application layer stays free of
// internal/infrastructure/drive imports. Field count: 3.
type StockDeliveryDeps struct {
	Publisher     delivery.Publisher         // optional (nil → backcompat; finalizer nil → OK)
	PublisherPort finalization.PublisherPort // optional (nil → backcompat; constructed from Publisher at composition root)
	Finalizer     finalization.JobFinalizer  // optional (nil → backcompat OR asymmetric gate fires when Publisher non-nil)
}

// stockConcreteDriveReader is the raw interface matched by the concrete
// drive types (*drive.Uploader, drive.Reader). Defined in the composition
// root so the application layer's DriveReaderPort can stay free of
// internal/infrastructure/drive imports. chooseDriveReader wraps the
// concrete with a stockDriveReaderAdapter that converts
// drive.DriveFileInfo → stockpipeline.DriveFileInfo.
type stockConcreteDriveReader interface {
	DownloadFile(ctx context.Context, fileID string) (io.ReadCloser, string, error)
	ListFiles(ctx context.Context, parentID string) ([]drive.DriveFileInfo, error)
}

// StockAcquisitionDeps groups the storage + dispatch layer the stock
// pipeline reads from (SourceStager, ClipsRepo, AssetIndex,
// Dispatcher, BatchRepository, DriveDownloader). Field count: 6.
type StockAcquisitionDeps struct {
	SourceStager    acquisition.SourceStager           // required
	ClipsRepo       *sqassets.ClipsRepository          // required
	AssetIndex      *assetindex.Service                // required
	Dispatcher      *outbox.Dispatcher                 // required
	BatchRepository stockpipeline.StockBatchRepository // optional; required in production via DB gate
	// DriveDownloader enables staging of Google Drive source URLs.
	// Optional — nil means Drive URLs fail with a typed error (no
	// silent fallback to yt-dlp). Wraps a concrete drive type.
	//
	// Deprecated: DriveReader is the canonical field going forward.
	// DriveDownloader is still accepted for backward compatibility.
	DriveDownloader stockConcreteDriveReader
	// DriveReader enables staging of Google Drive source URLs,
	// including folder expansion. Optional — nil means Drive URLs
	// fail with a typed error.
	DriveReader stockConcreteDriveReader
}

// StockMediaDeps groups the ffmpeg-mediated media processing layer
// (Cutter, Renderer). Field count: 2.
type StockMediaDeps struct {
	Cutter   stockpipeline.VideoCutter   // required
	Renderer stockpipeline.StockRenderer // required
}

// StockOrchestrationDeps groups the dispatcher-side control layer
// (Jobs, ChannelLister). ChannelLister is optional — when nil,
// query.go's resolveQuery fails-closed at first search. Field
// count: 2.
type StockOrchestrationDeps struct {
	Jobs          *appjobs.Service                 // required
	ChannelLister stockpipeline.ChannelLister      // optional
	FolderCreator stockpipeline.StockFolderCreator // optional
}

// StockFeatureGate is the canonical closure that decides whether
// /api/stock-pipeline/* routes are mounted.
// Field count: 1.
type StockFeatureGate struct {
	StockPipelineEnabled func() bool
}

// StockEnrichmentDeps groups the PR-011A/B/C RLM/LLM enrichment pass
// surface (LLMClient, Enabled closure, Emitter). All fields are
// OPTIONAL (nil = enrichment disabled, godlike/07 fail-closed).
// When EnrichmentEnabled() returns true AND the LLMClient is
// resolved (override > real adapter > stub), BuildStockBundle wires
// the canonical EnrichmentHandler
// (stockenrich.EnrichmentHandler) and registers it on the jobs
// dispatcher for appjobs.TypeMediaStockRLMEnrich
// ("media.stock_rlm_enrich"). Field count: 3.
type StockEnrichmentDeps struct {
	// EnrichmentLLMClient is the canonical Pattern-0 typed port.
	// PR-011A passes the stockenrich.StubEnrichmentLLMClient
	// (returns ErrEnrichmentLLMUnavailable, drives the worker
	// retry path end-to-end). PR-011B replaces the stub with a
	// real ollama-backed adapter.
	EnrichmentLLMClient stockenrich.EnrichmentLLMClient

	// EnrichmentEnabled is the canonical cfg-gated closure
	// (mirrors StockPipelineEnabled). When nil or returning
	// false, no handler is registered.
	EnrichmentEnabled func() bool

	// EnrichmentEmitter is the canonical Pattern-0 typed port for
	// the asset.published v1 outbox event (PR-011C). OPTIONAL
	// (nil = disabled-mode wiring; the handler's godlike/07
	// nil-tolerance logs a Warn and skips the emit step).
	EnrichmentEmitter stockenrich.AssetPublishedEmitter
}

// validateStockSymmetricGate enforces the godlike/07 production pairing
// of Publisher + JobFinalizer. The 4 states:
//
//	publisher=nil + finalizer=nil → nil (test/backcompat mode)
//	publisher≠nil + finalizer≠nil → nil (production mode)
//	publisher≠nil + finalizer=nil → ErrStockProductionJobFinalizerMissing
//	publisher=nil + finalizer≠nil → ErrStockProductionArtifactPrepMissing
//
// Pre-gate (before NewService): composition-time typed error surfaces
// loudly instead of silently passing through the orchestrator's
// RunResilient gate at orchestrator.go:478/480 (which fires AFTER
// source staging + cut dispatch — much later in the pipeline, harder
// to diagnose from incident reports).
//
// Exposed at package scope as unexported helper so build_bundles_stock_test.go
// can drive TDD coverage without standing up all 14 StockBundleDeps fields.
func validateStockSymmetricGate(publisher delivery.Publisher, finalizer finalization.JobFinalizer) error {
	if publisher != nil && finalizer == nil {
		return stockpipeline.ErrStockProductionJobFinalizerMissing
	}
	if publisher == nil && finalizer != nil {
		return stockpipeline.ErrStockProductionArtifactPrepMissing
	}
	return nil
}
