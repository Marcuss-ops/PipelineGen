// Package qdrant — identity_audit.go: the Qdrant point-identity half of the
// identity audit (item 12).
//
// The projection invariant is 1 canonical asset = 1 point. CountDuplicateAssetPoints
// scrolls the active collection and counts the number of extra points (beyond
// the first occurrence) that share the same canonical payload.asset_id. The
// result must be ZERO before the alias switch: a non-zero count means the
// point-ID / asset relationship drifted (e.g. a legacy writer emitted two
// points for one asset).
//
// godlike/07 fail-closed: the audit scrolls to completion and never guesses.
// Any scroll error aborts with a non-nil error so a partial count can never
// be mistaken for a clean projection.
package verification

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

// ScrollPager is the minimal Qdrant scroll surface the identity audit needs.
// *transport.Client satisfies it directly.
type ScrollPager interface {
	ScrollPoints(ctx context.Context, collection, offset string, limit int, filter map[string]any) (*schema.ScrollResult, error)
}

// CountDuplicateAssetPoints scrolls the collection and returns the number of
// extra points sharing a canonical payload.asset_id (the first occurrence of
// each asset_id is the canonical point; every subsequent point with the same
// asset_id is a duplicate). The result must be zero for a valid projection.
func CountDuplicateAssetPoints(ctx context.Context, pager ScrollPager, collection string) (int, error) {
	if pager == nil {
		return 0, fmt.Errorf("identity audit: nil scroll pager")
	}
	seen := make(map[string]struct{})
	duplicates := 0
	offset := ""
	const pageSize = 500

	for {
		res, err := pager.ScrollPoints(ctx, collection, offset, pageSize, nil)
		if err != nil {
			return 0, fmt.Errorf("identity audit: scroll %q: %w", collection, err)
		}
		for _, pt := range res.Points {
			assetID, ok := pt.Payload["asset_id"].(string)
			if !ok || assetID == "" {
				// Points without a canonical asset_id are a decode failure,
				// not a duplicate; the reindex verifier owns that gate.
				continue
			}
			if _, exists := seen[assetID]; exists {
				duplicates++
				continue
			}
			seen[assetID] = struct{}{}
		}
		if res.NextOffset == "" {
			break
		}
		offset = res.NextOffset
	}
	return duplicates, nil
}
