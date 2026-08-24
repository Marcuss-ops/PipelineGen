package indexing

import (
	"context"
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
)

var ErrProjectionUnavailable = errors.New("qdrant projection writer unavailable")

// ProjectionWriter is the canonical low-level write port for Qdrant
// projections whose collection is selected by the projection owner.
// Collection-specific adapters must depend on this port instead of reaching
// into transport.Client directly.
type ProjectionWriter interface {
	UpsertProjection(ctx context.Context, collection string, points []schema.Point) error
	DeleteProjection(ctx context.Context, collection string, pointIDs []string) error
}

// TransportProjectionWriter is the sole adapter allowed to translate the
// projection write port into Qdrant transport calls. Asset writes continue to
// use IndexWriter's validated path; this adapter covers concept/frame
// projections with their own collection contracts.
type TransportProjectionWriter struct {
	client *transport.Client
}

func NewTransportProjectionWriter(client *transport.Client) *TransportProjectionWriter {
	return &TransportProjectionWriter{client: client}
}

func (w *TransportProjectionWriter) UpsertProjection(ctx context.Context, collection string, points []schema.Point) error {
	if w == nil || w.client == nil {
		return ErrProjectionUnavailable
	}
	return w.client.UpsertPoints(ctx, collection, points)
}

func (w *TransportProjectionWriter) DeleteProjection(ctx context.Context, collection string, pointIDs []string) error {
	if w == nil || w.client == nil {
		return ErrProjectionUnavailable
	}
	return w.client.DeletePoints(ctx, collection, pointIDs)
}
