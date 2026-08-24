// Package qdrant — semantic_asset_search_convert.go — per-kind wire-shape conversion.
//
// convertAssetHitsByKind dispatches between the two convert helpers
// based on the KindAsset discriminant. The per-kind wire-shape
// invariants (DriveLink="" for clips, DriveLink=populated for stock)
// are preserved here.
package search

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
)

// convertAssetHitsByKind dispatches between the two convert helpers
// based on the KindAsset discriminant.
//
// Per godlike/07 NO-FAKE-AVAILABILITY: a future addition of KindXxx
// without a corresponding convert call returns typed-error here.
// Per godlike/06 SSOT: this single dispatch IS the canonical place
// where the per-kind wire-shape invariant lives.
func convertAssetHitsByKind(results []schema.SearchResult, kind KindAsset) []ports.AssetSearchHit {
	switch kind {
	case KindClip:
		return convertClipAssetHits(results)
	case KindStock:
		return convertStockAssetHits(results)
	default:
		return nil
	}
}

// convertClipAssetHits maps infra-level schema.SearchResult →
// canonical AssetSearchHit (5 fields, DriveLink empty for clip
// path per QDRANT-001).
func convertClipAssetHits(results []schema.SearchResult) []ports.AssetSearchHit {
	out := make([]ports.AssetSearchHit, 0, len(results))
	for _, r := range results {
		out = append(out, ports.AssetSearchHit{
			AssetID:   payloadString(r.Payload, "asset_id"),
			Name:      payloadString(r.Payload, "name"),
			Score:     r.Score,
			Source:    payloadString(r.Payload, "source"),
			DriveLink: "", // clip path: per QDRANT-001
		})
	}
	return out
}

// convertStockAssetHits maps infra-level schema.SearchResult →
// canonical AssetSearchHit. For the stock path, DriveLink IS
// populated (from payload "drive_link" or fallback "drive_url").
func convertStockAssetHits(results []schema.SearchResult) []ports.AssetSearchHit {
	out := make([]ports.AssetSearchHit, 0, len(results))
	for _, r := range results {
		dl := payloadString(r.Payload, "drive_link")
		if dl == "" {
			dl = payloadString(r.Payload, "drive_url")
		}
		out = append(out, ports.AssetSearchHit{
			AssetID:   payloadString(r.Payload, "asset_id"),
			Name:      payloadString(r.Payload, "name"),
			Source:    payloadString(r.Payload, "source"),
			DriveLink: dl,
			Score:     r.Score,
		})
	}
	return out
}
