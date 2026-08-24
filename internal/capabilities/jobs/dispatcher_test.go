// Package jobs — dispatcher_test.go (P0 Commit 4, July 2026).
//
// Unit tests for the new Dispatcher.Enqueue method (dispatcher.go).
// The tests exercise the canonical failure-path matrix (nil receiver,
// missing enqueuer, unfrozen registry, unknown jobType, missing codec,
// encode failure) plus the happy-path round-trip (stub EnqueuePort).
//
// Test design notes:
//
//   - Pure unit tests. No SQLite, no goroutines, no time.Now().
//     The validateEnqueueRequest + idempotency + UNIQUE-rescue paths
//     belong to *Service.Enqueue (service.go), which is the upstream
//     for every Dispatcher.Enqueue. Dispatcher.Enqueue itself is a
//     thin typed gateway; testing it does NOT require a database.
//
//   - stubEnqueuePort satisfies the EnqueuePort interface without
//     depending on *Service, so the test file can be read without
//     importing the entire jobs package wiring.
//
//   - The "happy path" test exercises the full flow end-to-end:
//     build a real CompiledJobRegistry with one canonical definition,
//     attach it to a *Dispatcher via WithRegistry, attach a stub
//     EnqueuePort via SetEnqueuer, call Enqueue, and verify the stub
//     received the canonical Type + Payload bytes with no DB round-trip.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── stub EnqueuePort ───────────────────────────────────────────────

// stubEnqueuePort satisfies EnqueuePort without the *Service wiring.
// Records every Enqueue call so tests can assert the canonical
// (Type, Payload-byte) pair arrived at the port.
type stubEnqueuePort struct {
	mu        sync.Mutex
	calls     int
	lastReq   *job.EnqueueRequest
	returnJob *job.Job
	returnErr error
}

func (s *stubEnqueuePort) Enqueue(_ context.Context, req *job.EnqueueRequest) (*job.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.lastReq = req
	return s.returnJob, s.returnErr
}

// ── test fixtures ──────────────────────────────────────────────────

// buildTestRegistry returns a frozen CompiledJobRegistry containing a
// single canonical JobDefinition for "test.dispatcher". The definition
// carries a marker PayloadCodec that does identity round-trip
// (json.Marshal + json.Unmarshal-to-map), matching the C3 marker
// helper used by canonical_definitions.go.
func buildTestRegistry(t *testing.T) job.CompiledJobRegistry {
	t.Helper()
	def := job.JobDefinition{
		Type:           "test.dispatcher",
		ExecutionClass: job.ExecutionCreatorAllowed,
		Queue:          "default",
		Timeout:        0,
		PayloadCodec: job.NewCodecDescriptorMarker(
			"pipelinegen.payload.test.dispatcher.v1", "test.dispatcher",
		),
		ResultCodec: job.NewCodecDescriptorMarker(
			"pipelinegen.result.test.dispatcher.v1", "test.dispatcher",
		),
		ArtifactPolicy: job.ArtifactPolicy{
			ProducesArtifacts: true,
			RequireManifest:   true,
		},
		RequiredCapabilities: []job.Capability{
			job.Capability("test.cap"),
		},
	}
	mutable := job.NewMutableJobRegistry()
	if err := mutable.RegisterDefinition(def); err != nil {
		t.Fatalf("RegisterDefinition: %v", err)
	}
	compiled, err := mutable.Freeze()
	if err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	return compiled
}

// ── failure-path tests ─────────────────────────────────────────────

// TestDispatcher_Enqueue_NilReceiver returns ErrEnqueuerNotWired
// when the receiver is nil. Defensive guard for nil-tolerance.
func TestDispatcher_Enqueue_NilReceiver(t *testing.T) {
	t.Parallel()
	var d *Dispatcher
	_, err := d.Enqueue(context.Background(), "test.dispatcher", nil)
	if err == nil {
		t.Fatal("expected error on nil receiver, got nil")
	}
	if !errors.Is(err, ErrEnqueuerNotWired) {
		t.Errorf("expected ErrEnqueuerNotWired, got %v", err)
	}
}

