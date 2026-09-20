package clipindexer

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/event"
)

// IndexAsset is the compatibility entry point of MediaIndexer.
//
// POSTGRES-MEDIA-CUTOVER (2026-09-20, MEDIA LEGACY READ-PLANE DEMOLITION):
// this method is now a PURE delegator onto the canonical media event plane.
// In canonical PostgreSQL media mode composition wires canonicalIndexRequester
// (pgmedia.ReindexRequester) and every call enqueues the canonical
// asset.index.requested event in the PostgreSQL outbox. PostgresIndexWorker is
// the ONLY writer of media_embeddings and of the terminal INDEXED state, so
// this package no longer performs ANY inline Qdrant write or media_assets SQL
// read.
//
// The legacy SQLite -> Qdrant implementation (indexAsset / tryFastPath /
// finalizeIndex / indexViaAPI / shouldSkipByName / computeContentHash) was
// DELETED in the same change, together with its inventory entries: it was
// unreachable in PostgreSQL mode and keeping it alive was the only reason the
// Qdrant media read plane still existed.
//
// Fail-closed (godlike/07): a nil canonicalIndexRequester means the media index
// plane is not wired. Returning a typed error is correct — a silent no-op would
// record a media index request as done while nothing was enqueued.
func (s *Service) IndexAsset(ctx context.Context, assetID string) error {
	if s == nil || s.canonicalIndexRequester == nil {
		return fmt.Errorf("clipindexer: canonical PostgreSQL media index requester is not wired for asset %q (the media index plane is owned by %s)", assetID, event.AssetIndexRequested)
	}
	return s.canonicalIndexRequester.RequestIndex(ctx, assetID)
}

// IndexClip is the legacy compatible wrapper. It delegates to the canonical
// IndexAsset so existing clip-vocabulary callers converge without a rename.
func (s *Service) IndexClip(ctx context.Context, clipID string) error {
	return s.IndexAsset(ctx, clipID)
}
