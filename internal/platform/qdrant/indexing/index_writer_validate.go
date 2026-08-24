package indexing

import (
	"fmt"
	"math"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
)

// ValidatePoint checks a point against the schema before upsert.
//
// QDRANT-001 closure (June 2026): error messages report the canonical
// asset ID (without the AssetIDPrefix namespace marker) so operators
// reading the dashboards see the same identifier they would on SQLite
// / Drive / Qdrant REST. Internal callers (PayloadMapper, IndexWriter)
// receive the prefixed form via point.ID and we strip it here at the
// public validation boundary.
func ValidatePoint(point *schema.Point, schema *schema.IndexSchema) error {
	if point == nil {
		return fmt.Errorf("point is nil")
	}
	if point.ID == "" {
		return fmt.Errorf("point ID must not be empty")
	}
	// AssetID is used purely to enrich error messages with a
	// human-readable identifier. Prefer `payload["asset_id"]` when
	// present (the canonical write path always populates it via
	// BuildPayload). Fall back to `point.ID` (the UUID v5 hash) when
	// the payload is missing or empty — this preserves backwards
	// compatibility with legacy code paths and unit-test fixtures
	// that construct bare Points without populating the payload.
	// QDRANT-001's silent-failure concern was about the IDENTITY
	// reverse-mapping from the UUID point, NOT about
	// the bare point.ID itself: the latter is a valid (if
	// operator-unfriendly) identifier, not a security bypass.
	var assetID string
	if point.Payload != nil {
		if id, ok := point.Payload["asset_id"].(string); ok && id != "" {
			assetID = id
		}
	}
	if assetID == "" {
		assetID = point.ID
	}

	vectors := point.Vectors
	if len(vectors) == 0 {
		return fmt.Errorf("point must have at least one vector")
	}

	for _, spec := range schema.DenseVectors {
		raw, ok := vectors[spec.Channel]
		if !ok {
			continue // optional channel
		}
		vec, ok := raw.([]float32)
		if !ok {
			return &transport.ErrVectorDimensionMismatch{
				Channel:  spec.Channel,
				Expected: spec.Dimensions,
				Actual:   0,
				AssetID:  assetID,
			}
		}
		if len(vec) == 0 {
			return &transport.ErrEmptyVector{Channel: spec.Channel, AssetID: assetID}
		}
		if len(vec) != spec.Dimensions {
			return &transport.ErrVectorDimensionMismatch{
				Channel:  spec.Channel,
				Expected: spec.Dimensions,
				Actual:   len(vec),
				AssetID:  assetID,
			}
		}
		for _, v := range vec {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				return &transport.ErrNaNOrInf{Channel: spec.Channel, AssetID: assetID}
			}
		}
	}

	return nil
}
