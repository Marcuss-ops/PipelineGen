// Package stockpipeline — run_orchestrator.go (Stock Cutover, July 2026).
//
// STATO ATTUALE: Service.runOrchestratorResilient è il canonical
// entrypoint per traffico produzione (Service.HandleJob e
// Service.Run via runSyncPersist).
//
// DEPRECATO: projectManifestToPipelineResult proietta il manifesto
// nel legacy *PipelineResult per il ServiceRunner interface.
package stockpipeline

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// runOrchestratorResilient is the canonical production entry point.
// Calls Orchestrator.RunResilient to obtain the *RunSummary that pairs
// the typed *job.ArtifactManifest with the per-run FinalStatus.
//
// STATO ATTUALE: Service.HandleJob (production broker traffic) uses
// this variant so FinalStatus surfaces in the result map.
// Service.runOrchestrator (manifest-only) remains for legacy callers.
//
// Resilience contract: artifacts on Drive + Qdrant OK ⇒ SUCCEEDED;
// artifacts on Drive + Qdrant failed ⇒ INDEX_PENDING;
// manifest-gate failed ⇒ typed sentinel ⇒ JobFailed.
// maxSearchQueryWorkers bounds concurrent provider searches. Search calls are
// network/process heavy, so keep this independent from FFmpeg source-cut
// parallelism and deliberately conservative for the CPU-first worker.
const maxSearchQueryWorkers = 3

// runOrchestratorResilient is the canonical production entry point.
// Calls Orchestrator.RunResilient to obtain the *RunSummary that pairs
// the typed *job.ArtifactManifest with the per-run FinalStatus.
//
// STATO ATTUALE: Service.HandleJob (production broker traffic) uses
// this variant so FinalStatus surfaces in the result map.
// Service.runOrchestrator (manifest-only) remains for legacy callers.
//
// Resilience contract: artifacts on Drive + Qdrant OK ⇒ SUCCEEDED;
// artifacts on Drive + Qdrant failed ⇒ INDEX_PENDING;
// manifest-gate failed ⇒ typed sentinel ⇒ JobFailed.
// searchQueryResolution holds one query's result at its original index. The
// indexed slices let workers write without locks while the caller performs
// deterministic ordered logging, URL deduplication, and error aggregation.
type searchQueryResolution struct {
	sources []VideoSource
	err     error
}

