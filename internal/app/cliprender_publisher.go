package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/persistence"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

// clipRenderPublisher is the composition-root adapter for the final render
// boundary. Drive publication goes through delivery.Publisher; the derived
// asset and its location are committed through the canonical AssetCommitter.
type clipRenderPublisher struct {
	drive     delivery.Publisher
	committer persistence.AssetCommitter
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
	pub, err := p.drive.Publish(ctx, delivery.PublishRequest{
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
	if err != nil {
		return nil, fmt.Errorf("publish rendered clip: %w", err)
	}
	if pub == nil || pub.FileID == "" {
		return nil, fmt.Errorf("publish rendered clip: empty Drive result")
	}

	var sidecarFileID, sidecarLink string
	if in.Subtitles != nil {
		sidecar, sidecarErr := p.drive.Publish(ctx, delivery.PublishRequest{
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
		if sidecarErr != nil {
			return nil, fmt.Errorf("publish subtitles sidecar: %w", sidecarErr)
		}
		if sidecar == nil || sidecar.FileID == "" {
			return nil, fmt.Errorf("publish subtitles sidecar: empty Drive result")
		}
		sidecarFileID, sidecarLink = sidecar.FileID, sidecar.WebViewLink
	}

	taxonomy, err := mediaregistry.ResolveTaxonomy(mediaregistry.TaxonomyInput{
		AssetID: assetID, Provider: "pipelinegen", MediaType: mediaregistry.MediaVideo,
		AssetKind: mediaregistry.AssetRenderedVideo,
	})
	if err != nil {
		return nil, fmt.Errorf("resolve rendered taxonomy: %w", err)
	}
	durationMS := int64(in.Outcome.DurationSec * 1000)
	_, err = p.committer.CommitAsset(ctx, persistence.AssetCommitRequest{
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
	})
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
