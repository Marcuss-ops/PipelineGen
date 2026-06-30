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
// PG-005 (June 2026) pre-work already defined the ports
// (ClipRepositoryPort, ClipDriveUploaderPort, ClipConfigPort,
// ClipIndexerPort, ClipHashPort). The handler-side glue line
//
//	*config.Config, *assets.ClipsRepository → ClipConfigPort, ClipRepositoryPort
//
// is what previously caused bulk_upload_worker.go to be on the
// docs/migrations/api-infrastructure-imports-allowlist.txt. This file
// is the canonical seam above that infra; the API handler now calls
// out to BulkUploadWorker.HandleJob without itself importing infra.
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

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/mutations"
)

// BulkUploadWorker owns the heavy business logic for the
// "bulk_upload_youtube_clips" job. Construction takes the typed ports
// only; no concrete infrastructure types appear in this file.
//
// PR 7 (June 2026, codex/qdrant-app-writers-fail-closed): the
// `dispatcher` field is the canonical mutations.AssetMutationDispatcher
// SSOT. Required so the worker's media_assets UPSERT step routes
// through the canonical outbox+tx writer (QDRANT-002 atomicity
// invariant). Strict fail-closed on nil dispatcher — the worker
// surfaces an explicit error instead of writing a half-state asset
// row. See internal/app/registry_adapters.go::newMutationsDispatcherAdapter
// for the error sentinel.
type BulkUploadWorker struct {
	uploader   ClipDriveUploaderPort // deprecated: kept for non-Publisher paths
	publisher  ClipPublisherPort    // canonical Drive upload (FASE 7)
	repo       ClipRepositoryPort
	indexer    ClipIndexerPort
	hasher     ClipHashPort
	cfg        ClipConfigPort
	dispatcher mutations.AssetMutationDispatcher
	log        *zap.Logger
}