func (s *Service) runOrchestratorResilient(ctx context.Context, input *RunInput, jobID string) (summary *RunSummary, err error) {
	var ownedRun *kernobs.Run
	defer func() {
		if ownedRun != nil {
			ownedRun.FinishWithError(err)
		}
	}()
	if s == nil {
		return nil, fmt.Errorf("stockpipeline.Service.runOrchestratorResilient: nil receiver")
	}
	if input == nil {
		return nil, fmt.Errorf("stockpipeline.Service.runOrchestratorResilient: nil *RunInput")
	}
	if kernobs.FromContext(ctx) == nil {
		// The job runtime normally binds the canonical Run before entering
		// the stock pipeline. Keep direct callers observable without creating
		// a second timer owner when a run is already present.
		ownedRun = kernobs.NewRunObserver(nil).StartRun(ctx, kernobs.RunInfo{JobID: jobID, AttemptID: kernobs.NewAttemptID()})
		ctx = kernobs.WithRun(ctx, ownedRun)
	}
	// drive_folder_id is the operator-selected parent. Resolve the readable
	// folder_name below it once, then publish round subfolders below that
	// resolved folder. This keeps Drive hierarchy creation inside stock.
	if strings.TrimSpace(input.DriveFolderID) != "" && strings.TrimSpace(input.FolderName) != "" {
		if s.folderCreator == nil {
			return nil, fmt.Errorf("stockpipeline.Service.runOrchestratorResilient: folder creator is not wired")
		}
		folderID, folderErr := s.folderCreator.GetOrCreateFolder(ctx, strings.TrimSpace(input.FolderName), strings.TrimSpace(input.DriveFolderID))
		if folderErr != nil {
			return nil, fmt.Errorf("stockpipeline.Service.runOrchestratorResilient: create stock root folder: %w", folderErr)
		}
		input.DriveFolderID = folderID
		input.DriveFolderResolved = true
	} else if strings.TrimSpace(input.DriveFolderID) != "" {
		// An already-resolved destination ID is authoritative. Per-clip
		// naming must not create nested folders below it.
		input.DriveFolderResolved = true
	}

	// Resolve text search queries to YouTube URLs before passing to
	// the orchestrator, which only understands DirectURLs.
	searchInputCount := len(input.SearchQueries)
	searchURLCount := len(input.DirectURLs)
	searchMetric := startServiceStockPhase(ctx, "stock.search", jobID)
	searchErr := s.resolveInputQueries(ctx, input)
	if searchMetric != nil {
		searchMetric.SetItems(int64(searchInputCount), int64(len(input.DirectURLs)-searchURLCount))
		finishServiceStockPhase(s.log, searchMetric, searchErr)
	}
	if searchErr != nil {
		return nil, fmt.Errorf("stockpipeline.Service.runOrchestratorResilient: %w", searchErr)
	}

	cfg := OrchestratorConfig{
		JobId:            jobID,
		Lease:            input.FinalizationLease,
		PolicyVersion:    "v1",
		ChunkDurationSec: effectiveChunkDurationSec(input, s),
		ClipDurationSec:  effectiveClipDurationSec(input, s),
	}
	// Phase 2 (July 2026): wire SQLite-backed step store for
	// crash-resume across process restarts. When db is nil (stock
	// Service routed via imageSvc, WireStockPipeline stubbed), the
	// orchestrator falls back to in-memory (test orchestrator default).
	// PROSSIMO STEP: make DB required when WireStockPipeline is
	// re-enabled.
	if s.stepStore != nil {
		cfg.StepStore = s.stepStore
	}
	planner := NewDeterministicPlanner()
	// Resolve the application-layer stager adapter around the acquisition
	// SourceStager injected by the composition root. This preserves the
	// legacy assets.SourceStager shape required by the orchestrator while
	// keeping Prepare/Release ownership in the canonical acquisition port.
	stager := s.stagerForRun()
	writer := TransactionalAssetWriter(nil)
	if s.dispatcher != nil {
		writer = stockDispatcherWriter{dispatcher: s.dispatcher, termUpdater: s.clipsRepo}
	}
	artifactPreparation := finalization.ArtifactPreparationService(nil)
	if s.publisher != nil {
		artifactPreparation = finalizer.NewArtifactPreparation(s.publisherPort, s.log)
	}
	var o *Orchestrator
	if s.runtimeMode == stockPipelineTestMode {
		// Fixture services are intentionally routed through the fixture
		// constructor. Its in-memory step store and noop resilience ports
		// cannot leak into production because production services take the
		// strict branch below.
		o = NewTestStockOrchestrator(cfg, planner, stager, s.cutter, s.renderer)
	} else {
		var constructErr error
		o, constructErr = NewProductionStockOrchestrator(cfg, ProductionStockPipelineDeps{
			Planner:             planner,
			Stager:              stager,
			Cutter:              s.cutter,
			Renderer:            s.renderer,
			Builder:             stockManifestBuilder{},
			Writer:              writer,
			Projection:          s.projection,
			StepStore:           s.stepStore,
			ArtifactPreparation: artifactPreparation,
			JobFinalizer:        s.finalizer,
			SourceProbe:         s.sourceProbe,
			BatchRepository:     s.batchRepo,
			LocalFS:             s.localFS,
			Logger:              s.log,
		})
		if constructErr != nil {
			return nil, fmt.Errorf("stockpipeline.Service.runOrchestratorResilient: construct production pipeline: %w", constructErr)
		}
	}
	summary, err = o.RunResilient(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("stockpipeline.Service.runOrchestratorResilient: orchestrator.RunResilient: %w", err)
	}
	if s.log != nil {
		s.log.Info("stock orchestrator resilient run succeeded",
			zap.String("job_id", summary.Manifest.JobID),
			zap.String("final_status", string(summary.FinalStatus)),
			zap.Int("artifact_count", len(summary.Manifest.Artifacts)),
		)
	}
	return summary, nil
}

// projectManifestToPipelineResult converts the typed
// *job.ArtifactManifest into the legacy *PipelineResult used by
// pre-cutover callers via the ServiceRunner interface.
//
// Fail-closed (godlike/07 no-fake-availability): the function returns
// ErrStockManifestUnprojectable when the manifest cannot be projected
// into a meaningful result — nil manifest, zero artifacts, or no
// projectable artifacts (no video chunk AND no metadata). This
// prevents the silent-empty class where a SUCCEEDED job surfaced
// total_clips=0/total_chunks=0/chunks=[] despite real uploads.
//
// DEPRECATO: tenere solo per back-compat ServiceRunner.

