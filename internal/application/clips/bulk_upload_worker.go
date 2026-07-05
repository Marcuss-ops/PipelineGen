// Package clips (bulk_upload_worker) — port-typed worker that handles the
// bg job "bulk_upload_youtube_clips" for ingesting a folder of local
// .mp4 files into the media pipeline.
//
// Wave 14 PR2 slice (June 2026): caused the handler-side
// internal/api/assets/clips/bulk_upload_worker.go to be removed from
// internal/api/ and re-homed under application/clips where it belongs
// per AGENTS.md Pattern 0 + Pattern 8 (transport handlers are thin;
// business logic lives in the application layer). The worker now
// depends exclusively on the typed ports declared in this package's
// ports.go — concrete adapters wrap *assets.ClipsRepository,
// *drive.Uploader, *config.Config, *clipindexer.Service,
// foldermemory.Service and hashutil.MD5File at the composition root.
//
// P1.7 (July 2026): the per-clip pipeline was extracted into 6 sibling
// files (one section per concern) so this file stays focused on the
// top-level orchestration: struct + ctor + HandleJob + processOneClip
// stitch. The 7 sections of the pipeline are:
//
//  1. worker       — this file (struct + ctor + orchestrator)
//  2. scanner      — bulk_upload_scan_pipeline.go
//  3. clip-pub     — bulk_upload_clip_pub.go
//  4. sidecar-pub  — bulk_upload_sidecar_pub.go
//  5. registration — bulk_upload_registration.go
//  6. enrichment   — bulk_upload_enrichment.go
//  7. result       — bulk_upload_result.go
//
// No new abstractions — only top-level helper functions consumed by
// the processOneClip stitch. The worker struct (7 typed-port fields)
// and constructor signature are unchanged from the pre-split surface.
package clips

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/mutations"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// BulkUploadWorker owns the heavy business logic for the
// "bulk_upload_youtube_clips" job. Construction takes the typed ports
// only; no concrete infrastructure types appear in this file.
//
// PR 7 (June 2026, codex/qdrant-app-writers-fail-closed): the
// `dispatcher` field is the canonical mutations.AssetMutationDispatcher
// SSOT. Required so the worker's media_assets UPSERT step routes
// through the canonical outbox+tx writer (QDRANT-002 atomicity
// invariant).
type BulkUploadWorker struct {
	publisher  ClipPublisherPort
	repo       ClipRepositoryPort
	indexer    ClipIndexerPort
	hasher     ClipHashPort
	cfg        ClipConfigPort
	dispatcher mutations.AssetMutationDispatcher
	log        *zap.Logger
}

// NewBulkUploadWorker constructs the canonical worker. All port
// arguments are required. Returns a *BulkUploadWorker (never nil).
func NewBulkUploadWorker(
	publisher ClipPublisherPort,
	repo ClipRepositoryPort,
	indexer ClipIndexerPort,
	hasher ClipHashPort,
	cfg ClipConfigPort,
	dispatcher mutations.AssetMutationDispatcher,
	log *zap.Logger,
) *BulkUploadWorker {
	if log == nil {
		log = zap.NewNop()
	}
	return &BulkUploadWorker{
		publisher:  publisher,
		repo:       repo,
		indexer:    indexer,
		hasher:     hasher,
		cfg:        cfg,
		dispatcher: dispatcher,
		log:        log,
	}
}

