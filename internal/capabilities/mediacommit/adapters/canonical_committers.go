// Package adapters — canonical_committers.go: image/asset-store to
// mediacommit bridge adapters.
//
// The composition root keeps newCanonicalAssetCommitter (the factory
// that constructs the SQLiteMediaCommitter concrete); this package owns
// the business mapping that turns a platform repository's canonical
// write hook into a mediacommit commit request:
//
//   - WireCanonicalImageCommitter — ImagesRepository hook: maps an
//     *detail.ImageAsset into a mediacommit.CommitMediaAssetRequest
//     (taxonomy resolution, tag normalization, content identity).
//   - WireCanonicalAssetStore     — AssetStoreSQLite hook: maps an
//     *asset.Details into a persistence.CommitRequest.
//
// Both are fail-closed: a nil repository/committer is a no-op registration
// (the hook simply stays unset), mirroring the previous composition-root
// behaviour.
package adapters

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediacommit"
	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesregistry"
	sqmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesrepo"
)

// WireCanonicalImageCommitter registers the canonical image commit path on
// the ImagesRepository: every image write converges on mediacommit instead of
// a repo-local insert.
func WireCanonicalImageCommitter(repo *imagesrepo.ImagesRepository, committer persistence.AssetCommitter) {
	if repo == nil || committer == nil {
		return
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
			AssetID:   assetID,
			Provider:  "image",
			MediaType: capregistry.MediaImage,
			AssetKind: kind,
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
			Source:      mediacommit.AssetSourceDraft{SourceType: "image", SourceURI: ref, SourceVersion: ref, IsPrimary: true},
			Taxonomy:    taxonomy,
			Content:     optionalImageContent(contentHash),
			IndexPolicy: mediacommit.IndexPolicy{Indexable: true},
			Actor:       "image-repository",
		}
		if canonical, ok := committer.(*sqmedia.SQLiteMediaCommitter); ok {
			_, err := canonical.CommitMediaAsset(ctx, request)
			return 0, err
		}
		result, err := committer.CommitAsset(ctx, persistence.CommitRequest{AssetID: assetID, Source: "image", Name: name, Filename: filename, MediaType: "image", ContentHash: contentHash, LifecycleState: "STAGING", LocalPath: img.LocalPath, FolderPath: img.PathRel, SourceURL: img.SourceURL, Title: name, EmitIndexEvent: true, AssetVersion: ref})
		_ = result
		return 0, err
	})
}

// WireCanonicalAssetStore registers the canonical save path on the
// AssetStoreSQLite: details commits converge on the AssetCommitter port.
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
		_, err := committer.CommitAsset(ctx, persistence.CommitRequest{
			AssetID: a.ID, Source: string(a.Source), Name: a.Name, Filename: a.Filename,
			MediaType: mediaType, Category: a.Category, DurationMs: a.Duration.Milliseconds(),
			ContentHash: a.LegacyFileMD5(), SearchText: a.SearchText, LifecycleState: string(a.LifecycleState),
			ThumbnailURL: a.ThumbnailURL, SourceURL: a.SourceURL, Title: a.Name,
			Metadata:   persistence.TypedMetadata{Extra: a.Metadata},
			IndexState: a.GetMetadataString("index_state"), AssetVersion: ref,
			EmitIndexEvent: false,
		})
		return err
	})
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