// runSyncPersist (July 2026) routes ALL sync paths through the
// resilient orchestrator (RunResilient) with a synthetic broker
// lease, so StockFinalizeStep writes to media_assets via the
// single-TX spine. This is the canonical path for both
// persist=true and persist=false sync-mode stock pipeline requests.
//
// godlike/07 no-fake-availability: the synthetic lease uses
// deterministic identifiers (sync-stock-<nanos>) so every call
// produces a distinct lease — the finalizer's CAS-fence won't
// conflate two sync-mode calls that happen to share a jobID.
//
// The §12-1 P0 #2 gate (in Orchestrator.RunResilient) fires
// typed errors when either publisher or finalizer is nil — the
// caller converts those to the ServiceRunner error surface without
// special-casing.
func (s *Service) runSyncPersist(ctx context.Context, input *RunInput) (*PipelineResult, error) {
	// Generate synthetic identifiers for sync-mode persistence.
	// The lease uses deterministic JobID/WorkerID so the finalizer's
	// CAS-fence (revision-match on jobs table) is still meaningful
	// even without a real broker — the sync mode holds the "lease"
	// for the duration of this call; concurrent sync requests get
	// distinct leases and won't CAS-fence each other.
	jobID := fmt.Sprintf("sync-stock-%d", time.Now().UnixNano())
	input.FinalizationLease = finalization.Lease{
		LeaseID:   jobID + "-lease",
		JobID:     jobID,
		WorkerID:  "sync-mode",
		Attempt:   1,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}

	if s.jobCreator == nil {
		return nil, fmt.Errorf("stockpipeline.Service.runSyncPersist: durable job creator is not wired")
	}
	now := time.Now().UTC()
	if err := s.jobCreator.Create(ctx, &job.Job{
		ID: jobID, Type: "media.stock", Status: job.StatusRunning,
		WorkerID:    input.FinalizationLease.WorkerID,
		LeaseID:     input.FinalizationLease.LeaseID,
		LeaseExpiry: &input.FinalizationLease.ExpiresAt,
		CreatedAt:   now, UpdatedAt: now, Revision: 1,
	}); err != nil {
		return nil, fmt.Errorf("stockpipeline.Service.runSyncPersist: insert synthetic job: %w", err)
	}

	// Delegate to the canonical resilient path — runOrchestratorResilient
	// resolves queries, builds the orchestrator with finalizer + asset
	// preparation, and invokes RunResilient. godlike/06 SSOT: the
	// orchestrator construction lives in exactly one method.
	summary, err := s.runOrchestratorResilient(ctx, input, jobID)
	if err != nil {
		return nil, fmt.Errorf("stockpipeline.Service.runSyncPersist: %w", err)
	}

	projected, err := projectManifestToPipelineResult(summary.Manifest)
	if err != nil {
		return nil, fmt.Errorf("stockpipeline.Service.runSyncPersist: %w", err)
	}
	return projected, nil
}

type projectedManifestVideo struct {
	artifact job.Artifact
	position int
	index    int
	hasIndex bool
}