// TestDispatcher_Enqueue_NilEnqueuer returns ErrEnqueuerNotWired
// when SetEnqueuer has not been called. Composition root bug.
func TestDispatcher_Enqueue_NilEnqueuer(t *testing.T) {
	t.Parallel()
	d := NewDispatcher()
	compiled := buildTestRegistry(t)
	d.WithRegistry(compiled)
	// Intentionally NOT calling SetEnqueuer.

	_, err := d.Enqueue(context.Background(), "test.dispatcher", map[string]any{"k": "v"})
	if err == nil {
		t.Fatal("expected error on nil enqueuer, got nil")
	}
	if !errors.Is(err, ErrEnqueuerNotWired) {
		t.Errorf("expected ErrEnqueuerNotWired, got %v", err)
	}
}

// TestDispatcher_Enqueue_NilRegistry returns ErrRegistryNotFrozen
// when WithRegistry(nil) was called (or never called). Composition bug.
func TestDispatcher_Enqueue_NilRegistry(t *testing.T) {
	t.Parallel()
	d := NewDispatcher()
	d.SetEnqueuer(&stubEnqueuePort{})

	_, err := d.Enqueue(context.Background(), "test.dispatcher", nil)
	if err == nil {
		t.Fatal("expected error on nil registry, got nil")
	}
	if !errors.Is(err, ErrRegistryNotFrozen) {
		t.Errorf("expected ErrRegistryNotFrozen, got %v", err)
	}
}

// unfrozenRegistryStub is a CompiledJobRegistry-shaped test double
// whose IsFrozen() returns false. Job.CompiledJobRegistry is a Go
// interface; the canonical production surface (readOnlyRegistry,
// returned by builderRegistry.Freeze) reports IsFrozen()=true. We
// build this stub to exercise the defense-in-depth path that catches
// a misconfigured wiring (e.g. a future contributor wires the
// MutableJobRegistry by mistake).
type unfrozenRegistryStub struct {
	def job.JobDefinition
}

func (u *unfrozenRegistryStub) Definition(_ string) (job.JobDefinition, bool) {
	return u.def, true
}
func (u *unfrozenRegistryStub) Handler(_ string) (job.JobHandlerFunc, bool) {
	return nil, false
}
func (u *unfrozenRegistryStub) HasHandler(_ string) bool { return false }
func (u *unfrozenRegistryStub) AllDefinitions() []job.JobDefinition {
	return []job.JobDefinition{u.def}
}
func (u *unfrozenRegistryStub) CreatorJobTypes() []string { return []string{u.def.Type} }
func (u *unfrozenRegistryStub) CreatorCapabilities() []job.Capability {
	return u.def.RequiredCapabilities
}
func (u *unfrozenRegistryStub) ValidateWorkflow(types []string) error { return nil }
func (u *unfrozenRegistryStub) IsFrozen() bool                        { return false }

// TestDispatcher_Enqueue_UnfrozenRegistry returns ErrRegistryNotFrozen
// when the attached CompiledJobRegistry reports IsFrozen()=false. This
// is the defense-in-depth path: the canonical production readOnlyRegistry
// always reports IsFrozen()=true (the builderRegistry.Freeze returns
// a readOnlyRegistry that hardcodes IsFrozen() → true). This test
// catches a future misuse where a MutableJobRegistry is wired into
// Dispatcher.WithRegistry by mistake.
func TestDispatcher_Enqueue_UnfrozenRegistry(t *testing.T) {
	t.Parallel()
	d := NewDispatcher()
	d.SetEnqueuer(&stubEnqueuePort{})
	d.registry = &unfrozenRegistryStub{
		def: job.JobDefinition{Type: "test.unfrozen"},
	}

	_, err := d.Enqueue(context.Background(), "test.unfrozen", nil)
	if err == nil {
		t.Fatal("expected error on unfrozen registry, got nil")
	}
	if !errors.Is(err, ErrRegistryNotFrozen) {
		t.Errorf("expected ErrRegistryNotFrozen, got %v", err)
	}
}

// TestDispatcher_Enqueue_UnknownJobType returns wrapped job.ErrUnknownJobType
// when the jobType is not registered. The wrap preserves the canonical
// C3 sentinel for errors.Is judgment at the caller.
func TestDispatcher_Enqueue_UnknownJobType(t *testing.T) {
	t.Parallel()
	d := NewDispatcher()
	d.WithRegistry(buildTestRegistry(t))
	d.SetEnqueuer(&stubEnqueuePort{})

	_, err := d.Enqueue(context.Background(), "test.unregistered", nil)
	if err == nil {
		t.Fatal("expected error on unknown jobType, got nil")
	}
	if !errors.Is(err, job.ErrUnknownJobType) {
		t.Errorf("expected job.ErrUnknownJobType, got %v", err)
	}
}

