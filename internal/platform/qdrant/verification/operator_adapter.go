package verification

import (
	"context"
	"fmt"

	operator "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/operator"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
)

type OperatorIndexVerifier struct{ client *transport.Client }

func NewOperatorIndexVerifier(client *transport.Client) *OperatorIndexVerifier {
	return &OperatorIndexVerifier{client: client}
}

func (v *OperatorIndexVerifier) Verify(ctx context.Context, assetID, collection string) (operator.QdrantPointInfo, error) {
	if v == nil || v.client == nil {
		return operator.QdrantPointInfo{}, fmt.Errorf("qdrant operator verifier: client is nil")
	}
	page, err := v.client.ScrollPoints(ctx, collection, "", 100, nil)
	if err != nil {
		return operator.QdrantPointInfo{}, err
	}
	for _, point := range page.Points {
		id, _ := point.Payload["asset_id"].(string)
		if id == assetID {
			return operator.QdrantPointInfo{Checked: true, Present: true, Collection: collection, PayloadAssetID: id}, nil
		}
	}
	return operator.QdrantPointInfo{Checked: true, Collection: collection}, nil
}

var _ operator.IndexVerifier = (*OperatorIndexVerifier)(nil)
