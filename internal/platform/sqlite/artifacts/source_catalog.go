package artifactsinfra

import (
	"context"
	"path/filepath"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	assets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesrepo"
)

type artifactClipsSourceAdapter struct{ inner *assets.ClipsRepository }

func (a *artifactClipsSourceAdapter) Get(ctx context.Context, id string) (*asset.Asset, error) {
	if a.inner == nil {
		return nil, nil
	}
	return a.inner.Get(ctx, id)
}
func (a *artifactClipsSourceAdapter) GetByDriveFileID(ctx context.Context, id string) (*asset.Asset, error) {
	if a.inner == nil {
		return nil, nil
	}
	return a.inner.GetByDriveFileID(ctx, id)
}
func (a *artifactClipsSourceAdapter) Delete(ctx context.Context, id string) error {
	if a.inner == nil {
		return nil
	}
	return a.inner.DeleteClip(ctx, id)
}

type artifactVoiceoverSourceAdapter struct{ inner *assets.VoiceoversRepository }

func (a *artifactVoiceoverSourceAdapter) Get(ctx context.Context, id string) (*asset.Asset, error) {
	if a.inner == nil {
		return nil, nil
	}
	rec, err := a.inner.GetByID(ctx, id)
	if err != nil || rec == nil {
		return nil, err
	}
	return voiceoverRecordToAsset(rec), nil
}
func (a *artifactVoiceoverSourceAdapter) GetByDriveFileID(ctx context.Context, id string) (*asset.Asset, error) {
	if a.inner == nil {
		return nil, nil
	}
	rec, err := a.inner.GetByDriveFileID(ctx, id)
	if err != nil || rec == nil {
		return nil, err
	}
	return voiceoverRecordToAsset(rec), nil
}
func (a *artifactVoiceoverSourceAdapter) Delete(ctx context.Context, id string) error {
	if a.inner == nil {
		return nil
	}
	return a.inner.Delete(ctx, id)
}

type artifactImagesSourceAdapter struct{ inner *imagesrepo.ImagesRepository }

func (a *artifactImagesSourceAdapter) Get(ctx context.Context, id string) (*asset.Asset, error) {
	if a.inner == nil {
		return nil, nil
	}
	img, err := a.inner.GetByID(ctx, id)
	if err != nil || img == nil {
		return nil, err
	}
	return imageAssetToAsset(img), nil
}
func (a *artifactImagesSourceAdapter) GetByDriveFileID(ctx context.Context, id string) (*asset.Asset, error) {
	if a.inner == nil {
		return nil, nil
	}
	img, err := a.inner.GetByDriveFileID(ctx, id)
	if err != nil || img == nil {
		return nil, err
	}
	return imageAssetToAsset(img), nil
}
func (a *artifactImagesSourceAdapter) Delete(ctx context.Context, id string) error {
	if a.inner == nil {
		return nil
	}
	return a.inner.Delete(ctx, id)
}

// NewArtifactSourceCatalog is the infrastructure composition adapter for the
// application-owned SourceCatalog. Concrete SQLite repositories never cross
// into application code.
func NewArtifactSourceCatalog(artlist, clips, stock *assets.ClipsRepository, voiceover *assets.VoiceoversRepository, images *imagesrepo.ImagesRepository) (*artifacts.SourceCatalog, error) {
	if artlist == nil || clips == nil || stock == nil || voiceover == nil || images == nil {
		return nil, artifacts.ErrSourceCatalogDependencyUnavailable
	}
	return artifacts.NewSourceCatalog(
		&artifactClipsSourceAdapter{inner: artlist},
		&artifactClipsSourceAdapter{inner: clips},
		&artifactClipsSourceAdapter{inner: stock},
		&artifactVoiceoverSourceAdapter{inner: voiceover},
		&artifactImagesSourceAdapter{inner: images},
	)
}

func voiceoverRecordToAsset(rec *assets.Record) *asset.Asset {
	if rec == nil {
		return nil
	}
	name := rec.Filename
	if name == "" {
		name = rec.TextPreview
		if len(name) > 50 {
			name = name[:50]
		}
	}
	clip := &asset.Asset{ID: rec.ID, Name: name, Filename: rec.Filename, Source: "voiceover", MediaType: "audio", SearchTerms: []string{rec.TextPreview}, CreatedAt: rec.CreatedAt, UpdatedAt: rec.UpdatedAt}
	clip.SetFolderID(rec.FolderID)
	clip.SetFolderPath(rec.FolderPath)
	clip.SetDriveLink(rec.DriveLink)
	clip.SetDriveFileID(rec.DriveFileID)
	clip.SetDownloadLink(rec.DownloadLink)
	clip.SetLegacyFileMD5(rec.LegacyFileMD5)
	clip.SetLocalPath(rec.LocalPath)
	clip.SetMetadataJSON(rec.Metadata)
	return clip
}

func imageAssetToAsset(item *asset.ImageAsset) *asset.Asset {
	if item == nil {
		return nil
	}
	name := item.Description
	if name == "" {
		name = filepath.Base(item.PathRel)
	}
	id := item.SlugID
	if id == "" {
		id = item.Hash
	}
	clip := &asset.Asset{ID: id, Name: name, Filename: filepath.Base(item.PathRel), Source: "images", MediaType: "image", Tags: item.Tags, SearchTerms: []string{item.Description}, CreatedAt: item.CreatedAt, UpdatedAt: item.CreatedAt}
	clip.SetDriveLink(item.SourceURL)
	clip.SetDriveFileID(item.DriveFileID)
	clip.SetLegacyFileMD5(item.Hash)
	clip.SetLocalPath(item.PathRel)
	return clip
}

var _ artifacts.SourceRepo = (*artifactClipsSourceAdapter)(nil)
var _ artifacts.SourceRepo = (*artifactVoiceoverSourceAdapter)(nil)
var _ artifacts.SourceRepo = (*artifactImagesSourceAdapter)(nil)