// NewBulkUploadWorker constructs the canonical worker. All port
// arguments are required. Returns a *BulkUploadWorker (never nil);
// callers should treat it as production code (no panic on nil deps).
//
// PR 7 (June 2026, codex/qdrant-app-writers-fail-closed): added
// `dispatcher` as the 7th positional arg (between cfg and log) so the
// worker's media_assets write enforces the canonical outbox path.
// Composition root pre-rejection lives in the wiring site (see
// internal/app/module_media.go::WireAssets) which surfaces a
// configure-time error if dispatcher is nil.
func NewBulkUploadWorker(
	uploader ClipDriveUploaderPort,
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
		uploader:   uploader,
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
// Job-level deadline prevents abandoned jobs from sitting half-done
// forever; the worker ctx only times out on shutdown, which leaves
// orphans otherwise.
//
// HC-1 (June 2026): the per-job timeout resolves through the typed
// config-port `w.cfg.JobTimeout(jobType)` — the canonical Registry is
// the SSOT (replaces the pre-HC-1 hard-coded 2*time.Hour literal).
// Refusal to fire wall-clock timeouts left a job open indefinitely
// under stacked Drive + indexer + Qdrant lock contention; the typed
// lookup lets the operator override the timeout per job-type without
// re-deploying the worker.
//
// HC-1 code-review cleanup: NO belt-and-suspenders 2h fallback on
// nil-cfg — the cfg adapter (internal/app/clips_adapters_cfg.go's
// clipsCfgAdapter.JobTimeout) already returns the canonical 10-minute
// default when the resolver is nil or returns 0. Trusting the typed
// port removes a dead-code path AND the 2h/10m inconsistency between
// the worker fallback (2h) and the rest of the pipeline (10m).
func (w *BulkUploadWorker) HandleJob(ctx context.Context, j *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	// HC-1 (June 2026): per-job-type timeout resolves through the
	// typed config-port (ClipConfigPort.JobTimeout → adapter
	// → *jobs.Registry.JobTimeout). cfg is mandatory — the
	// constructor (NewBulkUploadWorker) accepts it unconditionally
	// and production wiring in module_media.go always supplies
	// non-nil cfg. A nil cfg at runtime is a misconfiguration
	// surfaced as an explicit error (no silent fallback).
	//
	// P0.4 (June 2026): the local `const defaultTimeout = 10*time.Minute`
	// fallback is REMOVED. The canonical fallback lives in the
	// clipsCfgAdapter.JobTimeout (internal/app/clips_adapters_cfg.go)
	// which returns 10 minutes when the resolver is nil or returns 0.
	// The worker trusts the typed port and does not second-guess it.
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
	if w.uploader == nil && w.publisher == nil {
		return nil, fmt.Errorf("drive uploader or publisher not configured")
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

	// Resolve subdir folder cache (Drive folder ID per subdir).
	// Mutex prevents two concurrent clips sharing a subdir from both
	// creating duplicate folders on Drive.
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
		if v, ok := subdirFolderCache.Load(subdir); ok {
			return v.(string), nil
		}
		id, err := w.uploader.GetOrCreateFolder(ctx, subdir, payload.DriveFolderID)
		if err != nil {
			return "", err
		}
		subdirFolderCache.Store(subdir, id)
		return id, nil
	}

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

			if err := w.processOneClip(ctx, payload, cand, resolveSubdirFolderID,
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
func (w *BulkUploadWorker) processOneClip(
	ctx context.Context,
	payload *appjobs.BulkUploadYouTubeClipsPayload,
	cand clipCandidate,
	resolveSubdirFolderID func(ctx context.Context, subdir string) (string, error),
	uploaded, indexed, pushed, skipped, failed *atomic.Int64,
	log *zap.Logger,
) error {
	if w == nil || w.hasher == nil || (w.uploader == nil && w.publisher == nil) || w.repo == nil {
		failed.Add(1)
		return fmt.Errorf("bulk upload not configured correctly")
	}

	// Step 1: hash
	fileHash, err := w.hasher.MD5File(cand.LocalPath)
	if err != nil {
		failed.Add(1)
		return fmt.Errorf("hash: %w", err)
	}

	clipID := buildBulkClipID(cand, fileHash)
	log = log.With(zap.String("clip_id", clipID), zap.String("path", cand.LocalPath))

	// Step 2: Drive target folder (with subdir if requested)
	// When publisher is available, folder resolution is handled internally;
	// only call resolveSubdirFolderID when using the legacy uploader path.
	targetFolderID := payload.DriveFolderID
	if w.publisher == nil && payload.SubdirAsDriveSubdir && cand.Subdir != "" && cand.Subdir != "." {
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

		// Determine the group for Publisher (subdir maps to group folder)
		pubGroup := ""
		if payload.SubdirAsDriveSubdir && cand.Subdir != "" && cand.Subdir != "." {
			pubGroup = cand.Subdir
		}

		if w.publisher != nil {
			// FASE 7: use canonical Publisher
			pubReq := delivery.PublishRequest{
				Destination:        delivery.DestinationYouTubeClip,
				LocalPath:          cand.LocalPath,
				Filename:           driveFilename,
				Description:        driveDesc,
				Group:              pubGroup,
				RootFolderOverride: payload.DriveFolderID,
			}
			pubRes, err := w.publisher.Publish(ctx, pubReq)
			if err != nil {
				failed.Add(1)
				return fmt.Errorf("drive publish: %w", err)
			}
			driveFileID = pubRes.FileID
			driveLink = pubRes.WebViewLink
			// F1.5 (P0 #9): read DownloadLink from the canonical
			// PublishResult instead of reconstructing via string
			// interpolation. Recomputing uc?id= on the consumer side
			// risks drift against the URL format Drive returns and
			// prevents the canonical Publisher from centralising URL
			// formatting (e.g. for ?export=download variants).
			downloadLink = pubRes.DownloadLink
			targetFolderID = pubRes.FolderID
			uploaded.Add(1)
			log.Info("published to drive",
				zap.String("file_id", driveFileID),
				zap.String("drive_link", driveLink),
				zap.String("publish_action", string(pubRes.Action)))			// Upload siblings via Publisher (best effort)
			// Group is empty because the folder is already resolved by the
			// first Publish call (targetFolderID = pubRes.FolderID). Setting
			// Group here would create double-nesting.
			dir := filepath.Dir(cand.LocalPath)
			baseNoExt := strings.TrimSuffix(filepath.Base(cand.LocalPath), filepath.Ext(cand.LocalPath))
			manifestPath := filepath.Join(dir, "clip_manifest.json")
			if _, err := os.Stat(manifestPath); err == nil {
				w.publisher.Publish(ctx, delivery.PublishRequest{
					Destination:        delivery.DestinationYouTubeClip,
					LocalPath:          manifestPath,
					Filename:           baseNoExt + ".clip_manifest.json",
					Description:        "Clip manifest for " + baseNoExt,
					RootFolderOverride: targetFolderID,
				})
			}
			for _, tp := range []string{filepath.Join(dir, baseNoExt+".txt"), filepath.Join(dir, "transcript.txt")} {
				if _, err := os.Stat(tp); err == nil {
					w.publisher.Publish(ctx, delivery.PublishRequest{
						Destination:        delivery.DestinationYouTubeClip,
						LocalPath:          tp,
						Filename:           baseNoExt + ".transcript.txt",
						Description:        "Whisper transcript for " + baseNoExt,
						RootFolderOverride: targetFolderID,
					})
					break
				}
			}
		} else {
			// Legacy fallback
			upRes, err := w.uploader.UploadFileWithDescription(ctx, cand.LocalPath, targetFolderID, driveFilename, driveDesc)
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
		manifestPath := filepath.Join(dir, "clip_manifest.json")
		if _, err := os.Stat(manifestPath); err == nil {
				mdesc := "Clip manifest for " + baseNoExt
				if _, e := w.uploader.UploadFileWithDescription(ctx, manifestPath, targetFolderID, baseNoExt+".clip_manifest.json", mdesc); e != nil {
					log.Warn("manifest.json upload failed (non-fatal)", zap.Error(e))
				}
			}
			for _, tp := range []string{filepath.Join(dir, baseNoExt+".txt"), filepath.Join(dir, "transcript.txt")} {
				if _, err := os.Stat(tp); err == nil {
					tdesc := "Whisper transcript for " + baseNoExt
					if _, e := w.uploader.UploadFileWithDescription(ctx, tp, targetFolderID, baseNoExt+".transcript.txt", tdesc); e != nil {
						log.Warn("transcript.txt upload failed (non-fatal)", zap.Error(e))
					}
					break
				}
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
		LifecycleState: asset.StateActive,
		Duration:       time.Duration(extractIntFromManifest(cand.Manifest, "duration_sec")) * time.Second,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	clip.SetLocalPath(cand.LocalPath)
	clip.SetFileHash(fileHash)
	clip.SetFolderID(targetFolderID)
	clip.SetFolderPath(cand.Subdir)
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
	if cand.Transcript != "" {
		if clip.Metadata == nil {
			clip.Metadata = make(map[string]any)
		}
		maxLen := 200000
		if len(cand.Transcript) > maxLen {
			clip.Metadata["clean_transcript"] = cand.Transcript[:maxLen]
			clip.Metadata["transcript_truncated"] = true
		} else {
			clip.Metadata["clean_transcript"] = cand.Transcript
		}
	}

	// PR 7 (June 2026, codex/qdrant-app-writers-fail-closed): route the
	// media_assets UPSERT through the canonical mutations.AssetMutationDispatcher
	// so the QDRANT-002 atomicity invariant (media_assets UPSERT + outbox_events
	// INSERT in one tx) applies uniformly to bulk-upload workers.
	//
	// Strict fail-closed: a nil dispatcher returns
	// mutations.ErrDispatcherUnavailable wrapped with context so the
	// work's failure is operator-visible via the job outcome, NOT as a
	// half-written asset row that would orphan a Qdrant upsert.
	// contentHash is the MD5 already computed in Step 1; mirrors the v1
	// supersede-gate semantics (QDRANT-002 item F: source_version on
	// index.requested.v1).
	if w.dispatcher == nil {
		failed.Add(1)
		return fmt.Errorf("bulk upload dispatcher not configured (QDRANT-asset-mutation isolation required): %w", mutations.ErrDispatcherUnavailable)
	}
	if err := w.dispatcher.EnqueueAndIndex(ctx, clip, fileHash); err != nil {
		failed.Add(1)
		return fmt.Errorf("dispatcher enqueue: %w", err)
	}
	log.Info("saved clip to DB", zap.String("clip_id", clip.ID))

	// Step 5: embeddings via existing IndexClip pipeline.
	// SkipQdrant gating and the direct-vector-store fallback
	// (HasVectorStore / UpsertToVectorStore) are gone. The indexer is now
	// the canonical semantic-search backend and is the only post-DB-side leg.
	if !payload.SkipEmbeddings {
		// Stage the transcript in data/youtube-clips/ so the indexer's
		// /index_transcript endpoint can find it.
		if cand.Transcript != "" {
			stageRoot := w.cfg.YoutubeClipsPath()
			if stageRoot == "" {
				stageRoot = filepath.Join(w.cfg.DataDir(), "youtube-clips")
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
		if w.indexer != nil && w.indexer.IsEnabled() {
			if err := w.indexer.IndexClip(ctx, clip.ID); err != nil {
				log.Warn("indexer failed (non-fatal)", zap.Error(err))
			} else {
				indexed.Add(1)
			}
		}
	}
	return nil
}

// clipCandidate and its DisplayName method are defined in
// bulk_upload_helpers.go (this package). No duplicate here.

func scanLocalClips(root string, recursive bool, include, skip []string, limit int) ([]clipCandidate, error) {
	if root == "" {
		return nil, fmt.Errorf("root is empty")
	}
	out := []clipCandidate{}
	count := 0

	walk := func(path string, info os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(info.Name()), ".mp4") {
			return nil
		}
		if len(include) > 0 && !matchAny(info.Name(), include) {
			return nil
		}
		if len(skip) > 0 && matchAny(info.Name(), skip) {
			return nil
		}
		if limit > 0 && count >= limit {
			return filepath.SkipDir
		}
		subdir, _ := filepath.Rel(root, filepath.Dir(path))
		manifest := readManifest(filepath.Join(filepath.Dir(path), "clip_manifest.json"))
		transcript := readTranscript(filepath.Join(filepath.Dir(path), strings.TrimSuffix(info.Name(), filepath.Ext(info.Name()))+".txt"), path)
		out = append(out, clipCandidate{
			Name:       strings.TrimSuffix(info.Name(), filepath.Ext(info.Name())),
			LocalPath:  path,
			Subdir:     subdir,
			Manifest:   manifest,
			Transcript: transcript,
		})
		count++
		return nil
	}
	var err error
	if recursive {
		err = filepath.WalkDir(root, walk)
	} else {
		err = readDirShallow(root, walk)
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func readDirShallow(root string, walk func(path string, info os.DirEntry, err error) error) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		full := filepath.Join(root, e.Name())
		if err := walk(full, e, nil); err != nil {
			if err == filepath.SkipDir {
				continue
			}
			return err
		}
	}
	return nil
}

func matchAny(name string, patterns []string) bool {
	lower := strings.ToLower(name)
	for _, p := range patterns {
		if strings.Contains(lower, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

func readManifest(path string) map[string]any {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := jsonUnmarshal(b, &m); err != nil {
		return nil
	}
	return m
}

func readTranscript(txtPath, mp4Path string) string {
	for _, p := range []string{txtPath, filepath.Join(filepath.Dir(mp4Path), "transcript.txt")} {
		if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
			return string(b)
		}
	}
	return ""
}

// The helpers buildBulkClipID, sanitiseDriveName, buildBulkDriveDescription,
// deriveSearchText, and extractIntFromManifest are now defined once in
// bulk_upload_helpers.go (this package). The previous duplicates in this file
// were removed during Wave 14 PR2 slice 2 (June 2026).
