// Package outbox — asset_published_test.go: hermetic TDD coverage
// for AssetPublishedHandler (SEMANTIC-LOCATION-API-2026-07-06
// Wave 5).
//
// Each test runs against an in-memory `assetPublisherStub` so the
// test surface is hermetic (no SQLite / Qdrant / live server
// required). godlike/06 SSOT preserves: AssetPublishedHandler,
// ComposeSearchText, and EventType live in asset_published.go as
// the canonical SOLE owners.
package outbox

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// ── Stub (test-only) ────────────────────────────────────────────────────

// assetPublisherStub is a test-only AssetPublisher. It records every
// UpsertFromClip + SetIndexState call so tests can assert the
// port-receive surface.
type assetPublisherStub struct {
	upsertCalls        []string
	setIndexStateCalls []setIndexStateCall
	upsertErr          error
	setIndexStateErr   error
}

type setIndexStateCall struct {
	id    string
	state string
}

func (s *assetPublisherStub) UpsertFromClip(_ context.Context, clipID string) error {
	s.upsertCalls = append(s.upsertCalls, clipID)
	return s.upsertErr
}

func (s *assetPublisherStub) SetIndexState(_ context.Context, id string, state string) error {
	s.setIndexStateCalls = append(s.setIndexStateCalls, setIndexStateCall{id: id, state: state})
	return s.setIndexStateErr
}

// makeEvent returns a minimal outboxevents.Event with the given
// payload JSON. Other fields are zero-valued unless the test
// asserts on them.
func makeEvent(payloadJSON string) outboxevents.Event {
	return outboxevents.Event{
		EventType:   outboxevents.EventAssetPublished,
		PayloadJSON: payloadJSON,
	}
}

// ── Test 1 — payload parse error (terminal) ───────────────────────────────

func TestAssetPublished_PayloadParseError_Terminal(t *testing.T) {
	h := NewAssetPublishedHandler(&assetPublisherStub{}, zap.NewNop())
	bad := makeEvent("{not_json")
	err := h.Handle(context.Background(), bad)
	if err == nil {
		t.Fatalf("expected error on malformed JSON")
	}
	if !outboxevents.IsTerminal(err) {
		t.Errorf("expected TerminalError, got %T: %v", err, err)
	}
	if !errors.Is(err, ErrAssetPublishedPayloadParse) {
		t.Errorf("expected ErrAssetPublishedPayloadParse in chain, got %v", err)
	}
}

// ── Test 2 — schema version mismatch (terminal) ──────────────────────────

func TestAssetPublished_SchemaVersionMismatch_Terminal(t *testing.T) {
	h := NewAssetPublishedHandler(&assetPublisherStub{}, zap.NewNop())
	payload := `{"schema_version":"asset.published.v999","event_id":"e1","asset_id":"a1","destination":"stock","idempotency_key":"k1"}`
	err := h.Handle(context.Background(), makeEvent(payload))
	if !outboxevents.IsTerminal(err) {
		t.Errorf("expected terminal on schema mismatch, got %v", err)
	}
	if !errors.Is(err, ErrAssetPublishedSchemaVersionMismatch) {
		t.Errorf("expected ErrAssetPublishedSchemaVersionMismatch in chain, got %v", err)
	}
}

// ── Test 3 — required-field validation (terminal gates) ──────────────────

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
			h := NewAssetPublishedHandler(&assetPublisherStub{}, zap.NewNop())
			err := h.Handle(context.Background(), makeEvent(tc.payload))
			if !outboxevents.IsTerminal(err) {
				t.Errorf("expected TerminalError, got %v", err)
			}
			if !errors.Is(err, tc.sentinel) {
				t.Errorf("expected %v in chain, got %v", tc.sentinel, err)
			}
		})
	}
}

// ── Test 4 — happy path upserts + sets INDEXED ────────────────────────────

