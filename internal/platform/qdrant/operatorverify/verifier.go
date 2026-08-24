package operatorverify

import (
	"context"
	"fmt"
	"sort"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/operator"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
)

// Verifier adapts the canonical Qdrant transport client to the
// operator IndexVerifier port.
type Verifier struct {
	client *transport.Client
}

// NewVerifier creates a verifier backed by the supplied Qdrant
// transport client. A nil client is accepted but Verify will fail
// closed at runtime.
func NewVerifier(client *transport.Client) *Verifier {
	return &Verifier{client: client}
}

// Verify checks whether a point with payload.asset_id == assetID
// exists in the supplied collection and returns the live vector
// dimensions when the point is present.
func (v *Verifier) Verify(ctx context.Context, assetID, collection string) (operator.QdrantPointInfo, error) {
	info := operator.QdrantPointInfo{
		Collection: collection,
	}

	if v.client == nil {
		return info, fmt.Errorf("operatorverify: qdrant client not configured")
	}
	if collection == "" {
		return info, fmt.Errorf("operatorverify: collection name is required")
	}
	if assetID == "" {
		return info, fmt.Errorf("operatorverify: assetID is required")
	}

	filter := map[string]any{
		"must": []map[string]any{
			{
				"key": "asset_id",
				"match": map[string]any{
					"value": assetID,
				},
			},
		},
	}

	result, err := v.client.ScrollPointsWithVector(ctx, collection, "", 1, filter)
	if err != nil {
		return info, fmt.Errorf("operatorverify: scroll collection %q for asset %q: %w", collection, assetID, err)
	}

	info.Checked = true
	info.Present = len(result.Points) > 0
	if info.Present {
		point := result.Points[0]
		payload := point.Payload
		if lifecycle, ok := payload["lifecycle_state"].(string); ok {
			info.PayloadLifecycleState = lifecycle
		}
		if aid, ok := payload["asset_id"].(string); ok {
			info.PayloadAssetID = aid
		}
		info.VectorDimensions = vectorDimensions(point.Vector)
	}

	return info, nil
}

// vectorDimensions returns the length of the first dense vector it finds
// in the Qdrant point vector payload. It tolerates both []float32 and
// []float64 wire encodings as well as the generic []any produced by the
// JSON decoder. Sparse vectors are ignored because their dimension is
// the vocabulary size, not the embedding size the UI wants to show.
//
// The vector map is iterated over sorted keys so the reported
// dimension is deterministic when multiple dense vectors are present.
func vectorDimensions(vectors map[string]any) int {
	if len(vectors) == 0 {
		return 0
	}
	keys := make([]string, 0, len(vectors))
	for k := range vectors {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch vec := vectors[k].(type) {
		case []float32:
			return len(vec)
		case []float64:
			return len(vec)
		case []any:
			return len(vec)
		default:
			continue
		}
	}
	return 0
}