// HandleJob is the registered handler for
// "bulk_upload_youtube_clips". Wired by the API layer's
// Handler.RegisterJobHandlers (which delegates into this worker).
//
// P1.7 (July 2026): the per-clip pipeline calls into the 6 sibling
// files (publishClip + publishSidecars + registerClip + enrichClip +
// finalizeJobResult) instead of inlining them. The orchestrating
// flow is preserved verbatim — only the body of processOneClip
// changed (now stages helpers in sequence).
func (w *BulkUploadWorker) HandleJob(ctx context.Context, j *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	if w.cfg == nil {
		return nil, fmt.Errorf("bulk upload worker: cfg not configured (ClipConfigPort is nil — production wiring must supply non-nil cfg)")
	}
	jobTimeout := w.cfg.JobTimeout(appjobs.TypeBulUploadYouTubeClips)
	if jobTimeout <= 0 {
		return nil, fmt.Errorf("bulk upload worker: job timeout for %q resolved to %v (non-positive — check registry configuration)", appjobs.TypeBulUploadYouTubeClips, jobTimeout)
	}
	ctx, cancel := context.WithTimeout(ctx, jobTimeout)
	defer cancel()

	payload := &appjobs.BulkUploadYouTubeClipsPayload{}
	if err := json.Unmarshal(j.Payload, payload); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if w == nil {
		return nil, fmt.Errorf("bulk upload worker not configured (nil receiver)")
	}
	log := w.log.With(zap.String("job_id", j.ID), zap.String("handler", "bulk-upload-worker"))

	log.Info("bulk upload job started",
		zap.String("local_folder", payload.LocalFolder),
		zap.String("drive_folder_id", payload.DriveFolderID),
		zap.Int("concurrency", payload.Concurrency))

	if strings.TrimSpace(payload.LocalFolder) == "" {
		return nil, fmt.Errorf("local_folder is required")
	}
	if w.publisher == nil {
		return nil, fmt.Errorf("drive publisher not configured (P0.1: Publisher mandatory, ClipDriveUploaderPort removed)")
	}
	if w.repo == nil {
		return nil, fmt.Errorf("clips repository not configured")
	}
	if payload.DriveFolderID == "" {
		return nil, fmt.Errorf("drive_folder_id is required (enqueue path should have resolved it)")
	}

	cancelled := func() bool {
		if tools == nil || tools.IsCancelled == nil {
			return false
		}
		return tools.IsCancelled()
	}

	if tools != nil && tools.Progress != nil {
		tools.Progress(2, fmt.Sprintf("Scanning %s", payload.LocalFolder))
	}
	candidates, err := scanLocalClips(payload.LocalFolder, payload.Recursive, payload.FilePatterns, payload.SkipPatterns, payload.Limit)
	if err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}
	if len(candidates) == 0 {
		if tools != nil && tools.Progress != nil {
			tools.Progress(100, "No clips found")
		}
		return map[string]any{
			"total": 0, "uploaded": 0, "skipped": 0, "failed": 0,
			"message": "no clips matching patterns in local_folder",
		}, nil
	}

	total := len(candidates)
	log.Info("found clips", zap.Int("total", total))
	if tools != nil && tools.Progress != nil {
		tools.Progress(5, fmt.Sprintf("Found %d clips, starting pipeline", total))
	}

	// P0.1 (July 2026): Publisher is mandatory; subdir folder
	// resolution is handled internally by delivery.Publisher
	// via the Group and RootFolderOverride fields. Legacy
	// resolveSubdirFolderID closure removed.

	concurrency := payload.Concurrency
	if concurrency <= 0 {
		concurrency = 2
	}
	sem := make(chan struct{}, concurrency)
	var (
		wg            sync.WaitGroup
		uploaded      atomic.Int64
		indexed       atomic.Int64
		pushed        atomic.Int64
		skipped       atomic.Int64
		failed        atomic.Int64
		failedDetails []string
		failedMu      sync.Mutex
	)

	reportProgress := func(force bool) {
		if tools == nil || tools.Progress == nil {
			return
		}
		done := uploaded.Load() + skipped.Load() + failed.Load()
		pct := int(float64(done) / float64(total) * 95.0)
		if pct < 5 {
			pct = 5
		}
		if pct > 100 {
			pct = 100
		}
		tools.Progress(pct, fmt.Sprintf("Processed %d/%d (uploaded=%d indexed=%d qdrant=%d failed=%d)",
			done, total, uploaded.Load(), indexed.Load(), pushed.Load(), failed.Load()))
	}

	for i := range candidates {
		if cancelled() {
			log.Warn("job cancelled by caller", zap.Int("remaining", total-i))
			break
		}
		cand := candidates[i]
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			defer reportProgress(false)

			if err := w.processOneClip(ctx, payload, cand,
				&uploaded, &indexed, &pushed, &skipped, &failed, log); err != nil {
				failedMu.Lock()
				failedDetails = append(failedDetails, fmt.Sprintf("%s: %v", cand.LocalPath, err))
				if len(failedDetails) > 50 {
					failedDetails = failedDetails[len(failedDetails)-50:]
				}
				failedMu.Unlock()
				log.Warn("clip processing failed",
					zap.String("path", cand.LocalPath),
					zap.Error(err))
			}
		}()
	}
	wg.Wait()
	reportProgress(true)

	result := finalizeJobResult(total, &uploaded, &indexed, &pushed, &skipped, &failed, failedDetails, payload)
	log.Info("bulk upload job complete", zap.Any("result", result))
	return result, nil
}