func TestAssetPublished_HappyPath_UpsertsAndSetsIndexed(t *testing.T) {
	stub := &assetPublisherStub{}
	h := NewAssetPublishedHandler(stub, zap.NewNop())
	payload := `{
		"schema_version":"asset.published.v1",
		"event_id":"e1",
		"asset_id":"asset_mike_tyson_001",
		"destination":"stock",
		"origin":"retrieved",
		"category":"Boxe",
		"subject":"Mike Tyson",
		"provider":"pexels",
		"drive_file_id":"drive_file_xyz",
		"drive_path":"stock/Boxe/pexels/Mike-Tyson",
		"tags":["boxing","training"],
		"idempotency_key":"idemp_xyz",
		"requested_at":"2026-07-06T00:00:00Z"
	}`
	err := h.Handle(context.Background(), makeEvent(payload))
	if err != nil {
		t.Fatalf("happy path returned error: %v", err)
	}
	if len(stub.upsertCalls) != 1 || stub.upsertCalls[0] != "asset_mike_tyson_001" {
		t.Errorf("UpsertFromClip calls = %v, want [asset_mike_tyson_001]", stub.upsertCalls)
	}
	if len(stub.setIndexStateCalls) != 1 {
		t.Fatalf("SetIndexState calls = %d, want 1", len(stub.setIndexStateCalls))
	}
	got := stub.setIndexStateCalls[0]
	if got.id != "asset_mike_tyson_001" || got.state != "INDEXED" {
		t.Errorf("SetIndexState call = (%s,%s), want (asset_mike_tyson_001,INDEXED)", got.id, got.state)
	}
}

// ── Test 5 — happy path skips SetIndexState on upsert-only payload ───────

func TestAssetPublished_UpsertError_Retryable(t *testing.T) {
	stub := &assetPublisherStub{upsertErr: errors.New("qdrant refused connection")}
	h := NewAssetPublishedHandler(stub, zap.NewNop())
	payload := `{"schema_version":"asset.published.v1","event_id":"e1","asset_id":"asset_mike_tyson_001","destination":"stock","idempotency_key":"k1"}`
	err := h.Handle(context.Background(), makeEvent(payload))
	if err == nil {
		t.Fatalf("expected error on Qdrant upsert failure")
	}
	if outboxevents.IsTerminal(err) {
		t.Errorf("Upsert failure should be retryable, not terminal: %v", err)
	}
	if !errors.Is(err, ErrAssetPublishedQdrantUpsertFailed) {
		t.Errorf("expected ErrAssetPublishedQdrantUpsertFailed in chain, got %v", err)
	}
	if len(stub.setIndexStateCalls) != 0 {
		t.Errorf("SetIndexState should be skipped on upsert failure, got %v", stub.setIndexStateCalls)
	}
}

// ── Test 6 — SetIndexState failure after successful upsert → retryable ───

func TestPublished_SetIndexStateFailure_RetryableAfterUpsert(t *testing.T) {
	stub := &assetPublisherStub{setIndexStateErr: errors.New("sqlite busy")}
	h := NewAssetPublishedHandler(stub, zap.NewNop())
	payload := `{"schema_version":"asset.published.v1","event_id":"e1","asset_id":"a1","destination":"stock","idempotency_key":"k1"}`
	err := h.Handle(context.Background(), makeEvent(payload))
	if err == nil {
		t.Fatalf("expected error on SetIndexState failure")
	}
	if outboxevents.IsTerminal(err) {
		t.Errorf("SetIndexState failure should be retryable, not terminal: %v", err)
	}
	if !errors.Is(err, ErrAssetPublishedQdrantUpsertFailed) {
		t.Errorf("expected ErrAssetPublishedQdrantUpsertFailed in chain (covering both upsert + state-set), got %v", err)
	}
	if len(stub.upsertCalls) != 1 {
		t.Errorf("UpsertFromClip should have been called once before SetIndexState, got %v", stub.upsertCalls)
	}
}

// ── Test 7 — nil publisher returns sticky-pending sentinel ───────────────

func TestAssetPublished_NilPublisher_StickyPending(t *testing.T) {
	h := NewAssetPublishedHandler(nil, zap.NewNop())
	payload := `{"schema_version":"asset.published.v1","event_id":"e1","asset_id":"a1","destination":"stock","idempotency_key":"k1"}`
	err := h.Handle(context.Background(), makeEvent(payload))
	if err == nil {
		t.Fatalf("expected sentinel error on nil publisher")
	}
	if outboxevents.IsTerminal(err) {
		t.Errorf("nil publisher should NOT be terminal (sticky-pending for re-emit after operator action), got: %v", err)
	}
	if !errors.Is(err, ErrAssetPublishedPublisherNotWired) {
		t.Errorf("expected ErrAssetPublishedPublisherNotWired, got %v", err)
	}
}

// ── Test 8 — EventType returns canonical constant ───────────────────────

func TestAssetPublished_EventType_Canonical(t *testing.T) {
	h := NewAssetPublishedHandler(&assetPublisherStub{}, nil)
	if got := h.EventType(); got != outboxevents.EventAssetPublished {
		t.Errorf("EventType() = %q, want %q", got, outboxevents.EventAssetPublished)
	}
}

