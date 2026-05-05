package artlist

import (
	"context"

	"velox/go-master/internal/service/mediaasset"
)

type MediaProcessor interface {
	DownloadProcessUpload(ctx context.Context, input mediaasset.AssetInput) (*mediaasset.AssetResult, error)
}
