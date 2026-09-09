package adapters

import (
	"context"
	"encoding/json"

	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

// ClipRenderPublisher is the composition-root adapter for the final render
// boundary. Drive publication goes through delivery.Publisher; the derived
// asset and its location are committed through the canonical AssetCommitter.
//
// Every boundary step (hash, upload video, upload sidecar, taxonomy resolve,
// PostgreSQL commit) is logged with structured zap entries and timed so the
// pipelinegen call chain can be reconstructed from logs alone.
type ClipRenderPublisher struct {
	drive             delivery.Publisher
	committer         persistence.AssetCommitter
	log               *zap.Logger
	asyncDrive        bool
	asyncStagingRoot  string
	subtitleArtifacts detail.SubtitleArtifactRepository
}

// SetSubtitleArtifactRepository attaches the canonical ASS artifact registry.
func (p *ClipRenderPublisher) SetSubtitleArtifactRepository(repo detail.SubtitleArtifactRepository) {
	if p != nil {
		p.subtitleArtifacts = repo
	}
}

// SetAsyncDrive enables the production clip path that commits the rendered
// asset and a Drive-delivery intent without waiting for Google Drive. The
// outbox consumer owns the later upload and location reconciliation. Tests
// keep the legacy synchronous mode unless they opt in explicitly.
func (p *ClipRenderPublisher) SetAsyncDrive(enabled bool) {
	if p != nil {
		p.asyncDrive = enabled
	}
}

// SetAsyncDriveStagingRoot configures the durable root used to detach an
// asynchronous clip artifact from the per-job workspace. The workspace is
// cleaned as soon as the job finishes; this root must therefore be outside it.
func (p *ClipRenderPublisher) SetAsyncDriveStagingRoot(root string) {
	if p != nil {
		p.asyncStagingRoot = strings.TrimSpace(root)
	}
}

// NewClipRenderPublisher builds the publisher; log is required so each
// publication phase is observable.
func NewClipRenderPublisher(drive delivery.Publisher, committer persistence.AssetCommitter, log *zap.Logger) (*ClipRenderPublisher, error) {
	if drive == nil || committer == nil {
		return nil, errors.New("clip.render publisher: Drive publisher and asset committer are required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &ClipRenderPublisher{drive: drive, committer: committer, log: log}, nil
}

func (p *ClipRenderPublisher) publishPhase(phase, runID string, fields ...zap.Field) {
	all := append([]zap.Field{
		zap.String("subsystem", "clip_render_publish"),
		zap.String("phase", phase),
		zap.String("run_id", runID),
	}, fields...)
	p.log.Info("clip.render.publish.phase", all...)
}

// publishMetrics tracks the wall-clock duration of each publish phase.
type publishMetrics struct {
	HashMS               int64 `json:"hash_ms"`
	VideoUploadMS        int64 `json:"video_upload_ms"`
	SidecarUploadMS      int64 `json:"sidecar_upload_ms"`
	TaxonomyResolveMS    int64 `json:"taxonomy_resolve_ms"`
	AssetCommitMS        int64 `json:"asset_commit_ms"`
	TotalMS              int64 `json:"total_ms"`
	VideoUploadSucceeded bool  `json:"video_upload_succeeded"`
	SidecarUploaded      bool  `json:"sidecar_upload_succeeded"`
}

func (p *ClipRenderPublisher) Publish(ctx context.Context, in cliprender.RenderPublishInput) (*cliprender.RenderPublishResult, error) {
	if p == nil || p.drive == nil || p.committer == nil {
		return nil, fmt.Errorf("clip.render publisher: Drive publisher and asset committer are required")
	}
	if in.OutputPath == "" || in.Outcome == nil || in.DriveFolderID == "" {
		return nil, fmt.Errorf("clip.render publisher: output, outcome and destination folder are required")
	}
	metrics := publishMetrics{}
	started := time.Now()
	runID := in.RunID

	p.publishPhase("start", runID,
		zap.String("output_path", in.OutputPath),
		zap.String("drive_folder_id", in.DriveFolderID),
		zap.String("source_asset_id", in.SourceAssetID),
		zap.Int64("outcome_size_bytes", in.Outcome.SizeBytes),
		zap.Bool("has_subtitles", in.Subtitles != nil),
	)

	// ── Phase 1: hash the rendered mp4 on disk ────────────────────────
	hashStart := time.Now()
	contentHash, size, err := digest.SHA256File(in.OutputPath)
	if err != nil {
		p.publishPhase("hash_failed", runID, zap.Error(err))
		return nil, fmt.Errorf("hash rendered output: %w", err)
	}
	metrics.HashMS = time.Since(hashStart).Milliseconds()
	assetID := "cliprender_" + contentHash[:24]

	ext := filepath.Ext(in.OutputPath)
	if ext == "" {
		ext = ".mp4"
	}
	driveFilename := assetID + ext
	if strings.TrimSpace(in.SourceTitle) != "" {
		safeTitle := textutil.SanitizeFilename(in.SourceTitle)
		if safeTitle != "" && safeTitle != "unnamed" {
			driveFilename = safeTitle + ext
		}
	}

	// Subtitle sidecars are uploaded ONLY when explicitly in sidecar mode.
	// Burned subtitles are already baked into video frames and must never be uploaded as .ass files to Drive.
	hasSubtitleSidecar := in.Subtitles != nil && in.Subtitles.Mode == cliprender.SubtitlesModeSidecar
	p.publishPhase("hash_done", runID,
		zap.String("content_hash", contentHash),
		zap.Int64("size_bytes", size),
		zap.String("asset_id", assetID),
		zap.String("drive_filename", driveFilename),
		zap.Bool("has_subtitle_sidecar", hasSubtitleSidecar),
		zap.Int64("duration_ms", metrics.HashMS),
	)
	if p.asyncDrive && !hasSubtitleSidecar {
		return p.publishAsyncDrive(ctx, in, started, metrics.HashMS, contentHash, size, assetID, driveFilename)
	}

	// ── Phase 2: upload video + subtitle sidecar concurrently ────────
	// The video and its deterministic ASS sidecar are independent remote
	// artifacts. Upload them concurrently, but do not return success until
	// both are confirmed; the SQLite commit below remains the single durable
	// completion boundary. This removes the old video-upload -> ASS-upload
	// serialization without weakening fail-closed publication semantics.
	var pub *delivery.PublishResult
	var sidecarFileID, sidecarLink string
	if hasSubtitleSidecar && in.Subtitles.DriveFileID != "" {
		// The canonical ASS already exists in Drive. It is content-addressed by
		// the artifact repository; do not publish a duplicate for this render.
		sidecarFileID, sidecarLink = in.Subtitles.DriveFileID, in.Subtitles.DriveLink
		metrics.SidecarUploaded = true
		p.publishPhase("sidecar_upload_reused", runID, zap.String("file_id", sidecarFileID))
		hasSubtitleSidecar = false
	}
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		uploadStart := time.Now()
		p.publishPhase("video_upload_start", runID,
			zap.String("filename", driveFilename),
			zap.String("asset_id", assetID),
		)
		result, publishErr := p.drive.Publish(gctx, delivery.PublishRequest{
			Destination:         delivery.DestinationClipMetadata,
			DestinationFolderID: in.DriveFolderID,
			LocalPath:           in.OutputPath,
			Filename:            driveFilename,
			AssetID:             assetID,
			SourceVersion:       1,
			ContentHash:         contentHash,
			// Logical clip identity is stable across re-encoding. Do not include
			// the output hash here: a valid rerender must update the same Drive
			// filename rather than create a new file/folder for new bytes.
			IdempotencyKey: delivery.DeriveIdempotencyKey(delivery.DestinationClipMetadata, in.SourceAssetID+":"+driveFilename, "clip-render-v1", 1),
			// clip.render is a regenerable projection: the canonical filename
			// identifies the logical clip, while the encoded byte hash may change
			// between valid rerenders. Replace that file instead of creating a
			// second root entry for every rerender.
			ConflictPolicy: delivery.ConflictOverwrite,
		})
		metrics.VideoUploadMS = time.Since(uploadStart).Milliseconds()
		if publishErr != nil {
			p.publishPhase("video_upload_failed", runID,
				zap.Int64("duration_ms", metrics.VideoUploadMS),
				zap.Error(publishErr),
			)
			return fmt.Errorf("publish rendered clip: %w", publishErr)
		}
		if result == nil || result.FileID == "" {
			p.publishPhase("video_upload_invalid_result", runID,
				zap.Int64("duration_ms", metrics.VideoUploadMS),
			)
			return fmt.Errorf("publish rendered clip: empty Drive result")
		}
		metrics.VideoUploadSucceeded = true
		p.publishPhase("video_upload_done", runID,
			zap.String("file_id", result.FileID),
			zap.String("folder_id", result.FolderID),
			zap.String("action", string(result.Action)),
			zap.Int64("duration_ms", metrics.VideoUploadMS),
			zap.Int64("size_bytes", size),
			zap.String("md5_checksum", result.MD5Checksum),
		)
		pub = result
		return nil
	})
	if hasSubtitleSidecar {
		g.Go(func() error {
			uploadStart := time.Now()
			sidecarFilename := assetID + ".ass"
			if strings.TrimSpace(in.SourceTitle) != "" {
				safeTitle := textutil.SanitizeFilename(in.SourceTitle)
				if safeTitle != "" && safeTitle != "unnamed" {
					sidecarFilename = safeTitle + ".ass"
				}
			}
			p.publishPhase("sidecar_upload_start", runID,
				zap.String("filename", sidecarFilename),
				zap.String("local_path", in.Subtitles.LocalPath),
			)
			result, publishErr := p.drive.Publish(gctx, delivery.PublishRequest{
				Destination:         delivery.DestinationClipMetadata,
				DestinationFolderID: in.DriveFolderID,
				LocalPath:           in.Subtitles.LocalPath,
				Filename:            sidecarFilename,
				AssetID:             assetID,
				SourceVersion:       1,
				ContentHash:         in.Subtitles.SHA256,
				IdempotencyKey:      delivery.DeriveIdempotencyKey(delivery.DestinationClipMetadata, assetID, in.Subtitles.SHA256, 1),
				ConflictPolicy:      delivery.ConflictOverwrite,
			})
			metrics.SidecarUploadMS = time.Since(uploadStart).Milliseconds()
			if publishErr != nil {
				p.publishPhase("sidecar_upload_failed", runID,
					zap.Int64("duration_ms", metrics.SidecarUploadMS),
					zap.Error(publishErr),
				)
				return fmt.Errorf("publish subtitles sidecar: %w", publishErr)
			}
			if result == nil || result.FileID == "" {
				p.publishPhase("sidecar_upload_invalid_result", runID,
					zap.Int64("duration_ms", metrics.SidecarUploadMS),
				)
				return fmt.Errorf("publish subtitles sidecar: empty Drive result")
			}
			metrics.SidecarUploaded = true
			p.publishPhase("sidecar_upload_done", runID,
				zap.String("file_id", result.FileID),
				zap.Int64("duration_ms", metrics.SidecarUploadMS),
			)
			sidecarFileID, sidecarLink = result.FileID, result.WebViewLink
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	if in.Subtitles != nil && p.subtitleArtifacts != nil && sidecarFileID != "" {
		language := "und"
		textHash := ""
		if in.Transcript != nil {
			language = in.Transcript.Language
			if language == "" {
				language = "und"
			}
			textHash = in.Transcript.TextSHA256
		}
		if err := p.subtitleArtifacts.Upsert(ctx, &detail.SubtitleArtifact{
			AssetID: in.SourceAssetID, LanguageCode: language,
			Format: detail.SubtitleFormatASS, LocalPath: in.Subtitles.LocalPath,
			DriveFileID: sidecarFileID, DriveURL: sidecarLink,
			LegacyFileMD5: in.Subtitles.SHA256, TextHash: textHash,
			StyleVersion: in.Subtitles.StyleID, Status: detail.SubtitleStatusReady,
			IsCurrent: true,
		}); err != nil {
			return nil, fmt.Errorf("persist published subtitle artifact: %w", err)
		}
	}

	// ── Phase 3: resolve canonical taxonomy ───────────────────────────
	taxonomyStart := time.Now()
	taxonomy, err := mediaregistry.ResolveTaxonomy(mediaregistry.TaxonomyInput{
		AssetID: assetID, Provider: "pipelinegen", MediaType: mediaregistry.MediaVideo,
		AssetKind: mediaregistry.AssetRenderedVideo,
	})
	if err != nil {
		p.publishPhase("taxonomy_failed", runID, zap.Error(err))
		return nil, fmt.Errorf("resolve rendered taxonomy: %w", err)
	}
	metrics.TaxonomyResolveMS = time.Since(taxonomyStart).Milliseconds()
	p.publishPhase("taxonomy_resolved", runID,
		zap.Int64("duration_ms", metrics.TaxonomyResolveMS),
	)

	// ── Phase 4: SQLite asset commit (single durable completion) ──────
	durationMS := int64(in.Outcome.DurationSec * 1000)
	title := driveFilename
	if strings.TrimSpace(in.SourceTitle) != "" {
		title = in.SourceTitle
	}
	commitRequest := persistence.AssetCommitRequest{
		AssetID: assetID, Source: "clip.render", Name: driveFilename, Filename: driveFilename,
		MediaType: "video", Category: "clip-render", DurationMs: durationMS,
		// DISCOVERED is the canonical initial index state. PENDING was retired
		// from the media_assets enum and makes publication fail at the SQLite
		// registry gate after the Drive upload has already succeeded.
		ContentHash: contentHash, LifecycleState: "ACTIVE", IndexState: "DISCOVERED",
		LocalPath: in.OutputPath, FolderID: pub.FolderID, FolderPath: pub.FolderPath,
		SourceURL: in.SourceAssetID, AssetVersion: contentHash, Rendition: "rendered",
		Title: title, SourceProvider: "pipelinegen", Taxonomy: taxonomy,
		Metadata: persistence.TypedMetadata{Title: title, Origin: "clip.render", SourceVersion: contentHash,
			PublishAction: "clip.render", SizeBytes: size, Extra: map[string]any{
				"source_asset_id": in.SourceAssetID, "plan_run_id": in.RunID,
				"drive_file_id": pub.FileID, "subtitle_file_id": sidecarFileID,
			}},
		Locations: []persistence.LocationCommit{{Kind: "drive", Provider: "google_drive", ExternalID: pub.FileID,
			URI: pub.DownloadLink, WebViewLink: pub.WebViewLink, DownloadURL: pub.DownloadLink,
			MimeType: "video/mp4", FileSizeBytes: size, LegacyFileMD5: contentHash, IsPrimary: true}},
		EmitIndexEvent: true,
	}
	commitStart := time.Now()
	_, err = p.committer.CommitAsset(ctx, commitRequest)
	metrics.AssetCommitMS = time.Since(commitStart).Milliseconds()
	if err != nil {
		p.publishPhase("commit_failed", runID,
			zap.Int64("duration_ms", metrics.AssetCommitMS),
			zap.Error(err),
		)
		return nil, fmt.Errorf("commit rendered asset: %w", err)
	}
	p.publishPhase("commit_done", runID,
		zap.Int64("duration_ms", metrics.AssetCommitMS),
	)

	metrics.TotalMS = time.Since(started).Milliseconds()
	p.log.Info("clip.render.publish.completed",
		zap.String("subsystem", "clip_render_publish"),
		zap.String("run_id", runID),
		zap.String("asset_id", assetID),
		zap.String("drive_file_id", pub.FileID),
		zap.String("drive_link", pub.WebViewLink),
		zap.Int64("size_bytes", size),
		zap.Int64("hash_ms", metrics.HashMS),
		zap.Int64("video_upload_ms", metrics.VideoUploadMS),
		zap.Int64("sidecar_upload_ms", metrics.SidecarUploadMS),
		zap.Int64("taxonomy_resolve_ms", metrics.TaxonomyResolveMS),
		zap.Int64("asset_commit_ms", metrics.AssetCommitMS),
		zap.Int64("total_ms", metrics.TotalMS),
	)
	// Publication metrics have ONE chronometer owner: this publisher. The
	// measured sub-phase walls travel with the result so the worker projects
	// them into the canonical RenderMetricsV2 report instead of re-timing
	// publication with a second, worker-side chronometer.
	return &cliprender.RenderPublishResult{
		AssetID:       assetID,
		DriveFileID:   pub.FileID,
		DriveLink:     pub.WebViewLink,
		SizeBytes:     size,
		SidecarFileID: sidecarFileID,
		SidecarLink:   sidecarLink,
		Publish: &cliprender.PublicationMetrics{
			HashMS:            metrics.HashMS,
			VideoUploadMS:     metrics.VideoUploadMS,
			SidecarUploadMS:   metrics.SidecarUploadMS,
			TaxonomyResolveMS: metrics.TaxonomyResolveMS,
			AssetCommitMS:     metrics.AssetCommitMS,
			TotalMS:           metrics.TotalMS,
		},
	}, nil
}

// publishAsyncDrive completes the local, durable half of publication and
// emits a transactionally-persisted Drive intent. It deliberately supports
// burned subtitles (the normal clip-render mode); sidecar mode stays on the
// synchronous path until its subtitle-artifact mutation is included in the
// same delivery contract.
func (p *ClipRenderPublisher) publishAsyncDrive(
	ctx context.Context,
	in cliprender.RenderPublishInput,
	started time.Time,
	hashMS int64,
	contentHash string,
	size int64,
	assetID, driveFilename string,
) (*cliprender.RenderPublishResult, error) {
	stagedPath, err := p.stageAsyncArtifact(in.OutputPath, assetID, size)
	if err != nil {
		return nil, fmt.Errorf("stage rendered artifact for asynchronous Drive delivery: %w", err)
	}
	taxonomyStart := time.Now()
	taxonomy, err := mediaregistry.ResolveTaxonomy(mediaregistry.TaxonomyInput{
		AssetID: assetID, Provider: "pipelinegen", MediaType: mediaregistry.MediaVideo,
		AssetKind: mediaregistry.AssetRenderedVideo,
	})
	taxonomyMS := time.Since(taxonomyStart).Milliseconds()
	if err != nil {
		return nil, fmt.Errorf("resolve rendered taxonomy: %w", err)
	}

	payload := cliprender.ClipRenderDriveDeliveryRequest{
		SchemaVersion: "clip.render.drive_delivery.v1",
		AssetID:       assetID, RunID: in.RunID, SourceAssetID: in.SourceAssetID,
		LocalPath: stagedPath, Filename: driveFilename,
		FolderID: in.DriveFolderID, ContentHash: contentHash, SizeBytes: size,
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal Drive delivery intent: %w", err)
	}
	keyDigest := digest.SHA256Bytes([]byte(assetID + "|" + contentHash + "|" + in.DriveFolderID))
	commitRequest := persistence.AssetCommitRequest{
		AssetID: assetID, Source: "clip.render", Name: driveFilename,
		Filename: driveFilename, MediaType: "video", Category: "clip-render",
		DurationMs: int64(in.Outcome.DurationSec * 1000), ContentHash: contentHash,
		LifecycleState: "ACTIVE", IndexState: "DISCOVERED", LocalPath: stagedPath,
		FolderID: in.DriveFolderID, SourceURL: in.SourceAssetID,
		AssetVersion: contentHash, Rendition: "rendered", Title: driveFilename,
		SourceProvider: "pipelinegen", Taxonomy: taxonomy,
		Metadata: persistence.TypedMetadata{Title: driveFilename, Origin: "clip.render", SourceVersion: contentHash,
			PublishAction: "clip.render", SizeBytes: size, Extra: map[string]any{
				"source_asset_id": in.SourceAssetID, "plan_run_id": in.RunID,
				"delivery_status": "pending", "drive_folder_id": in.DriveFolderID,
			}},
		EmitIndexEvent: true,
		AdditionalOutboxEvents: []persistence.OutboxEvent{{
			EventType:   cliprender.EventClipRenderDriveDeliveryRequested,
			AggregateID: assetID, AggregateType: "media_asset",
			PayloadJSON: string(payloadJSON), EventKey: "clip-render-drive:" + keyDigest,
		}},
	}
	commitStart := time.Now()
	_, err = p.committer.CommitAsset(ctx, commitRequest)
	commitMS := time.Since(commitStart).Milliseconds()
	if err != nil {
		return nil, fmt.Errorf("commit rendered asset with Drive intent: %w", err)
	}

	metrics := &cliprender.PublicationMetrics{
		HashMS: hashMS, TaxonomyResolveMS: taxonomyMS, AssetCommitMS: commitMS,
		TotalMS: time.Since(started).Milliseconds(),
	}
	p.publishPhase("drive_queued", in.RunID,
		zap.String("asset_id", assetID), zap.String("event_type", cliprender.EventClipRenderDriveDeliveryRequested),
		zap.Int64("duration_ms", metrics.TotalMS), zap.Int64("drive_upload_ms", -1),
	)
	return &cliprender.RenderPublishResult{
		AssetID: assetID, DrivePending: true, SizeBytes: size, Publish: metrics,
	}, nil
}

// stageAsyncArtifact atomically detaches a rendered file from the ephemeral
// job workspace. The outbox event may be processed after the job runner has
// cleaned that workspace, so the payload must reference this durable staging
// copy instead of the renderer's run directory.
func (p *ClipRenderPublisher) stageAsyncArtifact(source, assetID string, size int64) (string, error) {
	root := strings.TrimSpace(p.asyncStagingRoot)
	if root == "" {
		root = filepath.Join(os.TempDir(), "pipelinegen", "cliprender", "staging")
	}
	if !filepath.IsAbs(root) {
		absolute, err := filepath.Abs(root)
		if err != nil {
			return "", fmt.Errorf("resolve staging root %q: %w", root, err)
		}
		root = absolute
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return "", fmt.Errorf("create staging root %q: %w", root, err)
	}
	ext := filepath.Ext(source)
	if ext == "" {
		ext = ".mp4"
	}
	destination := filepath.Join(root, assetID+ext)
	if source == destination {
		return destination, nil
	}

	if _, err := os.Stat(destination); err == nil {
		return p.reuseStagedArtifact(source, destination, assetID, size)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect staged artifact %q: %w", destination, err)
	}
	if err := os.Rename(source, destination); err == nil {
		return destination, nil
	}

	// The workspace and configured staging root may be on different mounts;
	// fall back to a verified copy when atomic rename is unavailable.
	inFile, err := os.Open(source)
	if err != nil {
		return "", fmt.Errorf("open source artifact %q: %w", source, err)
	}
	outFile, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		_ = inFile.Close()
		if os.IsExist(err) {
			return p.reuseStagedArtifact(source, destination, assetID, size)
		}
		return "", fmt.Errorf("create staged artifact %q: %w", destination, err)
	}
	_, copyErr := io.Copy(outFile, inFile)
	if copyErr == nil {
		copyErr = outFile.Sync()
	}
	closeOutErr := outFile.Close()
	closeInErr := inFile.Close()
	if copyErr != nil {
		_ = os.Remove(destination)
		return "", fmt.Errorf("copy artifact to staging: %w", copyErr)
	}
	if closeOutErr != nil {
		_ = os.Remove(destination)
		return "", fmt.Errorf("close staged artifact: %w", closeOutErr)
	}
	if closeInErr != nil {
		return "", fmt.Errorf("close source artifact: %w", closeInErr)
	}
	if err := os.Remove(source); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("remove workspace artifact after staging: %w", err)
	}
	return destination, nil
}

func (p *ClipRenderPublisher) reuseStagedArtifact(source, destination, assetID string, size int64) (string, error) {
	stagedHash, stagedSize, err := digest.SHA256File(destination)
	if err != nil {
		return "", fmt.Errorf("verify existing staged artifact: %w", err)
	}
	prefixLen := len(assetID) - len("cliprender_")
	if prefixLen <= 0 || len(stagedHash) < prefixLen || stagedSize != size || !strings.HasPrefix(assetID, "cliprender_") || !strings.HasPrefix(assetID[len("cliprender_"):], stagedHash[:prefixLen]) {
		return "", fmt.Errorf("existing staged artifact %q does not match asset %q", destination, assetID)
	}
	if err := os.Remove(source); err != nil && !os.IsNotExist(err) && source != destination {
		return "", fmt.Errorf("remove duplicate workspace artifact: %w", err)
	}
	return destination, nil
}