// buildTestRegistryNoPayloadCodec returns a frozen registry with a
// single JobDefinition whose PayloadCodec is nil. Used to exercise the
// ErrCodecMissing defense-in-depth path.
func buildTestRegistryNoPayloadCodec(t *testing.T) job.CompiledJobRegistry {
	t.Helper()
	def := job.JobDefinition{
		Type:           "test.noCodec",
		ExecutionClass: job.ExecutionSenderOnly, // sender-only bypasses RequiredCapabilities
		Queue:          "default",
		// PayloadCodec intentionally nil.
		// ResultCodec is irrelevant for the dispatch path; set a marker
		// so the JobDefinition passes the C3 Validate() (which checks
		// Queue, ExecutionClass, ArtifactPolicy — does NOT check codecs).
		ResultCodec:    job.NewCodecDescriptorMarker("v1", "test.noCodec"),
		ArtifactPolicy: job.ArtifactPolicy{},
	}
	mutable := job.NewMutableJobRegistry()
	if err := mutable.RegisterDefinition(def); err != nil {
		t.Fatalf("RegisterDefinition: %v", err)
	}
	compiled, err := mutable.Freeze()
	if err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	return compiled
}

// TestDispatcher_Enqueue_MissingPayloadCodec returns ErrCodecMissing
// when the JobDefinition has no PayloadCodec. Defense-in-depth against
// a post-Freeze mutation that nil-ed the codec.
func TestDispatcher_Enqueue_MissingPayloadCodec(t *testing.T) {
	t.Parallel()
	d := NewDispatcher()
	d.WithRegistry(buildTestRegistryNoPayloadCodec(t))
	stub := &stubEnqueuePort{}
	d.SetEnqueuer(stub)

	_, err := d.Enqueue(context.Background(), "test.noCodec", nil)
	if err == nil {
		t.Fatal("expected error on missing PayloadCodec, got nil")
	}
	if !errors.Is(err, ErrCodecMissing) {
		t.Errorf("expected ErrCodecMissing, got %v", err)
	}
	if stub.calls != 0 {
		t.Errorf("enqueuer must NOT have been called when codec is missing (got calls=%d)", stub.calls)
	}
}

// ── happy-path round-trip ──────────────────────────────────────────

// TestDispatcher_Enqueue_HappyPath_PINSCONTRACT is the load-bearing
// test: it exercises the full Enqueue flow end-to-end against a
// stubbed EnqueuePort, asserts the canonical (Type, raw-bytes) pair
// arrives at the port, and verifies the stub's returnJob bubbles
// back through the call.
func TestDispatcher_Enqueue_HappyPath_PINSCONTRACT(t *testing.T) {
	t.Parallel()

	d := NewDispatcher()
	d.WithRegistry(buildTestRegistry(t))
	stub := &stubEnqueuePort{
		returnJob: &job.Job{ID: "job_test_001", Type: "test.dispatcher"},
	}
	d.SetEnqueuer(stub)

	typedPayload := map[string]any{
		"topic":      "space exploration",
		"sentences":  42,
		"is_explore": true,
	}
	got, err := d.Enqueue(context.Background(), "test.dispatcher", typedPayload)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if got == nil || got.ID != "job_test_001" {
		t.Errorf("expected returnJob to bubble up, got %+v", got)
	}

	// Verify stub received the canonical call.
	if stub.calls != 1 {
		t.Fatalf("expected exactly 1 Enqueue call to the port, got %d", stub.calls)
	}
	if stub.lastReq == nil {
		t.Fatal("stub did not record the request")
	}
	if stub.lastReq.Type != "test.dispatcher" {
		t.Errorf("stub.lastReq.Type = %q, want test.dispatcher", stub.lastReq.Type)
	}

	// Verify the payload bytes survive round-trip. The C3 marker codec
	// does identity json.Marshal/Unmarshal; reusing json.Unmarshal here
	// proves the bytes are sensible canonical JSON (key order, types
	// preserved). The test does NOT require field-by-field equality
	// (json.Marshal does not guarantee key order), only that the bytes
	// decode back to a map[string]any with the expected fields.
	//
	// *job.EnqueueRequest.Payload is declared `any` (kernel-aliased
	// per internal/domain/job/service.go::type EnqueueRequest =
	// job.EnqueueRequest). The canonical Dispatcher.Enqueue
	// surface stores `json.RawMessage` (the codec-encoded bytes) into
	// Payload; the type assertion below narrows the interface back to
	// its concrete shape so json.Unmarshal(data []byte, ...) accepts it.
	// Prefer the explicit `json.RawMessage` cast over `[]byte` so the
	// contract reads as "the codec-encoded wire bytes" (not "any byte
	// slice"), and a future PR that stores a different concrete type
	// fails loudly with a self-documenting diagnostic.
	rawMessage, ok := stub.lastReq.Payload.(json.RawMessage)
	if !ok {
		t.Fatalf("stub.lastReq.Payload is not json.RawMessage (got %T) — Dispatcher.Enqueue must store json.RawMessage into Payload", stub.lastReq.Payload)
	}
	var decoded map[string]any
	if err := json.Unmarshal(rawMessage, &decoded); err != nil {
		t.Fatalf("stub.lastReq.Payload decode: %v (raw bytes=%s)", err, string(rawMessage))
	}
	if decoded["topic"] != "space exploration" {
		t.Errorf("decoded topic = %v, want space exploration", decoded["topic"])
	}
}

