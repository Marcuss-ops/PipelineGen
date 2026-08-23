// Package publish_outbox_test — handler_test.go (FASE 3 / Push 3.1c).
//
// Hermetic tests for the artifact.publish_requested.v1 handler.
// Uses a stubStore capturing every StageRequest (no real staging
// service / real Repository / real SQLite needed for the
// application-layer handler test) + zaptest/observer for
// structured-log assertions.
//
// Coverage (~8 cases):
//   - Fail-fast: NewHandler rejects (nil store), (nil log).
//   - Routing surfaces: EventType() + IdempotencyKey() pinned
//     to the canonical event_type (no drift on rename).
//   - Happy path: payload → Store.Stage called once with the
//     decoded StageRequest fields + canonical zap fields.
//   - Failure paths:
//   - malformed JSON → ErrInvalidPayload (Store.Stage NOT called)
//   - missing JobID / Mime / SourceURI → ErrMissingFields
//   - out-of-set Requirement → ErrInvalidPayload
//   - non-openable SourceURI → ErrSourceOpen
//   - Store.Stage failure → wrapped error surfaced (not swallowed)
//   - file:// prefix stripping (scheme / no-scheme both accepted).
//   - empty Requirement defaults to "optional".
package publish_outbox_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	publishoutbox "github.com/Marcuss-ops/PipelineGen/internal/application/publish_outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/application/staging"
	artifact "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// ── Mocks & helpers ─────────────────────────────────────────────────────

// stubStore captures every StageRequest and returns the canned
// receipt (or injected error). Implements staging.Store. Used
// in lieu of a real StoreService + Repository to keep the
// handler test hermetic (no SQLite, no filesystem workspace).
type stubStore struct {
	mu      sync.Mutex
	calls   []staging.StageRequest
	receipt *staging.StageReceipt
	err     error
}

func (s *stubStore) Stage(_ context.Context, req staging.StageRequest) (*staging.StageReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, req)
	return s.receipt, s.err
}

// makeEvent constructs an outboxevents.Event with the supplied
// payload JSON + idempotency key. event_type pinned to
// EventTypeArtifactPublishRequested so the handler-under-test
// accepts it without surprise.
func makeEvent(payloadJSON, eventKey string) outboxevents.Event {
	return outboxevents.Event{
		EventType:   publishoutbox.EventTypeArtifactPublishRequested,
		PayloadJSON: payloadJSON,
		EventKey:    eventKey,
	}
}

// validRequestPayload returns the canonical happy-path payload.
// Tests mutate one field at a time to exercise each rejection.
func validRequestPayload() publishoutbox.PublishRequestPayload {
	return publishoutbox.PublishRequestPayload{
		JobID:       "job-x",
		Mime:        "audio/mpeg",
		Requirement: "required",
		Destination: "drive:voiceover/test",
		SourceURI:   "/tmp/should-not-be-used-by-default-in-tests",
		EventKey:    "req-key-1",
	}
}

// newHandler constructs a Handler backed by a stubStore + an
// observed zap.Logger. Tests use observerLogs to assert on
// emitted structured-log fields without parsing human output.
func newHandler(t *testing.T) (*publishoutbox.Handler, *stubStore, *observer.ObservedLogs) {
	t.Helper()
	store := &stubStore{}
	core, logs := observer.New(zapcore.InfoLevel)
	log := zap.New(core)
	h, err := publishoutbox.NewHandler(store, log)
	if err != nil {
		t.Fatalf("NewHandler (test helper): %v", err)
	}
	return h, store, logs
}

// writeSourceFile creates a non-empty file at sourcePath so
// os.Open succeeds in the handler. Returns the absolute path.
func writeSourceFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.bin")
	if err := os.WriteFile(sourcePath, []byte(content), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	return sourcePath
}

// ── Test 1: NewHandler fail-fast on nil store ──────────────────────────

func TestNewHandler_RejectsNilStore(t *testing.T) {
	core, _ := observer.New(zapcore.InfoLevel)
	_, err := publishoutbox.NewHandler(nil, zap.New(core))
	if err == nil {
		t.Fatalf("NewHandler(nil store): expected error, got nil")
	}
}

// ── Test 2: NewHandler fail-fast on nil log ────────────────────────────

func TestNewHandler_RejectsNilLog(t *testing.T) {
	store := &stubStore{}
	_, err := publishoutbox.NewHandler(store, nil)
	if err == nil {
		t.Fatalf("NewHandler(nil log): expected error, got nil")
	}
}

// ── Test 3: EventType + IdempotencyKey stable ─────────────────────────

