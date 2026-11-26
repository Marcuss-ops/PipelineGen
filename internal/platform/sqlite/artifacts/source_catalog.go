package artifactsinfra

import (
	"context"

	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/artifacts"
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
	// MEDIA LEGACY READ-PLANE DEMOLITION (2026-09-20): calls the canonical
	// AssetStoreSQLite lookup directly. The ClipsRepository.GetByDriveFileID
	// alias that used to sit here was deleted with clips_resolution.go; media
	// rows reach the catalog through the canonical writer/committer, and the
	// resolver-side media reads now live on pgmedia.MediaSearcher.
	return a.inner.GetClipByDriveFileID(ctx, id)
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
	return assets.VoiceoverRecordToAsset(rec), nil
}
func (a *artifactVoiceoverSourceAdapter) GetByDriveFileID(ctx context.Context, id string) (*asset.Asset, error) {
	if a.inner == nil {
		return nil, nil
	}
	rec, err := a.inner.GetByDriveFileID(ctx, id)
	if err != nil || rec == nil {
		return nil, err
	}
	return assets.VoiceoverRecordToAsset(rec), nil
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
	return assets.ImageAssetToAsset(img), nil
}
func (a *artifactImagesSourceAdapter) GetByDriveFileID(ctx context.Context, id string) (*asset.Asset, error) {
	if a.inner == nil {
		return nil, nil
	}
	img, err := a.inner.GetByDriveFileID(ctx, id)
	if err != nil || img == nil {
		return nil, err
	}
	return assets.ImageAssetToAsset(img), nil
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

var _ artifacts.SourceRepo = (*artifactClipsSourceAdapter)(nil)
var _ artifacts.SourceRepo = (*artifactVoiceoverSourceAdapter)(nil)
var _ artifacts.SourceRepo = (*artifactImagesSourceAdapter)(nil)
