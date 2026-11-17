// Package indexing — ReindexRequesterHolder is the capability-level
// replacement for the retired Qdrant clipindexer service.
package indexing

import (
	"context"
	"fmt"
)

// IndexRequester is the narrow seam realized by pgmedia.ReindexRequester.
type IndexRequester interface {
	RequestIndex(ctx context.Context, assetID string) error
}

// ReindexRequesterHolder keeps the composition-time injection seam while
// delegating every imperative reindex call to the canonical PostgreSQL
// outbox. It replaces the Qdrant clipindexer service: no Qdrant write and no
// media_assets SQL read happens on this path.
type ReindexRequesterHolder struct {
	enabled   bool
	requester IndexRequester
}

// NewReindexRequesterHolder constructs the holder from the clip-indexer
// compatibility flag. The requester is injected later by the composition
// root, once the media PostgreSQL handle is available.
func NewReindexRequesterHolder(enabled bool) *ReindexRequesterHolder {
	return &ReindexRequesterHolder{enabled: enabled}
}

// SetRequester wires the canonical PG outbox requester.
func (h *ReindexRequesterHolder) SetRequester(r IndexRequester) {
	if h != nil {
		h.requester = r
	}
}

// IsEnabled mirrors the legacy clip-indexer compatibility flag.
func (h *ReindexRequesterHolder) IsEnabled() bool {
	return h != nil && h.enabled
}

// IndexClip enqueues the canonical asset.index.requested event. It fails
// closed when the media plane is not wired.
func (h *ReindexRequesterHolder) IndexClip(ctx context.Context, assetID string) error {
	if h == nil || h.requester == nil {
		return fmt.Errorf("indexing: canonical PostgreSQL media index requester is not wired for %q", assetID)
	}
	return h.requester.RequestIndex(ctx, assetID)
}