func TestHandler_EventTypeAndIdempotencyKeyStable(t *testing.T) {
	h, _, _ := newHandler(t)
	if got, want := h.EventType(), publishoutbox.EventTypeArtifactPublishRequested; got != want {
		t.Errorf("EventType() = %q, want %q", got, want)
	}
	if got, want := h.IdempotencyKey(), publishoutbox.EventTypeArtifactPublishRequested; got != want {
		t.Errorf("IdempotencyKey() = %q, want %q", got, want)
	}
}

// ── Test 4: Happy path (decode → Stage.Stage + structured log) ────────

func TestHandler_Handle_HappyPath(t *testing.T) {
	sourcePath := writeSourceFile(t, "publish-request-payload-bytes")
	h, store, logs := newHandler(t)
	// Inject a canned receipt so the handler can read
	// receipt.ID + receipt.EventKey for the structured log.
	store.receipt = &staging.StageReceipt{
		ID:        "art-test-1",
		Hash:      "deadbeef",
		Size:      int64(len("publish-request-payload-bytes")),
		LocalPath: "/tmp/staged",
		EventKey:  "stage:job-x:art-test-1",
	}

	payload := validRequestPayload()
	payload.SourceURI = "file://" + sourcePath
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal payload: %v", err)
	}

	if err := h.Handle(context.Background(), makeEvent(string(body), "req-key-1")); err != nil {
		t.Fatalf("Handle: unexpected error: %v", err)
	}

	// 1. Store.Stage called exactly once with the decoded
	//    StageRequest fields (the handler is a thin adapter).
	if got := len(store.calls); got != 1 {
		t.Fatalf("Store.Stage call count = %d, want 1", got)
	}
	got := store.calls[0]
	if got.JobID != "job-x" {
		t.Errorf("StageRequest.JobID = %q, want job-x", got.JobID)
	}
	if got.Mime != "audio/mpeg" {
		t.Errorf("StageRequest.Mime = %q, want audio/mpeg", got.Mime)
	}
	if got.Requirement != artifact.RequirementRequired {
		t.Errorf("StageRequest.Requirement = %q, want required", got.Requirement)
	}
	if got.Destination != "drive:voiceover/test" {
		t.Errorf("StageRequest.Destination = %q, want drive:voiceover/test", got.Destination)
	}
	if got.Content == nil {
		t.Errorf("StageRequest.Content is nil (handler MUST pass an io.Reader)")
	}

	// 2. Structured log: exactly one Info entry; message
	//    contains "stage materialized"; expected fields
	//    present for downstream telemetry correlation.
	entries := logs.All()
	if got := len(entries); got != 1 {
		t.Fatalf("log entry count = %d, want 1", got)
	}
	if entries[0].Level != zapcore.InfoLevel {
		t.Errorf("log level = %v, want InfoLevel", entries[0].Level)
	}
	if !strings.Contains(entries[0].Message, "stage materialized") {
		t.Errorf("log message = %q, want contains %q", entries[0].Message, "stage materialized")
	}
	fieldSet := map[string]bool{}
	for _, f := range entries[0].Context {
		fieldSet[f.Key] = true
	}
	for _, want := range []string{"event_type", "job_id", "stage_id", "stage_event_key", "request_event_key", "hash", "size", "requirement", "destination"} {
		if !fieldSet[want] {
			t.Errorf("log fields missing canonical key: %q", want)
		}
	}
}

// ── Test 5: malformed JSON → ErrInvalidPayload (no Store.Stage call) ─

func TestHandler_Handle_RejectsMalformedJSON(t *testing.T) {
	h, store, _ := newHandler(t)
	err := h.Handle(context.Background(), makeEvent("[not-json", ""))
	if !errors.Is(err, publishoutbox.ErrInvalidPayload) {
		t.Errorf("Handle malformed JSON: err = %v, want ErrInvalidPayload", err)
	}
	if got := len(store.calls); got != 0 {
		t.Errorf("Store.Stage calls = %d, want 0 (validate gate fires before stage)", got)
	}
}

// ── Test 6: missing JobID / Mime / SourceURI → ErrMissingFields ───────

func TestHandler_Handle_RejectsMissingFields(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*publishoutbox.PublishRequestPayload)
	}{
		{"missing JobID", func(p *publishoutbox.PublishRequestPayload) { p.JobID = "" }},
		{"missing Mime", func(p *publishoutbox.PublishRequestPayload) { p.Mime = "" }},
		{"missing SourceURI", func(p *publishoutbox.PublishRequestPayload) { p.SourceURI = "" }},
		{"whitespace-only JobID", func(p *publishoutbox.PublishRequestPayload) { p.JobID = "   " }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, store, _ := newHandler(t)
			p := validRequestPayload()
			tc.mutate(&p)
			// SourceURI must be valid for jobs that don't test
			// the missing-URI gate, so supply a real one for
			// the JobID / Mime removal subtests. Do NOT supply
			// a SourceURI for the missing-SourceURI case, or the
			// validation gate is bypassed and the test reaches
			// Store.Stage with a nil receipt stub.
			if p.SourceURI == "" && tc.name != "missing SourceURI" {
				p.SourceURI = writeSourceFile(t, "x")
			}
			body, _ := json.Marshal(p)
			err := h.Handle(context.Background(), makeEvent(string(body), ""))
			if !errors.Is(err, publishoutbox.ErrMissingFields) {
				t.Errorf("err = %v, want ErrMissingFields", err)
			}
			if got := len(store.calls); got != 0 {
				t.Errorf("Store.Stage calls = %d, want 0", got)
			}
		})
	}
}