// TestDispatcher_Enqueue_EncodeError_RefusalTypeMismatch verifies that
// the dispatch boundary surfaces ErrInvalidPayload when the codec's
// EncodePayload returns a typed error (e.g. TypedCodecAdapter's
// reflect-based type-mismatch).
//
// We bypass the C3 marker codec for this test by attaching an ad-hoc
// PayloadCodec whose EncodePayload returns a known sentinel error.
type errCodec struct {
	job.CodecDescriptorMarker
	sentinel error
}

func (e *errCodec) EncodePayload(_ any) (json.RawMessage, error) {
	return nil, e.sentinel
}

var errMarkerSentinel = errors.New("test: codec encode fails on this payload")

func TestDispatcher_Enqueue_EncodeError_RefusalTypeMismatch(t *testing.T) {
	t.Parallel()

	// Build a registry with a faulty codec.
	def := job.JobDefinition{
		Type:           "test.errCodec",
		ExecutionClass: job.ExecutionCreatorAllowed,
		Queue:          "default",
		PayloadCodec: &errCodec{
			CodecDescriptorMarker: job.NewCodecDescriptorMarker("v1", "test.errCodec"),
			sentinel:              errMarkerSentinel,
		},
		ResultCodec:          job.NewCodecDescriptorMarker("v1", "test.errCodec"),
		ArtifactPolicy:       job.ArtifactPolicy{ProducesArtifacts: true},
		RequiredCapabilities: []job.Capability{"x"},
	}
	mutable := job.NewMutableJobRegistry()
	if err := mutable.RegisterDefinition(def); err != nil {
		t.Fatalf("RegisterDefinition: %v", err)
	}
	compiled, err := mutable.Freeze()
	if err != nil {
		t.Fatalf("Freeze: %v", err)
	}

	d := NewDispatcher()
	d.WithRegistry(compiled)
	stub := &stubEnqueuePort{}
	d.SetEnqueuer(stub)

	_, err = d.Enqueue(context.Background(), "test.errCodec", struct{ Foo string }{Foo: "bar"})
	if err == nil {
		t.Fatal("expected encode error, got nil")
	}
	if !errors.Is(err, ErrInvalidPayload) {
		t.Errorf("expected ErrInvalidPayload, got %v", err)
	}
	if stub.calls != 0 {
		t.Errorf("enqueuer must NOT have been called when encode fails (got calls=%d)", stub.calls)
	}
}

// ── fluent API tests ───────────────────────────────────────────────

// TestDispatcher_WithRegistry_NilReceiver guards against the nil-d
// fluent chaining path. WithRegistry(nil) returns the receiver
// unchanged.
func TestDispatcher_WithRegistry_NilReceiver(t *testing.T) {
	t.Parallel()
	var d *Dispatcher
	got := d.WithRegistry(buildTestRegistry(t))
	if got != nil {
		t.Error("nil receiver fluent setter must return nil")
	}
}

// TestDispatcher_SetEnqueuer_NilReceiver guards the equivalent path
// for SetEnqueuer.
func TestDispatcher_SetEnqueuer_NilReceiver(t *testing.T) {
	t.Parallel()
	var d *Dispatcher
	got := d.SetEnqueuer(&stubEnqueuePort{})
	if got != nil {
		t.Error("nil receiver fluent setter must return nil")
	}
}