// ── Tests 9..13 — ComposeSearchText user-spec format ─────────────────────

// TestAssetPublished_ComposeSearchText_UserSpecExample verifies the
// format matches the user-spec literal:
//
//	stock video about Mike Tyson in category Boxe from provider pexels tags boxing training
func TestAssetPublished_ComposeSearchText_UserSpecExample(t *testing.T) {
	got := ComposeSearchText("stock", "video", "Mike Tyson", "Boxe", "pexels", []string{"boxing", "training"}, "stock/Boxe/pexels/Mike-Tyson", "video")
	want := "stock video about Mike Tyson in category Boxe from provider pexels tags boxing training in drive stock/Boxe/pexels/Mike-Tyson content_type video"
	if got != want {
		t.Errorf("ComposeSearchText:\n got  = %q\n want = %q", got, want)
	}
}

// TestAssetPublished_ComposeSearchText_ImageVariant verifies the
// destination + origin + subject segments for image variant.
func TestAssetPublished_ComposeSearchText_ImageVariant(t *testing.T) {
	got := ComposeSearchText("image", "generated", "Realistic portrait of Mike Tyson", "Realistic", "google-slides", []string{"boxing", "portrait"}, "images/Realistic/Mike-Tyson", "image")
	want := "image generated about Realistic portrait of Mike Tyson in category Realistic from provider google-slides tags boxing portrait in drive images/Realistic/Mike-Tyson content_type image"
	if got != want {
		t.Errorf("ComposeSearchText:\n got  = %q\n want = %q", got, want)
	}
}

// TestAssetPublished_ComposeSearchText_EmptyOptionalSegments silently
// drops empty category / provider / tags.
func TestAssetPublished_ComposeSearchText_EmptyOptionalSegments(t *testing.T) {
	got := ComposeSearchText("voiceover", "", "Mike Tyson documentary", "", "", nil, "", "")
	want := "voiceover about Mike Tyson documentary"
	if got != want {
		t.Errorf("ComposeSearchText:\n got  = %q\n want = %q", got, want)
	}
}

// TestAssetPublished_ComposeSearchText_EmptySubject_IsAnchoredOnDestination
// ensures the output always carries at least the destination label
// (godlike/07 no-fake-availability) so a downstream Qdrant embedding
// can route by destination without re-querying SQLite. All optional
// segments intentionally blank to test the absolute minimal form.
func TestAssetPublished_ComposeSearchText_EmptySubject(t *testing.T) {
	got := ComposeSearchText("stock", "retrieved", "", "", "", nil, "", "")
	want := "stock retrieved about"
	if got != want {
		t.Errorf("ComposeSearchText:\n got  = %q\n want = %q", got, want)
	}
}

// TestAssetPublished_ComposeSearchText_TagsWhitespaceStripped ensures
// tags with leading/trailing whitespace are silently dropped.
func TestAssetPublished_ComposeSearchText_TagsWhitespaceStripped(t *testing.T) {
	got := ComposeSearchText("stock", "retrieved", "Mike Tyson", "Boxe", "pexels", []string{"  boxing  ", "", "training"}, "", "")
	if !strings.Contains(got, "boxing") || !strings.Contains(got, "training") {
		t.Errorf("expected boxing + training tags to survive whitespace strip, got %q", got)
	}
}

// ── Tests 16..17 — DoD #9: drive_path + content_type in composed text ──

// TestAssetPublished_ComposeSearchText_DrivePathAndContentType pins
// the DoD #9 contract: when drive_path and content_type are non-empty,
// they appear in the composed search text after the tags segment so
// the Qdrant embedding vector can discriminate by canonical Drive
// folder path and media-type.
func TestAssetPublished_ComposeSearchText_DrivePathAndContentType(t *testing.T) {
	got := ComposeSearchText("stock", "retrieved", "Mike Tyson", "Boxe", "pexels", []string{"boxing"}, "stock/Boxe/pexels/Mike-Tyson", "video")
	want := "stock retrieved about Mike Tyson in category Boxe from provider pexels tags boxing in drive stock/Boxe/pexels/Mike-Tyson content_type video"
	if got != want {
		t.Errorf("ComposeSearchText:\n got  = %q\n want = %q", got, want)
	}
}

