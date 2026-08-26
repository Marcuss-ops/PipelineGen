// Package outbox — asset_published_test.go: hermetic coverage for the
// informational asset.published consumer.
package jobs

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"context"
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

func makeEvent(payloadJSON string) outboxevents.Event {
	return outboxevents.Event{
		EventType:   outboxevents.EventAssetPublished,
		PayloadJSON: payloadJSON,
	}
}

func validAssetPublishedPayload() string {
	return `{"schema_version":"asset.published.v1","event_id":"e1","asset_id":"asset-1","destination":"stock","origin":"retrieved","subject":"Mike Tyson","provider":"pexels","idempotency_key":"k1"}`
}

func TestAssetPublished_PayloadParseError_Terminal(t *testing.T) {
	h := NewAssetPublishedHandler(zap.NewNop())
	err := h.Handle(context.Background(), makeEvent("{not_json"))
	if err == nil || !outboxevents.IsTerminal(err) {
		t.Fatalf("expected terminal parse error, got %v", err)
	}
	if !errors.Is(err, ErrAssetPublishedPayloadParse) {
		t.Fatalf("expected ErrAssetPublishedPayloadParse, got %v", err)
	}
}

func TestAssetPublished_SchemaVersionMismatch_Terminal(t *testing.T) {
	h := NewAssetPublishedHandler(zap.NewNop())
	err := h.Handle(context.Background(), makeEvent(`{"schema_version":"asset.published.v999","event_id":"e1","asset_id":"a1","destination":"stock","idempotency_key":"k1"}`))
	if err == nil || !outboxevents.IsTerminal(err) || !errors.Is(err, ErrAssetPublishedSchemaVersionMismatch) {
		t.Fatalf("expected terminal schema error, got %v", err)
	}
}

func TestAssetPublished_RequiredFields_Terminal(t *testing.T) {
	cases := []struct {
		name     string
		payload  string
		sentinel error
	}{
		{"missing event_id", `{"schema_version":"asset.published.v1","asset_id":"a1","destination":"stock","idempotency_key":"k1"}`, ErrAssetPublishedEventIDMissing},
		{"missing asset_id", `{"schema_version":"asset.published.v1","event_id":"e1","destination":"stock","idempotency_key":"k1"}`, ErrAssetPublishedAssetIDMissing},
		{"missing destination", `{"schema_version":"asset.published.v1","event_id":"e1","asset_id":"a1","idempotency_key":"k1"}`, ErrAssetPublishedDestinationMissing},
		{"missing idempotency_key", `{"schema_version":"asset.published.v1","event_id":"e1","asset_id":"a1","destination":"stock"}`, ErrAssetPublishedIdempotencyKeyMissing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := NewAssetPublishedHandler(zap.NewNop()).Handle(context.Background(), makeEvent(tc.payload))
			if err == nil || !outboxevents.IsTerminal(err) || !errors.Is(err, tc.sentinel) {
				t.Fatalf("expected terminal %v, got %v", tc.sentinel, err)
			}
		})
	}
}

func TestAssetPublished_ValidPayloadIsInformationalOnly(t *testing.T) {
	h := NewAssetPublishedHandler(zap.NewNop())
	if err := h.Handle(context.Background(), makeEvent(validAssetPublishedPayload())); err != nil {
		t.Fatalf("valid asset.published returned error: %v", err)
	}
}

func TestAssetPublished_ValidPayloadWithRichMetadataIsInformationalOnly(t *testing.T) {
	h := NewAssetPublishedHandler(zap.NewNop())
	payload := `{"schema_version":"asset.published.v1","event_id":"e1","asset_id":"asset-1","destination":"stock","origin":"retrieved","category":"Boxe","subject":"Mike Tyson","provider":"pexels","drive_file_id":"drive-1","drive_path":"stock/Boxe/pexels/Mike-Tyson","content_type":"video","tags":["boxing","training"],"idempotency_key":"k1","requested_at":"2026-07-06T00:00:00Z"}`
	if err := h.Handle(context.Background(), makeEvent(payload)); err != nil {
		t.Fatalf("rich asset.published returned error: %v", err)
	}
}

func TestAssetPublished_EventType_Canonical(t *testing.T) {
	h := NewAssetPublishedHandler(zap.NewNop())
	if got := h.EventType(); got != outboxevents.EventAssetPublished {
		t.Errorf("EventType() = %q, want %q", got, outboxevents.EventAssetPublished)
	}
	if got := h.IdempotencyKey(); got != outboxevents.EventAssetPublished+"."+outboxevents.SchemaVersionAssetPublished {
		t.Errorf("IdempotencyKey() = %q", got)
	}
}

func TestAssetPublished_ComposeSearchText_UserSpecExample(t *testing.T) {
	got := ComposeSearchText("stock", "video", "Mike Tyson", "Boxe", "pexels", []string{"boxing", "training"}, "stock/Boxe/pexels/Mike-Tyson", "video")
	want := "stock video about Mike Tyson in category Boxe from provider pexels tags boxing training in drive stock/Boxe/pexels/Mike-Tyson content_type video"
	if got != want {
		t.Errorf("ComposeSearchText: got %q want %q", got, want)
	}
}

func TestAssetPublished_ComposeSearchText_OptionalSegments(t *testing.T) {
	if got, want := ComposeSearchText("voiceover", "", "Mike Tyson documentary", "", "", nil, "", ""), "voiceover about Mike Tyson documentary"; got != want {
		t.Errorf("ComposeSearchText: got %q want %q", got, want)
	}
	got := ComposeSearchText("stock", "retrieved", "Mike Tyson", "Boxe", "pexels", []string{"  boxing  ", "", "training"}, "", "")
	if !strings.Contains(got, "boxing") || !strings.Contains(got, "training") {
		t.Errorf("expected normalized tags in %q", got)
	}
}