// ── Test 7: out-of-set Requirement → ErrInvalidPayload ────────────────

func TestHandler_Handle_RejectsInvalidRequirement(t *testing.T) {
	h, store, _ := newHandler(t)
	p := validRequestPayload()
	p.SourceURI = writeSourceFile(t, "x")
	p.Requirement = "mandatory" // not in canonical {optional, required}
	body, _ := json.Marshal(p)
	err := h.Handle(context.Background(), makeEvent(string(body), ""))
	if !errors.Is(err, publishoutbox.ErrInvalidPayload) {
		t.Errorf("err = %v, want ErrInvalidPayload", err)
	}
	if got := len(store.calls); got != 0 {
		t.Errorf("Store.Stage calls = %d, want 0", got)
	}
}

// ── Test 8: empty Requirement defaults to "optional" ──────────────────

func TestHandler_Handle_DefaultsRequirementToOptional(t *testing.T) {
	h, store, _ := newHandler(t)
	store.receipt = &staging.StageReceipt{
		ID: "art-1", EventKey: "stage:job-x:art-1",
		LocalPath: "/tmp/x", Hash: "x", Size: 1,
	}
	p := validRequestPayload()
	p.SourceURI = writeSourceFile(t, "x")
	p.Requirement = "" // empty → default to optional
	body, _ := json.Marshal(p)
	if err := h.Handle(context.Background(), makeEvent(string(body), "")); err != nil {
		t.Fatalf("Handle defaults-requirement: %v", err)
	}
	if got := store.calls[0].Requirement; got != artifact.RequirementOptional {
		t.Errorf("Requirement = %q, want optional", got)
	}
}

// ── Test 9: non-openable SourceURI → ErrSourceOpen ───────────────────

func TestHandler_Handle_RejectsNonOpenableSourceURI(t *testing.T) {
	h, store, _ := newHandler(t)
	p := validRequestPayload()
	p.SourceURI = "/this/path/does/not/exist/" + t.Name()
	body, _ := json.Marshal(p)
	err := h.Handle(context.Background(), makeEvent(string(body), ""))
	if !errors.Is(err, publishoutbox.ErrSourceOpen) {
		t.Errorf("err = %v, want ErrSourceOpen", err)
	}
	if got := len(store.calls); got != 0 {
		t.Errorf("Store.Stage calls = %d, want 0", got)
	}
}

// ── Test 10: Store.Stage failure → wrapped error surfaced ────────────

func TestHandler_Handle_StoreStageFailure_SurfacedWrapped(t *testing.T) {
	sourcePath := writeSourceFile(t, "x")
	h, store, _ := newHandler(t)
	injectedErr := errors.New("simulated stage failure")
	store.err = injectedErr

	p := validRequestPayload()
	p.SourceURI = sourcePath
	body, _ := json.Marshal(p)
	err := h.Handle(context.Background(), makeEvent(string(body), ""))
	if err == nil {
		t.Fatalf("Handle: expected non-nil error (Store.Stage failure)")
	}
	if !strings.Contains(err.Error(), "Store.Stage") {
		t.Errorf("err = %v, want contains 'Store.Stage'", err)
	}
	if !errors.Is(err, injectedErr) {
		t.Errorf("err chain does not include injected sentinel: %v", err)
	}
}

// ── Test 11: file:// prefix stripped cleanly ─────────────────────────

func TestHandler_Handle_StripsFileScheme(t *testing.T) {
	sourcePath := writeSourceFile(t, "x")
	h, store, _ := newHandler(t)
	// No receipt -> store.call returns (nil, nil) which would
	// crash on receipt.ID below; inject a minimal stub.
	store.receipt = &staging.StageReceipt{
		ID: "art-1", EventKey: "stage:job-x:art-1",
		LocalPath: "/tmp/x", Hash: "x", Size: 1,
	}
	p := validRequestPayload()
	p.SourceURI = "file://" + sourcePath
	body, _ := json.Marshal(p)
	if err := h.Handle(context.Background(), makeEvent(string(body), "")); err != nil {
		t.Errorf("Handle file:// URI: %v", err)
	}
	if got := len(store.calls); got != 1 {
		t.Errorf("Store.Stage calls = %d, want 1", got)
	}
}
