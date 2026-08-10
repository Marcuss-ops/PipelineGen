package images

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ImageRepository is the application-owned persistence port for image assets.
// SQLite and other storage implementations are wired at the composition root.
type ImageRepository interface {
	GetImageByHash(context.Context, string) (*asset.ImageAsset, error)
	GetByDriveFileID(context.Context, string) (*asset.ImageAsset, error)
	ListImagesBySubject(context.Context, string) ([]asset.ImageAsset, error)
	ListImagesByOrigin(context.Context, asset.ImageOrigin, int) ([]asset.ImageAsset, error)
	ListAll(context.Context) ([]*asset.ImageAsset, error)
	AddImage(context.Context, *asset.ImageAsset) (int64, error)
	Delete(context.Context, any) error
	UpdateImageMetadata(context.Context, string, string) error
	UpdateOrigin(context.Context, string, string, string) error
	UpdateDriveDelivery(context.Context, string, string, string, string, string) error
	UpsertRetrievedDetails(context.Context, *asset.RetrievedImageDetail) error
	GetSubjectBySlugOrAlias(context.Context, string) (*asset.Subject, error)
	CreateSubject(context.Context, *asset.Subject) (int64, error)
}
