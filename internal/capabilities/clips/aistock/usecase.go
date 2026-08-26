// Package aistock — use case for AI-generated stock clip ingestion.
package aistock

import (
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/visualanalysis"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/capabilities/clips"
	appupload "github.com/Marcuss-ops/PipelineGen/internal/capabilities/clips/upload"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"
)

// UseCase orchestrates the ingestion of an AI-generated stock clip from a
// visual analysis document and a Google Drive video file.
type UseCase struct {
	driveReader DriveReaderPort
	artifact    appupload.ArtifactServicePort
	dispatcher  appclips.ClipIndexDispatcherPort
	log         *zap.Logger
}

// UseCaseDeps carries every dependency the use case needs.
type UseCaseDeps struct {
	DriveReader DriveReaderPort
	Artifact    appupload.ArtifactServicePort
	Dispatcher  appclips.ClipIndexDispatcherPort
	Log         *zap.Logger
}

// NewUseCase constructs the canonical AI stock ingestion use case.
func NewUseCase(d UseCaseDeps) (*UseCase, error) {
	if d.DriveReader == nil {
		return nil, fmt.Errorf("aistock.NewUseCase: DriveReader is required")
	}
	if d.Artifact == nil {
		return nil, fmt.Errorf("aistock.NewUseCase: Artifact is required")
	}
	if d.Dispatcher == nil {
		return nil, fmt.Errorf("aistock.NewUseCase: Dispatcher is required")
	}
	if d.Log == nil {
		d.Log = zap.NewNop()
	}
	return &UseCase{
		driveReader: d.DriveReader,
		artifact:    d.Artifact,
		dispatcher:  d.Dispatcher,
		log:         d.Log,
	}, nil
}

// CreateAIStockResult is returned by Execute on success.
type CreateAIStockResult struct {
	ClipID      string
	DriveFileID string
	LocalPath   string
}

// Execute parses the visual analysis document, downloads the referenced
// video from Google Drive, stages it locally, builds the canonical asset,
// and dispatches it for indexing.
func (uc *UseCase) Execute(ctx context.Context, cmd CreateAIStockCommand) (*CreateAIStockResult, error) {
	if cmd.DocumentJSON == "" {
		return nil, fmt.Errorf("aistock: document is required")
	}
	if cmd.DriveURL == "" {
		return nil, fmt.Errorf("aistock: drive_url is required")
	}

	// 1. Parse and validate the visual analysis document.
	doc, err := visualanalysis.Parse([]byte(cmd.DocumentJSON))
	if err != nil {
		return nil, fmt.Errorf("aistock: parse document: %w", err)
	}
	if doc.Asset.ProposedAssetID == "" {
		return nil, fmt.Errorf("aistock: asset.proposed_asset_id is required")
	}

	// 2. Resolve the Drive file ID from the supplied reference.
	fileID, err := visualanalysis.DriveFileID(cmd.DriveURL)
	if err != nil {
		return nil, fmt.Errorf("aistock: invalid drive reference: %w", err)
	}

	// 3. Fetch Drive metadata so we can use the real filename.
	meta, err := uc.driveReader.GetFileMeta(ctx, fileID)
	if err != nil {
		return nil, fmt.Errorf("aistock: get drive file meta: %w", err)
	}
	if meta == nil {
		return nil, fmt.Errorf("aistock: drive file meta is nil")
	}

	// 4. Download the video bytes.
	body, contentType, err := uc.driveReader.DownloadFile(ctx, fileID)
	if err != nil {
		return nil, fmt.Errorf("aistock: download drive file: %w", err)
	}
	defer body.Close()
	if contentType == "" {
		contentType = "video/mp4"
	}
	if !isVideoContentType(contentType, meta.Name) {
		return nil, fmt.Errorf("aistock: drive file has non-video content type %q", contentType)
	}

	// 5. Stage the file in the artifact store.
	artifactRef, err := uc.artifact.CreateAndVerify(ctx, appupload.ArtifactCreateInput{
		ID:       doc.Asset.ProposedAssetID,
		Kind:     "video",
		MimeType: contentType,
		Reader:   body,
	})
	if err != nil {
		return nil, fmt.Errorf("aistock: stage artifact: %w", err)
	}

	localPath, err := uc.artifact.LocalPath(ctx, artifactRef.ID)
	if err != nil {
		return nil, fmt.Errorf("aistock: resolve artifact local path: %w", err)
	}

	// 6. Build the canonical asset.
	if artifactRef.SHA256 == "" {
		return nil, fmt.Errorf("aistock: artifact SHA256 is empty")
	}
	clip, err := uc.buildAsset(doc, meta.Name, fileID, localPath, artifactRef.SHA256)
	if err != nil {
		return nil, fmt.Errorf("aistock: build asset: %w", err)
	}

	// 7. Dispatch atomic UPSERT + outbox event.
	contentHash := artifactRef.SHA256
	if err := uc.dispatcher.EnqueueAndIndex(ctx, clip, contentHash); err != nil {
		return nil, fmt.Errorf("aistock: enqueue and index: %w", err)
	}

	uc.log.Info("ai stock clip ingested",
		zap.String("clip_id", clip.ID),
		zap.String("drive_file_id", fileID),
		zap.String("local_path", localPath),
	)

	return &CreateAIStockResult{
		ClipID:      clip.ID,
		DriveFileID: fileID,
		LocalPath:   localPath,
	}, nil
}

