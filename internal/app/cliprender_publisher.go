package app

import (
	"context"

	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/persistence"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

// clipRenderPublisher is the composition-root adapter for the final render
// boundary. Drive publication goes through delivery.Publisher; the derived
// asset and its location are committed through the canonical AssetCommitter.
//
// Every boundary step (hash, upload video, upload sidecar, taxonomy resolve,
// SQLite commit) is logged with structured zap entries and timed so the
// pipelinegen call chain can be reconstructed from logs alone.
type clipRenderPublisher struct {
	drive     delivery.Publisher
	committer persistence.AssetCommitter
	log       *zap.Logger
	// SQLite has one canonical writer. Render jobs may encode and upload in
	// parallel, but their final asset commits must not overlap.
	commitMu sync.Mutex
}

// newClipRenderPublisher builds the publisher; log is required so each
// publication phase is observable.
func newClipRenderPublisher(drive delivery.Publisher, committer persistence.AssetCommitter, log *zap.Logger) (*clipRenderPublisher, error) {
	if drive == nil || committer == nil {
		return nil, errors.New("clip.render publisher: Drive publisher and asset committer are required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &clipRenderPublisher{drive: drive, committer: committer, log: log}, nil
}

func (p *clipRenderPublisher) publishPhase(phase, runID string, fields ...zap.Field) {
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

func (p *clipRenderPublisher) Publish(ctx context.Context, in cliprender.RenderPublishInput) (*cliprender.RenderPublishResult, error) {
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
	filename := assetID + filepath.Ext(in.OutputPath)
	hasSubtitleSidecar := in.Subtitles != nil
	p.publishPhase("hash_done", runID,
		zap.String("content_hash", contentHash),
		zap.Int64("size_bytes", size),
		zap.String("asset_id", assetID),
		zap.String("filename", filename),
		zap.Int64("duration_ms", metrics.HashMS),
	)

	// ── Phase 2: upload video + subtitle sidecar concurrently ────────
	// The video and its deterministic ASS sidecar are independent remote
	// artifacts. Upload them concurrently, but do not return success until
	// both are confirmed; the SQLite commit below remains the single durable
	// completion boundary. This removes the old video-upload -> ASS-upload
	// serialization without weakening fail-closed publication semantics.
	var pub *delivery.PublishResult
	var sidecarFileID, sidecarLink string
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		uploadStart := time.Now()
		p.publishPhase("video_upload_start", runID,
			zap.String("filename", filename),
			zap.String("asset_id", assetID),
		)
		result, publishErr := p.drive.Publish(gctx, delivery.PublishRequest{
			Destination:         delivery.DestinationClipMetadata,
			DestinationFolderID: in.DriveFolderID,
			LocalPath:           in.OutputPath,
			Filename:            filename,
			AssetID:             assetID,
			SourceVersion:       1,
			ContentHash:         contentHash,
			IdempotencyKey:      delivery.DeriveIdempotencyKey(delivery.DestinationClipMetadata, assetID, contentHash, 1),
			ConflictPolicy:      delivery.ConflictSkip,
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
			p.publishPhase("sidecar_upload_start", runID,
				zap.String("filename", assetID+".ass"),
				zap.String("local_path", in.Subtitles.LocalPath),
			)
			result, publishErr := p.drive.Publish(gctx, delivery.PublishRequest{
				Destination:         delivery.DestinationClipMetadata,
				DestinationFolderID: in.DriveFolderID,
				LocalPath:           in.Subtitles.LocalPath,
				Filename:            assetID + ".ass",
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
	commitRequest := persistence.AssetCommitRequest{
		AssetID: assetID, Source: "clip.render", Name: filename, Filename: filename,
		MediaType: "video", Category: "clip-render", DurationMs: durationMS,
		// DISCOVERED is the canonical initial index state. PENDING was retired
		// from the media_assets enum and makes publication fail at the SQLite
		// registry gate after the Drive upload has already succeeded.
		ContentHash: contentHash, LifecycleState: "ACTIVE", IndexState: "DISCOVERED",
		LocalPath: in.OutputPath, FolderID: pub.FolderID, FolderPath: pub.FolderPath,
		SourceURL: in.SourceAssetID, AssetVersion: contentHash, Rendition: "rendered",
		Title: filename, SourceProvider: "pipelinegen", Taxonomy: taxonomy,
		Metadata: persistence.TypedMetadata{Title: filename, Origin: "clip.render", SourceVersion: contentHash,
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
	p.commitMu.Lock()
	_, err = p.committer.CommitAsset(ctx, commitRequest)
	p.commitMu.Unlock()
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
	return &cliprender.RenderPublishResult{
		AssetID:       assetID,
		DriveFileID:   pub.FileID,
		DriveLink:     pub.WebViewLink,
		SizeBytes:     size,
		SidecarFileID: sidecarFileID,
		SidecarLink:   sidecarLink,
	}, nil
}
