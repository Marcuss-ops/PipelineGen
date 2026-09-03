// Package adapters — canonical_committers.go: repository to canonical writer
// bridge adapters. Repositories keep read/detail-table responsibilities while
// every media_assets mutation routes through the one production writer.
package adapters

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediacommit"
	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesrepo"
)

// WireCanonicalImageCommitter registers both creation and mutation views of
// the SAME canonical writer on ImagesRepository. Missing mutation capability
// is left unwired so write methods fail closed rather than falling back to SQL.
func WireCanonicalImageCommitter(repo *imagesrepo.ImagesRepository, committer persistence.AssetCommitter) {
	if repo == nil || committer == nil {
		return
	}
	if mutator, ok := committer.(persistence.AssetMutator); ok {
		repo.SetCanonicalMutator(mutator)
	}
	repo.SetCanonicalCommitter(func(ctx context.Context, img *detail.ImageAsset) (int64, error) {
		if img == nil {
			return 0, fmt.Errorf("canonical image commit: image is nil")
		}
		assetID := strings.TrimSpace(img.Hash)
		if assetID == "" {
			assetID = "img_" + fmt.Sprint(img.CreatedAt.UnixNano())
		}
		name := strings.TrimSpace(img.Description)
		if name == "" {
			name = filepath.Base(img.PathRel)
		}
		if name == "" || name == "." {
			name = assetID
		}
		filename := filepath.Base(img.PathRel)
		if filename == "" || filename == "." {
			filename = assetID + ".image"
		}
		tagsJSON, _ := json.Marshal(img.Tags)
		ref := firstImageRef(img.DriveFileID, img.SourceURL, assetID)
		kind := capregistry.AssetWebImage
		if img.Origin == detail.ImageOriginGenerated {
			kind = capregistry.AssetAIImage
		}
		if img.Origin == "" {
			kind = capregistry.AssetWebImage
		}
		taxonomy, taxErr := capregistry.ResolveTaxonomy(capregistry.TaxonomyInput{
			AssetID: assetID, Provider: "image", MediaType: capregistry.MediaImage, AssetKind: kind,
		})
		if taxErr != nil {
			return 0, fmt.Errorf("canonical image commit: resolve taxonomy: %w", taxErr)
		}
		contentHash := ""
		if isSHA256(img.Hash) {
			contentHash = img.Hash
		}
		request := mediacommit.CommitMediaAssetRequest{
			Asset: mediacommit.AssetDraft{
				AssetID: assetID, Source: "image", Name: name, Filename: filename,
				MediaType: "image", ContentHash: contentHash, LifecycleState: "STAGING",
				LocalPath: img.LocalPath, FolderPath: img.PathRel, SourceURL: img.SourceURL,
				Title: name, Metadata: persistence.TypedMetadata{Extra: map[string]any{
					"subject_id": img.SubjectID, "license": img.License,
					"quality_score": img.QualityScore, "error": img.Error,
				}},
				Image: &mediacommit.ImageDraft{URL: img.SourceURL, TagsJSON: string(tagsJSON), TagsNorm: normalizeImageTags(img.Tags), Width: img.Width, Height: img.Height, RelativePath: img.PathRel, Origin: string(img.Origin), Provider: string(img.Provider)},
			},
			Source:   mediacommit.AssetSourceDraft{SourceType: "image", SourceURI: ref, SourceVersion: ref, IsPrimary: true},
			Taxonomy: taxonomy, Content: optionalImageContent(contentHash),
			IndexPolicy: mediacommit.IndexPolicy{Indexable: true}, Actor: "image-repository",
		}
		// MEDIA DEMOLITION (September 2026): the SQLite fast-path type
		// assertion is removed — the canonical media committer (PostgreSQL
		// since the demolition) exposes CommitMediaAsset(ctx,
		// mediacommit.CommitMediaAssetRequest) directly.
		if canonical, ok := committer.(mediacommit.MediaCommitter); ok {
			_, err := canonical.CommitMediaAsset(ctx, request)
			return 0, err
		}
		_, err := committer.CommitAsset(ctx, persistence.CommitRequest{
			AssetID: assetID, Source: "image", Name: name, Filename: filename,
			MediaType: "image", ContentHash: contentHash, LifecycleState: "STAGING",
			LocalPath: img.LocalPath, FolderPath: img.PathRel, SourceURL: img.SourceURL,
			Title: name, EmitIndexEvent: true, AssetVersion: ref, Taxonomy: taxonomy,
		})
		return 0, err
	})
}

