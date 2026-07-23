package operatorverify

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/operator"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
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
// exists in the supplied collection.
func (v *Verifier) Verify(ctx context.Context, assetID, collection string) (operator.QdrantPointInfo, error) {
	info := operator.QdrantPointInfo{
		Checked:    true,
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

	result, err := v.client.ScrollPoints(ctx, collection, "", 1, filter)
	if err != nil {
		return info, fmt.Errorf("operatorverify: scroll collection %q for asset %q: %w", collection, assetID, err)
	}

	info.Present = len(result.Points) > 0
	if info.Present && len(result.Points) > 0 {
		payload := result.Points[0].Payload
		if lifecycle, ok := payload["lifecycle_state"].(string); ok {
			info.PayloadLifecycleState = lifecycle
		}
		if aid, ok := payload["asset_id"].(string); ok {
			info.PayloadAssetID = aid
		}
	}

	return info, nil
}
