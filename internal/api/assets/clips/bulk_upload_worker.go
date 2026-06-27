package clips

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
)

// HandleBulkUploadYouTubeClipsJob is the worker entry point. Wired up by
// RegisterJobHandlers (called from NewHandler).
func (h *Handler) HandleBulkUploadYouTubeClipsJob(ctx context.Context, j *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	// Job-level deadline so abandoned jobs can't sit half-done forever.
	// Worker ctx only times out on shutdown, which leaves orphans otherwise.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()

	payload := &appjobs.BulkUploadYouTubeClipsPayload{}
	if err := json.Unmarshal(j.Payload, payload); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	log := h.log.With(zap.String("job_id", j.ID), zap.String("handler", "bulk-upload-worker"))

	log.Info("bulk upload job started",
		zap.String("local_folder", payload.LocalFolder),
		zap.String("drive_folder_id", payload.DriveFolderID),
		zap.Int("concurrency", payload.Concurrency))

	if strings.TrimSpace(payload.LocalFolder) == "" {
		return nil, fmt.Errorf("local_folder is required")
	}
	if h.driveUploader == nil {
		return nil, fmt.Errorf("drive uploader not configured")
	}
	if h.clipsRepo == nil {
		return nil, fmt.Errorf("clips repository not configured")
	}
	if payload.DriveFolderID == "" {
		return nil, fmt.Errorf("drive_folder_id is required (enqueue path should have resolved it)")
	}

	// Honour cancellation
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

	// Resolve subdir folder cache (Drive folder ID per subdir)
	// Use mutex to prevent two concurrent clips sharing a subdir from both
	// creating duplicate folders on Drive (the previous sync.Map-only design
	// had a TOCTOU race: A loads miss, B loads miss, A creates + stores,
	// B creates another folder + overwrites A's ID in the cache).
	subdirFolderCache := sync.Map{}
	var subdirMu sync.Mutex

	resolveSubdirFolderID := func(ctx context.Context, subdir string) (string, error) {
		if subdir == "" || !payload.SubdirAsDriveSubdir {
			return payload.DriveFolderID, nil
		}
		if v, ok := subdirFolderCache.Load(subdir); ok {
			return v.(string), nil
		}
		subdirMu.Lock()
		defer subdirMu.Unlock()
		// Re-check after acquiring the lock
		if v, ok := subdirFolderCache.Load(subdir); ok {
			return v.(string), nil
		}
		id, err := h.driveUploader.GetOrCreateFolder(ctx, subdir, payload.DriveFolderID)
		if err != nil {
			return "", err
		}
		subdirFolderCache.Store(subdir, id)
		return id, nil
	}

	// Worker pool — the HTTP handler already caps `payload.Concurrency` at 8
	// (single source of truth), so we trust the payload here.
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

			if err := h.processOneClip(ctx, payload, cand, resolveSubdirFolderID,
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

	result := map[string]any{
		"total":         total,
		"uploaded":      uploaded.Load(),
		"indexed":       indexed.Load(),
		"qdrant_pushed": pushed.Load(),
		"skipped":       skipped.Load(),
		"failed":        failed.Load(),
		"local_folder":  payload.LocalFolder,
		"drive_folder":  payload.DriveFolderID,
	}
	if len(failedDetails) > 0 {
		result["failures"] = failedDetails
	}
	log.Info("bulk upload job complete", zap.Any("result", result))
	return result, nil
}

// processOneClip handles a single .mp4 through the pipeline.
// Steps (skip flags short-circuit downstream steps):
//  1. Compute MD5 hash + check dedup by local_path in DB
//  2. Upload to Drive (unless skip_upload)
//  3. Upload siblings (manifest.json, transcript.txt) to same Drive folder
//  4. Create / update MediaAsset record
//  5. Generate embeddings via Python server (unless skip_embeddings)
//  6. Push to Qdrant (handled automatically by clipIndexer.IndexClip unless skip_qdrant)
func (h *Handler) processOneClip(
	ctx context.Context,
	payload *appjobs.BulkUploadYouTubeClipsPayload,
	cand clipCandidate,
	resolveSubdirFolderID func(ctx context.Context, subdir string) (string, error),
	uploaded, indexed, pushed, skipped, failed *atomic.Int64,
	log *zap.Logger,
) error {
	// Step 0: dedup is handled implicitly by the deterministic clip ID
	// (ytlocal_{HASH8}_{slug}) and UpsertClip. Re-running the endpoint on
	// the same folder will update existing records rather than duplicate them.
	// The previous GetClip(localPath) approach was broken because GetClip
	// looks up by primary key (clip ID), not by local_path.

	// Step 1: hash
	fileHash, err := hashutil.MD5File(cand.LocalPath)
	if err != nil {
		failed.Add(1)
		return fmt.Errorf("hash: %w", err)
	}

	clipID := buildBulkClipID(cand, fileHash)
	log = log.With(zap.String("clip_id", clipID), zap.String("path", cand.LocalPath))

	// Step 2: Drive target folder (with subdir if requested)
	targetFolderID := payload.DriveFolderID
	if payload.SubdirAsDriveSubdir && cand.Subdir != "" && cand.Subdir != "." {
		id, ferr := resolveSubdirFolderID(ctx, cand.Subdir)
		if ferr != nil {
			failed.Add(1)
			return fmt.Errorf("resolve subdir folder %q: %w", cand.Subdir, ferr)
		}
		targetFolderID = id
	}

	var (
		driveFileID  string
		driveLink    string
		downloadLink string
	)
	if !payload.SkipUpload {
		// Build Drive filename: use subdir (actor name) if available, else clip name
		driveName := ""
		if cand.Subdir != "" && cand.Subdir != "." {
			driveName = sanitiseDriveName(filepath.Base(cand.Subdir))
		}
		if driveName == "" {
			driveName = sanitiseDriveName(cand.DisplayName())
		}
		if driveName == "" {
			driveName = cand.Name
		}
		driveFilename := driveName + ".mp4"

		driveDesc := buildBulkDriveDescription(cand, fileHash, *payload)
		upRes, err := h.driveUploader.UploadFileWithDescription(ctx, cand.LocalPath, targetFolderID, driveFilename, driveDesc)
		if err != nil {
			failed.Add(1)
			return fmt.Errorf("drive upload: %w", err)
		}
		driveFileID = upRes.FileID
		driveLink = upRes.WebViewLink
		downloadLink = upRes.DownloadLink
		uploaded.Add(1)
		log.Info("uploaded to drive",
			zap.String("file_id", driveFileID),
			zap.String("drive_link", driveLink))

		// Step 3: upload siblings (best effort)
		dir := filepath.Dir(cand.LocalPath)
		baseNoExt := strings.TrimSuffix(filepath.Base(cand.LocalPath), filepath.Ext(cand.LocalPath))
		// clip_manifest.json
		manifestPath := filepath.Join(dir, "clip_manifest.json")
		if _, err := os.Stat(manifestPath); err == nil {
			mdesc := "Clip manifest for " + baseNoExt
			if _, e := h.driveUploader.UploadFileWithDescription(ctx, manifestPath, targetFolderID, baseNoExt+".clip_manifest.json", mdesc); e != nil {
				log.Warn("manifest.json upload failed (non-fatal)", zap.Error(e))
			}
		}
		// transcript.txt (sibling .txt or named transcript.txt)
		for _, tp := range []string{filepath.Join(dir, baseNoExt+".txt"), filepath.Join(dir, "transcript.txt")} {
			if _, err := os.Stat(tp); err == nil {
				tdesc := "Whisper transcript for " + baseNoExt
				if _, e := h.driveUploader.UploadFileWithDescription(ctx, tp, targetFolderID, baseNoExt+".transcript.txt", tdesc); e != nil {
					log.Warn("transcript.txt upload failed (non-fatal)", zap.Error(e))
				}
				break
			}
		}
	}

	// Step 4: create / update MediaAsset record
	now := time.Now().UTC()
	source := payload.Source
	if source == "" {
		source = "youtube-local"
	}
	category := payload.Category
	if category == "" && cand.Subdir != "" && cand.Subdir != "." {
		// use first subdir level as a category hint (e.g. comedy/anna-faris -> comedy)
		category = strings.SplitN(cand.Subdir, "/", 2)[0]
	}

	clip := &asset.Asset{
		ID:             clipID,
		Name:           cand.DisplayName(),
		Filename:       filepath.Base(cand.LocalPath),
		Source:         asset.Source(source),
		Category:       category,
		MediaType:      asset.MediaType("video"),
		SearchText:     deriveSearchText(cand),
		LifecycleState: asset.StateReady,
		Duration:       time.Duration(extractIntFromManifest(cand.Manifest, "duration_sec")) * time.Second,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	clip.SetLocalPath(cand.LocalPath)
	clip.SetFileHash(fileHash)
	clip.SetFolderID(targetFolderID)
	clip.SetFolderPath(cand.Subdir)
	// Store the YouTube ID and source URL if the manifest has them
	if cand.Manifest != nil {
		if v, ok := cand.Manifest["youtube_video_id"].(string); ok && v != "" {
			clip.SetMetadataString("youtube_video_id", v)
		} else if v, ok := cand.Manifest["youtube_id"].(string); ok && v != "" {
			clip.SetMetadataString("youtube_video_id", v)
		}
		if v, ok := cand.Manifest["youtube_url"].(string); ok && v != "" {
			clip.SetMetadataString("youtube_url", v)
		} else if v, ok := cand.Manifest["url"].(string); ok && v != "" {
			clip.SetMetadataString("youtube_url", v)
		}
	}
	if v, ok := cand.Manifest["tags"].([]any); ok {
		for _, t := range v {
			if s, ok := t.(string); ok && s != "" {
				clip.Tags = append(clip.Tags, s)
			}
		}
	}
	if driveFileID != "" {
		clip.SetDriveLink(driveLink)
		clip.SetDownloadLink(downloadLink)
		clip.SetDriveFileID(driveFileID)
	}
	// Persist transcript text in metadata for embedding/indexing
	if cand.Transcript != "" {
		if clip.Metadata == nil {
			clip.Metadata = make(map[string]any)
		}
		// Cap to a reasonable size to avoid blowing up metadata_json
		// 200k chars ≈ 50k tokens, enough for a 20-30 min Whisper transcript.
		maxLen := 200000
		if len(cand.Transcript) > maxLen {
			clip.Metadata["clean_transcript"] = cand.Transcript[:maxLen]
			clip.Metadata["transcript_truncated"] = true
		} else {
			clip.Metadata["clean_transcript"] = cand.Transcript
		}
	}

	if err := h.clipsRepo.Upsert(ctx, clip); err != nil {
		failed.Add(1)
		return fmt.Errorf("upsert clip: %w", err)
	}
	log.Info("saved clip to DB", zap.String("clip_id", clip.ID))

	// Step 5: embeddings via existing IndexClip pipeline.
	// SkipQdrant
	// gating and the direct-vector-store fallback (HasVectorStore /
	// UpsertToVectorStore) are gone. The indexer is now the canonical
	// semantic-search backend and is the only post-DB-side leg.
	if !payload.SkipEmbeddings {
		// Stage the transcript in data/youtube-clips/ so the indexer's
		// /index_transcript endpoint (which looks for {base}.txt there) can
		// find it.
		if cand.Transcript != "" {
			stageRoot := h.cfg.Storage.YoutubeClipsPath()
			if stageRoot == "" {
				stageRoot = filepath.Join(h.cfg.Storage.DataDir, "youtube-clips")
			}
			baseNoExt := strings.TrimSuffix(filepath.Base(cand.LocalPath), filepath.Ext(cand.LocalPath))
			subBucket := strings.TrimSpace(cand.Subdir)
			if subBucket == "" || subBucket == "." {
				subBucket = "_root"
			}
			subBucket = strings.Map(func(r rune) rune {
				if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
					return r
				}
				return '_'
			}, subBucket)
			stageDir := filepath.Join(stageRoot, subBucket)
			_ = os.MkdirAll(stageDir, 0o755)
			stagePath := filepath.Join(stageDir, baseNoExt+".txt")
			if err := os.WriteFile(stagePath, []byte(cand.Transcript), 0o644); err != nil {
				log.Warn("transcript staging failed (non-fatal)", zap.String("path", stagePath), zap.Error(err))
			}
		}
		if h.clipIndexer != nil && h.clipIndexer.IsEnabled() {
			if err := h.clipIndexer.IndexClip(ctx, clip.ID); err != nil {
				log.Warn("indexer failed (non-fatal)", zap.Error(err))
			} else {
				indexed.Add(1)
			}
		}
	}
	return nil
}