func projectManifestToPipelineResult(manifest *job.ArtifactManifest) (*PipelineResult, error) {
	result := &PipelineResult{}
	if manifest == nil {
		return nil, ErrStockManifestUnprojectable
	}
	if len(manifest.Artifacts) == 0 {
		return nil, fmt.Errorf("%w: manifest %q carries zero artifacts", ErrStockManifestUnprojectable, manifest.JobID)
	}

	// The manifest is the canonical post-publication source. Keep the
	// legacy result deterministic: metadata is identified by kind, while
	// video artifacts are sorted by their explicit chunk index (not by
	// producer append order, which can vary when uploads run concurrently).
	var metadata *job.Artifact
	videoArtifacts := make([]projectedManifestVideo, 0)
	for position := range manifest.Artifacts {
		artifact := manifest.Artifacts[position]
		switch artifact.Kind {
		case job.ArtifactKindMetadata:
			if metadata == nil {
				metadata = &manifest.Artifacts[position]
			}
		case string(finalization.KindVideo):
			index, hasIndex := manifestIntValue(artifact.ArtifactMetadata, "chunk_index")
			videoArtifacts = append(videoArtifacts, projectedManifestVideo{
				artifact: artifact,
				position: position,
				index:    index,
				hasIndex: hasIndex,
			})
		}
	}
	// A manifest that is formally valid but carries neither a metadata
	// artifact nor any video chunk cannot be projected into a meaningful
	// legacy result (no links, no counts, no chunks). Failing closed here
	// keeps the SUCCEEDED-but-empty response class impossible.
	if metadata == nil && len(videoArtifacts) == 0 {
		return nil, fmt.Errorf("%w: manifest %q has %d artifacts but none projectable (no video chunk, no metadata)",
			ErrStockManifestUnprojectable, manifest.JobID, len(manifest.Artifacts))
	}

	hasManifestClipCount := false
	if metadata != nil {
		result.MetadataFileID = firstNonEmpty(
			metadata.RemoteFileID,
			manifestString(metadata.ArtifactMetadata, "drive_file_id"),
			manifestString(metadata.ArtifactMetadata, "file_id"),
		)
		result.MetadataLink = firstNonEmpty(
			metadata.RemoteWebViewLink,
			metadata.RemoteDownloadLink,
			manifestString(metadata.ArtifactMetadata, "drive_link"),
			manifestString(metadata.ArtifactMetadata, "drive_path"),
		)
		result.TotalClips = manifestInt(metadata.ArtifactMetadata, "total_clips")
		hasManifestClipCount = result.TotalClips > 0
	}

	sort.SliceStable(videoArtifacts, func(i, j int) bool {
		left, right := videoArtifacts[i], videoArtifacts[j]
		switch {
		case left.hasIndex && right.hasIndex:
			return left.index < right.index
		case left.hasIndex:
			return true
		case right.hasIndex:
			return false
		default:
			return left.position < right.position
		}
	})

	result.TotalChunks = len(videoArtifacts)
	result.Chunks = make([]ChunkResult, 0, len(videoArtifacts))
	usedChunkIndices := make(map[int]struct{}, len(videoArtifacts))
	for position, projected := range videoArtifacts {
		artifact := projected.artifact
		metadata := artifact.ArtifactMetadata
		index := projected.index
		if !projected.hasIndex {
			// Legacy manifests without chunk_index retain their stable
			// output order and start with their positional index.
			index = position
		}
		// A malformed manifest can contain duplicate explicit indices.
		// Preserve the first sorted artifact's requested index and move
		// later collisions to the next free deterministic index so the
		// legacy DTO never exposes duplicate chunk identities.
		for {
			if _, exists := usedChunkIndices[index]; !exists {
				break
			}
			index++
		}
		usedChunkIndices[index] = struct{}{}
		clipCount := manifestInt(metadata, "clip_count")
		if clipCount <= 0 {
			clipCount = 1
		}
		if !hasManifestClipCount {
			result.TotalClips += clipCount
		}
		driveFileID := firstNonEmpty(
			artifact.RemoteFileID,
			manifestString(metadata, "drive_file_id"),
			manifestString(metadata, "file_id"),
		)
		driveLink := firstNonEmpty(
			artifact.RemoteWebViewLink,
			manifestString(metadata, "drive_link"),
			manifestString(metadata, "drive_path"),
			artifact.RemoteDownloadLink,
		)
		hash := firstNonEmpty(artifact.SHA256, manifestString(metadata, "sha256"))
		uploaded := driveFileID != "" || driveLink != "" || artifact.RemoteDownloadLink != ""
		chunk := ChunkResult{
			Index:         index,
			TimelineStart: manifestFloat(metadata, "start_sec"),
			TimelineEnd:   manifestFloat(metadata, "end_sec"),
			LocalPath:     artifact.Path,
			DriveLink:     driveLink,
			DownloadLink:  artifact.RemoteDownloadLink,
			DriveFileID:   driveFileID,
			SHA256:        hash,
			Title:         manifestString(metadata, "title"), Rendered: artifact.Path != "",
			Uploaded: uploaded,
		}
		chunk.SourceIDs = manifestStringSlice(metadata, "source_ids")
		if len(chunk.SourceIDs) == 0 {
			chunk.SourceIDs = manifestStringSlice(metadata, "source_urls")
		}
		if len(chunk.SourceIDs) == 0 {
			if sourceURL := manifestString(metadata, "source_url"); sourceURL != "" {
				chunk.SourceIDs = []string{sourceURL}
			}
		}
		result.Chunks = append(result.Chunks, chunk)
	}
	if result.TotalClips == 0 {
		// Older manifests did not carry per-artifact clip counts. The
		// current stock pipeline emits one video artifact per clip, so
		// the number of video artifacts is the safe compatibility fallback.
		result.TotalClips = result.TotalChunks
	}
	return result, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func manifestString(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, ok := values[key]
	if !ok {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func manifestStringSlice(values map[string]any, key string) []string {
	if values == nil {
		return nil
	}
	switch typed := values[key].(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, value := range typed {
			if text, ok := value.(string); ok && text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func manifestFloat(values map[string]any, key string) float64 {
	if values == nil {
		return 0
	}
	value, ok := values[key]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int8:
		return float64(typed)
	case int16:
		return float64(typed)
	case int32:
		return float64(typed)
	case int64:
		return float64(typed)
	case uint:
		return float64(typed)
	case uint8:
		return float64(typed)
	case uint16:
		return float64(typed)
	case uint32:
		return float64(typed)
	case uint64:
		return float64(typed)
	default:
		parsed, _ := strconv.ParseFloat(fmt.Sprint(value), 64)
		return parsed
	}
}

func manifestIntValue(values map[string]any, key string) (int, bool) {
	if values == nil {
		return 0, false
	}
	if _, ok := values[key]; !ok {
		return 0, false
	}
	return int(manifestFloat(values, key)), true
}

func manifestInt(values map[string]any, key string) int {
	return int(manifestFloat(values, key))
}

// resolveInputQueries converts text search queries in input.SearchQueries
// to resolved YouTube URLs via s.resolveQuery(), appending them to
// input.DirectURLs. Search calls run through a bounded worker pool, but
// aggregation remains in query order so retries and downstream planning are
// deterministic. URLs are deduplicated by their trimmed first appearance.
//
// A query-level failure logs a warning and does not cancel sibling queries;
// this preserves partial-success behavior. If every query fails, the existing
// typed ErrStockPipelineAllQueriesFailed is returned. Parent context
// cancellation is propagated as-is; provider errors retain the existing
// partial-success behavior and are only fatal when no usable URL remains.
func (s *Service) resolveInputQueries(ctx context.Context, input *RunInput) error {
	if s == nil || input == nil || len(input.SearchQueries) == 0 {
		return nil
	}

	queries := append([]string(nil), input.SearchQueries...)
	results := make([]searchQueryResolution, len(queries))
	workerCount := maxSearchQueryWorkers
	if workerCount > len(queries) {
		workerCount = len(queries)
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan int)
	var wg sync.WaitGroup
	wg.Add(workerCount)
	for worker := 0; worker < workerCount; worker++ {
		go func() {
			defer wg.Done()
			for {
				select {
				case <-workCtx.Done():
					return
				case index, ok := <-jobs:
					if !ok {
						return
					}
					if err := workCtx.Err(); err != nil {
						results[index].err = err
						continue
					}
					sources, err := s.resolveQuery(workCtx, queries[index])
					results[index] = searchQueryResolution{sources: sources, err: err}
				}
			}
		}()
	}

	dispatching := true
	for index := range queries {
		if !dispatching {
			break
		}
		select {
		case jobs <- index:
		case <-workCtx.Done():
			dispatching = false
		}
	}
	close(jobs)
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return err
	}
	total := len(queries)
	failed := 0
	var lastErr error
	seen := make(map[string]struct{}, len(input.DirectURLs))
	directURLs := make([]string, 0, len(input.DirectURLs))
	for _, rawURL := range input.DirectURLs {
		url := strings.TrimSpace(rawURL)
		if url == "" {
			continue
		}
		if _, exists := seen[url]; exists {
			continue
		}
		seen[url] = struct{}{}
		directURLs = append(directURLs, url)
	}
	input.DirectURLs = directURLs
	for index, query := range queries {
		result := results[index]
		if result.err != nil {
			if s.log != nil {
				s.log.Warn("stock: failed to resolve search query, skipping",
					zap.String("query", query), zap.Error(result.err))
			}
			failed++
			lastErr = result.err
			continue
		}

		for _, src := range result.sources {
			url := strings.TrimSpace(src.URL)
			if url == "" {
				continue
			}
			if _, exists := seen[url]; exists {
				continue
			}
			seen[url] = struct{}{}
			input.DirectURLs = append(input.DirectURLs, url)
		}
		if s.log != nil {
			if len(result.sources) > 0 {
				s.log.Info("stock: resolved search query to URLs",
					zap.String("query", query),
					zap.Int("urls", len(result.sources)))
			} else {
				s.log.Warn("stock: search query returned no results",
					zap.String("query", query))
			}
		}
	}

	// Clear resolved queries so the orchestrator doesn't try to use
	// raw text as a URL (firstSource checks SearchQueries after
	// DirectURLs — the resolved URLs are already in DirectURLs).
	input.SearchQueries = nil
	// PR-STOCK-QUERY-RESOLUTION-FAIL-CLOSED (July 2026): when ALL
	// queries fail to resolve, return a typed error instead of
	// silently clearing SearchQueries. Without this, the
	// orchestrator hits the misleading "no sources to plan" error
	// in StockPlanStep.Run instead of surfacing the actual yt-dlp
	// failure (n-challenge, cookies, network).
	if failed > 0 && failed == total && len(input.DirectURLs) == 0 {
		return fmt.Errorf("%w: %d/%d queries failed, last error: %v",
			ErrStockPipelineAllQueriesFailed, failed, total, lastErr)
	}
	return nil
}

// effectiveChunkDurationSec resolves the per-run chunk duration
// (sec) override chain. Mirrors the prior run.go body semantics
// (input.ChunkDuration takes precedence over the runtime config)
// which falls back to the minimal runtime chunk duration).
//
// Centralised here so Service.Run and Service.runOrchestrator
// (and future Commit 4-7 entrypoints) share the same override
// chain without re-deriving it on every call site.
func effectiveChunkDurationSec(input *RunInput, s *Service) int {
	if input != nil && input.ChunkDuration > 0 {
		return input.ChunkDuration
	}
	if s != nil && s.runtime != nil {
		return s.runtime.ChunkDurationSec
	}
	return 0
}

// effectiveClipDurationSec resolves the per-run clip duration
// (sec) override chain. Mirrors the prior run.go body semantics.
// Centralised for the same reason as effectiveChunkDurationSec.
func effectiveClipDurationSec(input *RunInput, s *Service) int {
	if input != nil && input.ClipDuration > 0 {
		return input.ClipDuration
	}
	if s != nil && s.runtime != nil {
		return s.runtime.ClipDurationSec
	}
	return 0
}

// stagerForRun resolves the canonical assets.SourceStager for the
// stock pipeline (Commit 1.2 — Stock Cutover, July 2026).
//
// godlike/06 SSOT: this helper centralises registry construction so
// production wiring has one canonical entry point per run. Today
// the registry carries a single SourceKindExistingCatalog entry
// (StockStager wrapping Service.StageSource — the only SourceStager
// adapter the stock pipeline actually invokes at runtime). Future
// commit waves add YouTube / Artlist / Drive / HTTP / per-source-kind
// dispatch when the orchestrator's stage_sources step gains real
// Stage invocations (currently Begin/Complete only).
//
// nil receiver returns a nil SourceStager; the orchestrator's
// nil-guard handles that case (ErrOrchestratorNilDeps) so the
// production error path is observable.
func (s *Service) stagerForRun() assets.SourceStager {
	if s == nil {
		return nil
	}
	reg := assets.NewSourceStagerRegistry()
	// Existing-catalog path is the only kind the stock pipeline
	// dispatches today. StockStager wraps Service.StageSource
	// (the canonical yt-dlp-backed download path) and satisfies
	// assets.SourceStager via the compile-time assertion at
	// stager_adapter.go:18.
	stockStager := NewStockStager(s).
		WithSourceCache(s.sourceCacheReader, s.sourceCacheWriter).
		WithDownloader(serviceSourceDownloader{service: s})
	if s.driveReader != nil {
		stockStager = stockStager.WithDriveReader(s.driveReader)
	}
	if err := reg.Register(assets.SourceKindExistingCatalog, stockStager); err != nil {
		// godlike/07 typed-error path: log+drop for production;
		// tests assert via the registry's own error sentinels.
		return nil
	}
	resolvedStager, err := reg.Resolve(assets.SourceKindExistingCatalog)
	if err != nil {
		return nil
	}
	return resolvedStager
}
