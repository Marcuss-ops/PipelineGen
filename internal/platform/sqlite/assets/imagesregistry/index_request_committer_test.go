package imagesregistry

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

type indexRequestOutboxCapture struct {
	eventType     string
	aggregateID   string
	aggregateType string
	payloadJSON   string
	eventKey      string
}

func (f *indexRequestOutboxCapture) Enqueue(
	_ context.Context,
	_ *sql.Tx,
	eventType, aggregateID, aggregateType, payloadJSON, eventKey string,
) (*outboxevents.EnqueueResult, error) {
	f.eventType = eventType
	f.aggregateID = aggregateID
	f.aggregateType = aggregateType
	f.payloadJSON = payloadJSON
	f.eventKey = eventKey
	return &outboxevents.EnqueueResult{Inserted: true}, nil
}

func TestCommitIndexRequestTx_ProviderEnvelopeUsesCanonicalContract(t *testing.T) {
	box := &indexRequestOutboxCapture{}
	requestedAt := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)

	result, err := CommitIndexRequestTx(
		context.Background(),
		new(sql.Tx),
		box,
		IndexRequest{
			AssetID:       "yt_video_10_20_v1",
			Source:        "youtube",
			MediaType:     "video",
			SourceVersion: "sha256:abc123",
			RequestedAt:   requestedAt,
		},
	)
	if err != nil {
		t.Fatalf("CommitIndexRequestTx returned error: %v", err)
	}
	if !result.Inserted {
		t.Fatal("expected Inserted=true")
	}
	if result.EventID == "" {
		t.Fatal("expected a non-empty event id")
	}
	if result.EventKey == "" || result.EventKey != box.eventKey {
		t.Fatalf("event key mismatch: result=%q enqueue=%q", result.EventKey, box.eventKey)
	}
	if box.eventType != outboxevents.EventAssetIndexRequested {
		t.Fatalf("unexpected event type: %q", box.eventType)
	}
	if box.aggregateID != "yt_video_10_20_v1" || box.aggregateType != "media_asset" {
		t.Fatalf("unexpected aggregate: id=%q type=%q", box.aggregateID, box.aggregateType)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(box.payloadJSON), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	assertIndexPayloadString(t, payload, "schema_version", outboxevents.ReindexEnvelopeV1Schema)
	assertIndexPayloadString(t, payload, "event_id", result.EventID)
	assertIndexPayloadString(t, payload, "asset_id", "yt_video_10_20_v1")
	assertIndexPayloadString(t, payload, "operation", indexRequestOperationUpsert)
	assertIndexPayloadString(t, payload, "source_version", "sha256:abc123")
	assertIndexPayloadString(t, payload, "idempotency_key", result.EventKey)
	assertIndexPayloadString(t, payload, "source", "youtube")
	assertIndexPayloadString(t, payload, "media_type", "video")
}

func TestCommitIndexRequestTx_RejectsMissingSourceVersion(t *testing.T) {
	_, err := CommitIndexRequestTx(
		context.Background(),
		new(sql.Tx),
		&indexRequestOutboxCapture{},
		IndexRequest{AssetID: "asset-1", Source: "youtube"},
	)
	if err == nil {
		t.Fatal("expected missing source_version error")
	}
}

func assertIndexPayloadString(t *testing.T, payload map[string]any, key, want string) {
	t.Helper()
	got, ok := payload[key].(string)
	if !ok || got != want {
		t.Fatalf("payload[%q]: want %q, got %#v", key, want, payload[key])
	}
}

var _ IndexRequestOutbox = (*indexRequestOutboxCapture)(nil)