// processOneClip stitches the per-clip pipeline: config check →
// hash + clipID → publish-clip (skip-upload gate) → sidecar-publish
// (skip-upload gate) → register (clip build + dispatcher
// EnqueueAndIndex) → enrich (skip-embeddings gate).
//
// P1.7 (July 2026): the steps are extracted into top-level helpers
// in 6 sibling files. This method only sequences them + manages
// per-clip counter book-keeping.
//
// Latent counters preserved verbatim from pre-split (forward-pointer
// for a future hardening wave, NOT changed in P1.7):
//   - "skipped" is never incremented (always 0 in the report)
//   - "pushed" is never incremented (always 0 in the report)
//
// Both counters are kept on the report schema for back-compat
// with downstream reconciliation + dashboard consumers; promoting
// them to real metrics is a separate change. Function parameters
// in Go don't require suppressors, so `skipped` / `pushed` are
// passed through to finalizeJobResult where they are loaded.
func (w *BulkUploadWorker) processOneClip(
	ctx context.Context,
	payload *appjobs.BulkUploadYouTubeClipsPayload,
	cand clipCandidate,
	uploaded, indexed, pushed, skipped, failed *atomic.Int64,
	log *zap.Logger,
) error {
	if w == nil || w.hasher == nil || w.publisher == nil || w.repo == nil {
		failed.Add(1)
		return fmt.Errorf("bulk upload not configured correctly (P0.1: Publisher mandatory)")
	}

	// Step 1: hash + clipID (stays in worker.go — these are the
	// per-clip log-bookkeeping fields; the scan-pipeline file
	// owns the folder-walk helpers only).
	fileHash, err := w.hasher.MD5File(cand.LocalPath)
	if err != nil {
		failed.Add(1)
		return fmt.Errorf("hash: %w", err)
	}
	clipID := buildBulkClipID(cand, fileHash)
	log = log.With(zap.String("clip_id", clipID), zap.String("path", cand.LocalPath))

	// Step 2-3: publish clip via Publisher (clip-pub section).
	var pubRes *delivery.PublishResult
	targetFolderID := payload.DriveFolderID
	if !payload.SkipUpload {
		var pubErr error
		pubRes, pubErr = publishClip(ctx, w.publisher, payload, cand, fileHash, log)
		if pubErr != nil {
			failed.Add(1)
			return pubErr
		}
		uploaded.Add(1)
		targetFolderID = pubRes.FolderID
		log.Info("published to drive",
			zap.String("file_id", pubRes.FileID),
			zap.String("drive_link", pubRes.WebViewLink),
			zap.String("publish_action", string(pubRes.Action)))
		// Step 3b: best-effort sidecar publishes (errors logged
		// but not bubbled — pre-split silently dropped sidecar
		// errors; P1.7 preserves silent-drop semantics by NOT
		// bumping any counter from publishSidecars). The added
		// `log.Warn` on sidecar publish failure is a hygiene
		// improvement: original code had no observability at all
		// — operator dashboards would never see sidecar drift.
		publishSidecars(ctx, w.publisher, cand, targetFolderID, log)
	}

	// Step 4 + 5: build clip Asset + dispatcher.EnqueueAndIndex
	// (registration section). Strict fail-closed on nil
	// dispatcher — surfaces explicitly via returned error.
	if err := registerClip(ctx, w.dispatcher, payload, cand, pubRes, fileHash, targetFolderID, log); err != nil {
		failed.Add(1)
		return err
	}
	log.Info("saved clip to DB", zap.String("clip_id", clipID))

	// Step 6: enrichment (transcript staging + IndexClip), only
	// when embeddings are not skipped (preserve pre-split gate).
	if !payload.SkipEmbeddings {
		if didIndex := enrichClip(ctx, w.cfg, w.indexer, cand, clipID, log); didIndex {
			indexed.Add(1)
		}
	}
	return nil
}