// buildAsset maps the validated visual analysis document to a domain asset.
func (uc *UseCase) buildAsset(doc visualanalysis.Document, filename, fileID, localPath, sha256 string) (*asset.Asset, error) {
	now := time.Now().UTC()
	clip := &asset.Asset{
		ID:         doc.Asset.ProposedAssetID,
		Name:       doc.Asset.Title,
		Filename:   filename,
		Source:     detail.SourceAIGenerated,
		Category:   visualanalysis.FolderCategory(doc.Asset.FolderPath),
		Group:      doc.Asset.FolderPath,
		MediaType:  asset.MediaType(doc.Asset.MediaType),
		SearchText: uc.composeSearchText(doc),
		Duration:   time.Duration(doc.Asset.DurationMs) * time.Millisecond,
		Tags:       append([]string(nil), doc.Visual.Subjects...),
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	clip.SetLocalPath(localPath)
	clip.SetLegacyFileMD5(sha256)
	clip.SetFolderPath(doc.Asset.FolderPath)
	clip.SetDriveFileID(fileID)
	clip.SetDriveLink(canonicalDriveWebURL(fileID))
	clip.SetDownloadLink(canonicalDriveDownloadURL(fileID))

	// Persist the AI stock metadata and visual analysis inside the asset row.
	aiMeta := doc.Metadata()
	aiMetaJSON, err := json.Marshal(aiMeta)
	if err != nil {
		return nil, fmt.Errorf("marshal ai stock metadata: %w", err)
	}
	clip.SetMetadataString("ai_stock_metadata", string(aiMetaJSON))
	visual := doc.VisualAnalysis()
	visualJSON, err := json.Marshal(visual)
	if err != nil {
		return nil, fmt.Errorf("marshal visual analysis: %w", err)
	}
	clip.SetMetadataString("visual_analysis", string(visualJSON))

	return clip, nil
}

// composeSearchText builds the canonical search text for the asset.
func (uc *UseCase) composeSearchText(doc visualanalysis.Document) string {
	if doc.SearchText != "" {
		return doc.SearchText
	}
	parts := []string{doc.Asset.Title, doc.Visual.SummaryEN, doc.Visual.SummaryIT}
	parts = append(parts, doc.Visual.Subjects...)
	parts = append(parts, doc.Visual.Environment...)
	parts = append(parts, doc.Visual.Actions...)
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return joinUnique(out, ". ")
}

// canonicalDriveWebURL returns the canonical Google Drive web view link.
func canonicalDriveWebURL(fileID string) string {
	return fmt.Sprintf("https://drive.google.com/file/d/%s/view", fileID)
}

// canonicalDriveDownloadURL returns the canonical Google Drive download link.
func canonicalDriveDownloadURL(fileID string) string {
	return fmt.Sprintf("https://drive.google.com/uc?export=download&id=%s", fileID)
}

// joinUnique joins non-empty strings with the given separator, deduplicating
// entries while preserving order.
func joinUnique(parts []string, sep string) string {
	seen := make(map[string]struct{})
	var out []string
	for _, p := range parts {
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return strings.Join(out, sep)
}

// isVideoContentType returns true for MIME types the pipeline treats as video.
// Drive sometimes reports video files as application/octet-stream; in that
// case we fall back to checking the file extension.
func isVideoContentType(ct, filename string) bool {
	if strings.HasPrefix(ct, "video/") {
		return true
	}
	if ct == "application/octet-stream" {
		return hasVideoExtension(filename)
	}
	return false
}

// hasVideoExtension returns true if filename has a known video extension.
func hasVideoExtension(filename string) bool {
	if filename == "" {
		return false
	}
	lower := strings.ToLower(filename)
	for _, ext := range []string{".mp4", ".mov", ".avi", ".mkv", ".webm", ".m4v", ".mpeg", ".mpg"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}