// WireCanonicalAssetStore registers the compatibility Save/Delete surfaces on
// AssetStoreSQLite. They are thin delegators only; the repository contains no
// media_assets write SQL.
func WireCanonicalAssetStore(store *sqassets.AssetStoreSQLite, committer persistence.AssetCommitter) {
	if store == nil || committer == nil {
		return
	}
	store.SetCanonicalSave(func(ctx context.Context, details *asset.Details) error {
		if details == nil || details.Asset == nil {
			return fmt.Errorf("canonical asset commit: details are required")
		}
		a := details.Asset
		ref := firstImageRef(a.MetadataSourceVideoID(), a.SourceURL, a.ID)
		mediaType := string(a.MediaType)
		if mediaType == "" {
			mediaType = "video"
		}
		lifecycle := string(a.LifecycleState)
		if lifecycle == "" {
			lifecycle = "ACTIVE"
		}
		filename := a.Filename
		if filename == "" {
			filename = a.ID + ".asset"
		}
		metadata := persistence.TypedMetadata{
			Title:          a.Title(),
			Description:    a.Description(),
			SourceVersion:  a.GetMetadataString("source_version"),
			SourceProvider: a.MetadataSourceProvider(),
			SourceVideoID:  a.MetadataSourceVideoID(),
			Tags:           append([]string(nil), a.Tags...),
			Category:       a.Category,
			Extra:          a.Metadata,
		}
		if metadata.Title == "" {
			metadata.Title = a.Name
		}
		_, err := committer.CommitAsset(ctx, persistence.CommitRequest{
			AssetID: a.ID, Source: string(a.Source), Name: a.Name, Filename: filename,
			MediaType: mediaType, Category: a.Category, GroupName: a.Group, DurationMs: a.Duration.Milliseconds(),
			ContentHash: a.ContentHash(), SearchText: a.SearchText, LifecycleState: lifecycle,
			LocalPath: a.LocalPath(), FolderID: a.FolderID(), FolderPath: a.FolderPath(),
			ThumbnailURL: a.ThumbnailURL, DownloadLink: a.DownloadLink(), SourceURL: a.SourceURL, Title: metadata.Title,
			SourceProvider: a.MetadataSourceProvider(), SourceVideoID: a.MetadataSourceVideoID(),
			Metadata: metadata, Locations: assetLocationsFromAsset(a),
			IndexState: a.GetMetadataString("index_state"), AssetVersion: ref,
			EmitIndexEvent: false,
		})
		return err
	})
	if mutator, ok := committer.(persistence.AssetMutationCommitter); ok {
		store.SetCanonicalDelete(func(ctx context.Context, id string) error {
			state := "DELETED"
			deletedAt := time.Now().UTC().Format(time.RFC3339Nano)
			return mutator.UpdateLifecycle(ctx, id, state, deletedAt, deletedAt)
		})
	}
}

func assetLocationsFromAsset(a *asset.Asset) []asset.LocationCommit {
	if a == nil {
		return nil
	}
	locations := make([]asset.LocationCommit, 0, 2)
	if localPath := a.LocalPath(); localPath != "" {
		locations = append(locations, asset.LocationCommit{
			Kind: "local", Provider: "local", URI: localPath,
			MimeType: string(a.MediaType), LegacyFileMD5: a.LegacyFileMD5(),
		})
	}
	if fileID, link := a.DriveFileID(), a.DriveLink(); fileID != "" || link != "" {
		uri := link
		if fileID != "" {
			uri = "drive://" + fileID
		}
		locations = append(locations, asset.LocationCommit{
			Kind: "drive", Provider: "google_drive", ExternalID: fileID,
			URI: uri, WebViewLink: link, DownloadURL: a.DownloadLink(),
			MimeType: string(a.MediaType), LegacyFileMD5: a.LegacyFileMD5(), IsPrimary: true,
		})
	}
	return locations
}

func optionalImageContent(hash string) *mediacommit.ContentIdentity {
	if !isSHA256(hash) {
		return nil
	}
	return &mediacommit.ContentIdentity{ContentSHA256: hash}
}

func isSHA256(value string) bool {
	if len(value) != digest.SHA256HexLength {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func firstImageRef(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "unknown"
}

func normalizeImageTags(tags []string) string {
	values := make([]string, 0, len(tags))
	for _, tag := range tags {
		if value := strings.TrimSpace(strings.ToLower(tag)); value != "" {
			values = append(values, value)
		}
	}
	return strings.Join(values, " ")
}
