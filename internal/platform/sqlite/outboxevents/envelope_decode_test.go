package outboxevents

import "testing"

type decodeV1Request struct {
	Envelope
	AssetID string `json:"asset_id"`
}

func decodeV1Event(payload string) Event {
	return Event{ID: 7, AggregateID: "agg-1", AttemptCount: 2, PayloadJSON: payload}
}

func TestDecodeV1AcceptsCanonicalEnvelope(t *testing.T) {
	const payload = `{"schema_version":"test.event.v1","event_id":"evt-1","asset_id":"asset-1","idempotency_key":"idem-1"}`
	var req decodeV1Request
	if err := DecodeV1(DecodePolicy{EventName: "test.event", WantSchemaVersion: "test.event.v1"}, decodeV1Event(payload), &req); err != nil {
		t.Fatalf("DecodeV1: %v", err)
	}
	if req.EventID != "evt-1" || req.IdempotencyKey != "idem-1" || req.AssetID != "asset-1" {
		t.Fatalf("payload not decoded into target: %+v", req)
	}
}

func TestDecodeV1RejectsMalformedPayloadTerminally(t *testing.T) {
	var req decodeV1Request
	err := DecodeV1(DecodePolicy{EventName: "test.event", WantSchemaVersion: "test.event.v1"}, decodeV1Event(`{`), &req)
	if err == nil {
		t.Fatal("malformed JSON must fail")
	}
	if !IsTerminal(err) {
		t.Fatalf("parse failure must be terminal, got %v", err)
	}
}

func TestDecodeV1RejectsEnvelopeViolationsTerminally(t *testing.T) {
	cases := map[string]string{
		"schema_version mismatch": `{"schema_version":"test.event.v9","event_id":"evt-1","idempotency_key":"idem-1"}`,
		"missing event_id":        `{"schema_version":"test.event.v1","idempotency_key":"idem-1"}`,
		"missing idempotency_key": `{"schema_version":"test.event.v1","event_id":"evt-1"}`,
	}
	for name, payload := range cases {
		var req decodeV1Request
		err := DecodeV1(DecodePolicy{EventName: "test.event", WantSchemaVersion: "test.event.v1"}, decodeV1Event(payload), &req)
		if err == nil {
			t.Fatalf("%s: expected a terminal error", name)
		}
		if !IsTerminal(err) {
			t.Fatalf("%s: envelope violations must be terminal, got %v", name, err)
		}
	}
}

func TestDecodeV1ValidatesWithoutTarget(t *testing.T) {
	const payload = `{"schema_version":"test.event.v1","event_id":"evt-1","idempotency_key":"idem-1"}`
	if err := DecodeV1(DecodePolicy{EventName: "test.event", WantSchemaVersion: "test.event.v1"}, decodeV1Event(payload), nil); err != nil {
		t.Fatalf("envelope-only validation must pass: %v", err)
	}
}
