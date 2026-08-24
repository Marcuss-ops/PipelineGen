package jobs

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// indexDeletePayloadV1 mirrors the next-hop consumer envelope without importing
// infrastructure-layer producer types into the application package.
type indexDeletePayloadV1 struct {
	SchemaVersion  string `json:"schema_version"`
	EventID        string `json:"event_id"`
	AssetID        string `json:"asset_id"`
	RequestedAt    string `json:"requested_at,omitempty"`
	IdempotencyKey string `json:"idempotency_key"`
}

func buildIndexDeletePayloadForDrive(assetID string) ([]byte, error) {
	return json.Marshal(indexDeletePayloadV1{
		SchemaVersion:  DeleteRequestSchemaVersion,
		EventID:        uuid.NewString(),
		AssetID:        assetID,
		RequestedAt:    timeutil.FormatRFC3339(time.Now()),
		IdempotencyKey: "delete:" + assetID,
	})
}
