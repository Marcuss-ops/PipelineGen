// Package monitor — category channel service port.
package assets

import (
	"context"

	channels "github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// CategoryChannelsPort is the narrow channel-service surface consumed by monitor orchestration.
type CategoryChannelsPort interface {
	ListEnabled(ctx context.Context) ([]*asset.CategoryChannel, error)
	MarkChecked(ctx context.Context, cmd channels.MarkCheckedCommand) error
	UpdateCursor(ctx context.Context, cmd channels.UpdateCursorCommand) error
}
