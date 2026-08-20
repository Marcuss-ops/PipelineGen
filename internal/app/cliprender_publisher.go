package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/persistence"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"golang.org/x/sync/errgroup"
)

// clipRenderPublisher is the composition-root adapter for the final render
// boundary. Drive publication goes through delivery.Publisher; the derived
// asset and its location are committed through the canonical AssetCommitter.
type clipRenderPublisher struct {
	drive     delivery.Publisher
	committer persistence.AssetCommitter
	// SQLite has one canonical writer. Render jobs may encode and upload in
	// parallel, but their final asset commits must not overlap.
	commitMu sync.Mutex
}

func (p *clipRenderPublisher) Publish(ctx context.Context, in cliprender.RenderPublishInput) (*cliprender.RenderPublishResult, error) {
	if p == nil || p.drive == nil || p.committer == nil {
		return nil, fmt.Errorf("clip.render publisher: Drive publisher and asset committer are required")
	}
	if in.OutputPath == "" || in.Outcome == nil || in.DriveFolderID == "" {
		return nil, fmt.Errorf("clip.render publisher: output, outcome and destination folder are required")
	}
	contentHash, size, err := sha256File(in.OutputPath)
	if err != nil {
		return nil, fmt.Errorf("hash rendered output: %w", err)
	}
	assetID := "cliprender_" + contentHash[:24]
	filename := assetID + filepath.Ext(in.OutputPath)
	// The video and its deterministic ASS sidecar are independent remote
	// artifacts. Upload them concurrently, but do not return success until
	// both are confirmed; the SQLite commit below remains the single durable
	// completion boundary. This removes the old video-upload -> ASS-upload
	// serialization without weakening fail-closed publication semantics.
	var pub *delivery.PublishResult
	var sidecarFileID, sidecarLink string
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
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
		if publishErr != nil {
			return fmt.Errorf("publish rendered clip: %w", publishErr)
		}
		if result == nil || result.FileID == "" {
			return fmt.Errorf("publish rendered clip: empty Drive result")
		}
		pub = result
		return nil
	})
	if in.Subtitles != nil {
		g.Go(func() error {
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
			if publishErr != nil {
				return fmt.Errorf("publish subtitles sidecar: %w", publishErr)
			}
			if result == nil || result.FileID == "" {
				return fmt.Errorf("publish subtitles sidecar: empty Drive result")
			}
			sidecarFileID, sidecarLink = result.FileID, result.WebViewLink
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	taxonomy, err := mediaregistry.ResolveTaxonomy(mediaregistry.TaxonomyInput{
		AssetID: assetID, Provider: "pipelinegen", MediaType: mediaregistry.MediaVideo,
		AssetKind: mediaregistry.AssetRenderedVideo,
	})
	if err != nil {
		return nil, fmt.Errorf("resolve rendered taxonomy: %w", err)
	}
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
			MimeType: "video/mp4", FileSizeBytes: size, FileHash: contentHash, IsPrimary: true}},
		EmitIndexEvent: true,
	}
	p.commitMu.Lock()
	_, err = p.committer.CommitAsset(ctx, commitRequest)
	p.commitMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("commit rendered asset: %w", err)
	}
	return &cliprender.RenderPublishResult{AssetID: assetID, DriveFileID: pub.FileID, DriveLink: pub.WebViewLink,
		SizeBytes: size, SidecarFileID: sidecarFileID, SidecarLink: sidecarLink}, nil
}

func sha256File(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
