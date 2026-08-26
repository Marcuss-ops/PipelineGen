package images

import (
	"context"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// ImageRepository is the application-owned persistence port for image assets.
// SQLite and other storage implementations are wired at the composition root.
type ImageRepository interface {
	GetImageByHash(context.Context, string) (*detail.ImageAsset, error)
	GetByDriveFileID(context.Context, string) (*detail.ImageAsset, error)
	ListImagesBySubject(context.Context, string) ([]detail.ImageAsset, error)
	ListImagesByOrigin(context.Context, detail.ImageOrigin, int) ([]detail.ImageAsset, error)
	ListAll(context.Context) ([]*detail.ImageAsset, error)
	AddImage(context.Context, *detail.ImageAsset) (int64, error)
	Delete(context.Context, any) error
	UpdateImageMetadata(context.Context, string, string) error
	UpdateDriveDelivery(context.Context, string, string, string, string, string) error
	UpsertRetrievedDetails(context.Context, *detail.RetrievedImageDetail) error
	GetSubjectBySlugOrAlias(context.Context, string) (*asset.Subject, error)
	CreateSubject(context.Context, *asset.Subject) (int64, error)
}
