package assetregistry

import "context"

type Registry interface {
	UpsertMedia(ctx context.Context, rec *MediaRecord) error
	GetMedia(ctx context.Context, id string) (*MediaRecord, error)
	DeleteMedia(ctx context.Context, id string) error
	GetAllWithDriveFileID(ctx context.Context) ([]*MediaRecord, error)
	FindByPHash(ctx context.Context, phash string) (string, error)
}