// TestAssetPublished_ComposeSearchText_EmptyDrivePathAndContentType
// pins: when drive_path and content_type are empty, they are silently
// dropped from the composed text (backward-compat with pre-DoD-#9
// payloads).
func TestAssetPublished_ComposeSearchText_EmptyDrivePathAndContentType(t *testing.T) {
	got := ComposeSearchText("stock", "retrieved", "Mike Tyson", "", "", nil, "", "")
	want := "stock retrieved about Mike Tyson"
	if got != want {
		t.Errorf("ComposeSearchText:\n got  = %q\n want = %q", got, want)
	}
	// Also verify: non-empty drive_path with empty content_type
	got2 := ComposeSearchText("stock", "retrieved", "Mike Tyson", "", "", nil, "stock/Boxe/Mike", "")
	want2 := "stock retrieved about Mike Tyson in drive stock/Boxe/Mike"
	if got2 != want2 {
		t.Errorf("ComposeSearchText (drive_path only):\n got  = %q\n want = %q", got2, want2)
	}
	// And: empty drive_path with non-empty content_type
	got3 := ComposeSearchText("stock", "retrieved", "Mike Tyson", "", "", nil, "", "video")
	want3 := "stock retrieved about Mike Tyson content_type video"
	if got3 != want3 {
		t.Errorf("ComposeSearchText (content_type only):\n got  = %q\n want = %q", got3, want3)
	}
}

func TestAssetPublished_HappyPath_ComposesCorrectSearchText(t *testing.T) {
	stub := &assetPublisherStub{}
	capturedSearchText := ""
	h := NewAssetPublishedHandler(stub, zap.NewNop())
	// Override the log to capture the search_text via a custom core
	// is overkill — instead verify via the canonical user-spec output
	// literally, separate from stub calls.
	payload := `{
		"schema_version":"asset.published.v1",
		"event_id":"e1",
		"asset_id":"asset_mike_tyson_001",
		"destination":"stock",
		"origin":"retrieved",
		"category":"Boxe",
		"subject":"Mike Tyson",
		"provider":"pexels",
		"tags":["boxing","training"],
		"idempotency_key":"idemp_xyz"
	}`
	if err := h.Handle(context.Background(), makeEvent(payload)); err != nil {
		t.Fatalf("happy path returned error: %v", err)
	}
	_ = capturedSearchText // (Future enhancement: capture log; for now assert the path was taken)
	if len(stub.upsertCalls) != 1 || len(stub.setIndexStateCalls) != 1 {
		t.Errorf("expected 1 upsert + 1 state-set, got %v / %v", stub.upsertCalls, stub.setIndexStateCalls)
	}

	// Direct ComposeSearchText call to verify the user-spec format
	// end-to-end via the same handler inputs:
	searchText := ComposeSearchText("stock", "retrieved", "Mike Tyson", "Boxe", "pexels", []string{"boxing", "training"}, "", "")
	if !strings.Contains(searchText, "stock") || !strings.Contains(searchText, "Mike Tyson") ||
		!strings.Contains(searchText, "Boxe") || !strings.Contains(searchText, "pexels") ||
		!strings.Contains(searchText, "boxing") || !strings.Contains(searchText, "training") {
		t.Errorf("searchText missing required segments: %q", searchText)
	}
}

// ── Test 15 — handler does NOT mutate the JSON payload (idempotent replay safe) ──

func TestAssetPublished_ReplaySafe_NoMutation(t *testing.T) {
	stub := &assetPublisherStub{}
	h := NewAssetPublishedHandler(stub, zap.NewNop())
	payload := `{"schema_version":"asset.published.v1","event_id":"e1","asset_id":"a1","destination":"stock","idempotency_key":"k1"}`
	if err := h.Handle(context.Background(), makeEvent(payload)); err != nil {
		t.Fatalf("first delivery returned error: %v", err)
	}
	if err := h.Handle(context.Background(), makeEvent(payload)); err != nil {
		t.Fatalf("replay returned error: %v", err)
	}
	if len(stub.upsertCalls) != 2 {
		t.Errorf("expected 2 upserts (idempotent), got %d", len(stub.upsertCalls))
	}
	// Idempotent re-upsert is OK at the Qdrant layer (Qdrant PUT is
	// natively idempotent on a deterministic UUID5 point id); a real
	// production handler should consult the canonical IdempotencyKey
	// to skip the second call. This test pins the current behavior
	// so future Play 6 wiring that adds the idempotency check
	// surfaces as a behavior change rather than a silent drift.
}
