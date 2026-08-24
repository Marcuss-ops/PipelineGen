// Package clips (bulk_upload_worker) — orchestrator for the
// "bulk_upload_youtube_clips" bg job. The per-clip pipeline is split across
// 7 sibling files (one section per concern: scan, hash, clip-pub, sidecar,
// register, enrich, result); this file owns the struct, constructor,
// HandleJob entry, and the processOneClip stitch.
//
// Counter semantics (canonical results map, set by finalizeJobResult):
//   - committed   — dispatcher.EnqueueAndIndex succeeded (asset row +
//     outbox event in one tx)
//   - failed      — any stage in processOneClip returned error
package clips

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/mutations"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/queue"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// BulkUploadWorker owns the heavy business logic. Typed ports only.
type BulkUploadWorker struct {
	publisher  ClipPublisherPort
	repo       ClipRepositoryPort
	hasher     ClipHashPort
	cfg        ClipConfigPort
	dispatcher mutations.AssetMutationDispatcher
	log        *zap.Logger
}

func NewBulkUploadWorker(
	publisher ClipPublisherPort,
	repo ClipRepositoryPort,
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
		hasher:     hasher,
		cfg:        cfg,
		dispatcher: dispatcher,
		log:        log,
	}
}

func (w *BulkUploadWorker) HandleJob(ctx context.Context, j *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	if w.cfg == nil {
		return nil, fmt.Errorf("bulk upload worker: cfg not configured")
	}
	jobTimeout := w.cfg.JobTimeout(JobBulkUpload)
	if jobTimeout <= 0 {
		return nil, fmt.Errorf("bulk upload worker: job timeout for %q resolved to %v (non-positive — check registry)", JobBulkUpload, jobTimeout)
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
		zap.String("drive_folder_id", payload.DriveFolderID))

	if tools != nil && tools.Event != nil {
		tools.Event("job_started", "bulk upload job started", map[string]any{
			"local_folder":    payload.LocalFolder,
			"drive_folder_id": payload.DriveFolderID,
		})
	}

	if strings.TrimSpace(payload.LocalFolder) == "" {
		return nil, fmt.Errorf("local_folder is required")
	}
	if w.publisher == nil {
		return nil, fmt.Errorf("drive publisher not configured")
	}
	if w.repo == nil {
		return nil, fmt.Errorf("clips repository not configured")
	}
	if payload.DriveFolderID == "" {
		return nil, fmt.Errorf("drive_folder_id is required (enqueue path should have resolved it)")
	}

	if tools != nil && tools.Progress != nil {
		tools.Progress(2, fmt.Sprintf("Scanning %s", payload.LocalFolder))
	}
	if tools != nil && tools.Event != nil {
		tools.Event("scan_started", fmt.Sprintf("Scanning %s", payload.LocalFolder), map[string]any{
			"local_folder": payload.LocalFolder,
		})
	}
	recursive := payload.Recursive
	if !recursive && payload.Concurrency > 0 {
		// shallow scan requested explicitly; concurrency default handled below
	}
	concurrency := payload.Concurrency
	if concurrency <= 0 {
		concurrency = 2
	}
	candidates, err := scanLocalClips(payload.LocalFolder, recursive, nil, nil, 0)
	if err != nil {
		if tools != nil && tools.Event != nil {
			tools.Event("error", fmt.Sprintf("scan failed: %v", err), map[string]any{
				"local_folder": payload.LocalFolder,
				"error":        err.Error(),
			})
		}
		return nil, fmt.Errorf("scan: %w", err)
	}
	if len(candidates) == 0 {
		if tools != nil && tools.Progress != nil {
			tools.Progress(100, "No clips found")
		}
		return map[string]any{
			"total":     0,
			"committed": 0,
			"failed":    0,
			"message":   "no clips in local_folder",
		}, nil
	}

	total := len(candidates)
	log.Info("found clips", zap.Int("total", total))
	if tools != nil && tools.Event != nil {
		tools.Event("scan_complete", fmt.Sprintf("Found %d clips", total), map[string]any{
			"total": total,
		})
	}
	if tools != nil && tools.Progress != nil {
		tools.Progress(5, fmt.Sprintf("Found %d clips, starting pipeline", total))
	}

	sem := make(chan struct{}, concurrency)
	var (
		wg            sync.WaitGroup
		committed     atomic.Int64
		failed        atomic.Int64
		failedDetails []string
		failedMu      sync.Mutex
	)

	reportProgress := func(force bool) {
		if tools == nil || tools.Progress == nil {
			return
		}
		done := committed.Load() + failed.Load()
		pct := int(float64(done) / float64(total) * 95.0)
		if pct < 5 {
			pct = 5
		}
		if pct > 100 {
			pct = 100
		}
		tools.Progress(pct, fmt.Sprintf("Processed %d/%d (committed=%d failed=%d)",
			done, total, committed.Load(), failed.Load()))
	}

	for i := range candidates {
		if ctx.Err() != nil {
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
				&committed, &failed, log, tools); err != nil {
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

	result := finalizeJobResult(total, &committed, &failed, failedDetails, payload)
	log.Info("bulk upload job complete", zap.Any("result", result))
	if tools != nil && tools.Event != nil {
		tools.Event("completed", "bulk upload job completed", map[string]any{
			"total":     total,
			"committed": committed.Load(),
			"failed":    failed.Load(),
		})
	}
	return result, nil
}

// processOneClip stitches the per-clip pipeline: config check → hash +
// clipID → publish clip + sidecars → register (asset build + dispatcher
// EnqueueAndIndex) → enrich.
func (w *BulkUploadWorker) processOneClip(
	ctx context.Context,
	payload *appjobs.BulkUploadYouTubeClipsPayload,
	cand clipCandidate,
	committed, failed *atomic.Int64,
	log *zap.Logger,
	tools *appjobs.JobTools,
) error {
	if w == nil || w.hasher == nil || w.publisher == nil || w.repo == nil {
		failed.Add(1)
		return fmt.Errorf("bulk upload not configured correctly")
	}

	fileHash, err := w.hasher.MD5File(cand.LocalPath)
	if err != nil {
		failed.Add(1)
		if tools != nil && tools.Event != nil {
			tools.Event("error", fmt.Sprintf("hash failed for %s", cand.LocalPath), map[string]any{
				"local_path": cand.LocalPath,
				"error":      err.Error(),
			})
		}
		return fmt.Errorf("hash: %w", err)
	}
	clipID := buildBulkClipID(cand, fileHash)
	log = log.With(zap.String("clip_id", clipID), zap.String("path", cand.LocalPath))
	if tools != nil && tools.Event != nil {
		tools.Event("hash_computed", fmt.Sprintf("Computed hash for %s", cand.LocalPath), map[string]any{
			"clip_id":         clipID,
			"legacy_file_md5": fileHash,
		})
	}

	pubRes, pubErr := publishClip(ctx, w.publisher, payload, cand, fileHash, log)
	if pubErr != nil {
		failed.Add(1)
		if tools != nil && tools.Event != nil {
			tools.Event("error", fmt.Sprintf("drive upload failed for %s", cand.LocalPath), map[string]any{
				"local_path": cand.LocalPath,
				"error":      pubErr.Error(),
			})
		}
		return pubErr
	}
	targetFolderID := pubRes.FolderID
	log.Info("published to drive",
		zap.String("file_id", pubRes.FileID),
		zap.String("drive_link", pubRes.WebViewLink),
		zap.String("publish_action", string(pubRes.Action)))
	if tools != nil && tools.Event != nil {
		tools.Event("drive_upload", fmt.Sprintf("Uploaded %s to Drive", cand.LocalPath), map[string]any{
			"clip_id":    clipID,
			"file_id":    pubRes.FileID,
			"drive_link": pubRes.WebViewLink,
		})
	}
	publishSidecars(ctx, w.publisher, cand, targetFolderID, log)

	if err := registerClip(ctx, w.dispatcher, payload, cand, pubRes, fileHash, targetFolderID, log); err != nil {
		failed.Add(1)
		if tools != nil && tools.Event != nil {
			tools.Event("error", fmt.Sprintf("db register failed for %s", cand.LocalPath), map[string]any{
				"local_path": cand.LocalPath,
				"error":      err.Error(),
			})
		}
		return err
	}
	committed.Add(1)
	log.Info("saved clip to DB", zap.String("clip_id", clipID))
	if tools != nil && tools.Event != nil {
		tools.Event("db_register", fmt.Sprintf("Registered %s in DB", clipID), map[string]any{
			"clip_id": clipID,
		})
	}

	enrichClip(w.cfg, cand, log)
	return nil
}
