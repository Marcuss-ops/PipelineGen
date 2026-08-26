// Package texttracks — fanout_test.go: hermetic tests for
// MaterializeFanOut (post-publish → asset.text.materialize
// enqueue helper).
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 3 (July 2026).
//
// godlike/07 hermetic coverage: every probe pins (a) payload
// construction (Type, ActiveKey, marshaled JSON shape), (b)
// typed-error short-circuit on empty args, (c) broker-error
// propagation, (d) ActiveKey composition collapse invariant
// ((asset, source) repeats collapse to the same key for the
// broker-side UNIQUE-constraint rescue).
//
// godlike/06 SSOT: the narrow MaterializeEnqueuer port
// (defined in fanout.go next to the helper that consumes it)
// makes hermetic testing trivial — the tests below record
// the (Type, Payload, ActiveKey) tuple directly through a
// in-package stub. No *appjobs.Service / DB / dispatcher
// ceremony.
//
// POST-FIX PRODUCER CONTRACT (godlike/06 SSOT, July 2026):
// the fanout hands the broker a typed struct
// (MaterializeJobPayload) — NOT pre-marshaled
// bytes — so the broker's canonical Service.Enqueue can
// perform the SINGLE json.Marshal(req.Payload) that derives
// the SQLite payload_json bytes. The pre-fix design
// (pre-marshal → []byte → broker re-marshals → base64-stripped
// JSON string → cannot unmarshal on the worker side) is the
// regression this test pins AGAINST, via the stubEnqueuer
// contract panic and the marshal→unmarshal wire-shape probe.
package texttracks

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// stubEnqueuer is a thin MaterializeEnqueuer stub: records every
// Enqueue call (deep-copy of payload bytes so post-call
// mutations don't bleed into the assertion) and optionally
// returns a hook error.
type stubEnqueuer struct {
	mu       sync.Mutex
	calls    int32
	recorded []enqueueJob
	hookErr  error
}

// enqueueJob is the minimal snapshot the test asserts against.
// We don't record the full *job.Job (the fanout doesn't read
// the response) — just the dispatch contract.
//
// POST-FIX (godlike/06): Payload is `any` (the post-fix producer
// contract hands the broker a typed struct — the broker
// marshals it once via Service.Enqueue). The probes inspect
// fields directly + marshal locally for the wire-shape pin.
type enqueueJob struct {
	Type      string
	Payload   any
	ActiveKey string
}

func (s *stubEnqueuer) Enqueue(_ context.Context, req *job.EnqueueRequest) (*job.Job, error) {
	atomic.AddInt32(&s.calls, 1)
	s.mu.Lock()
	defer s.mu.Unlock()
	// POST-FIX PRODUCER CONTRACT (godlike/06 SSOT): the fanout
	// hands the broker a typed MaterializeJobPayload
	// struct (NOT pre-marshaled []byte, NOT map[string]any).
	// The broker's canonical Service.Enqueue performs the
	// SINGLE json.Marshal(req.Payload) that writes payload_json
	// to SQLite. A type assertion mismatch here means a future
	// contributor changed the fanout contract and re-introduced
	// the double-marshal base64-encoding regression. HARD-FAIL
	// on contract violation rather than silently recording the
	// wrong shape.
	//
	// godlike/07 fail-closed: the stub does NOT take *testing.T
	// (the MaterializeEnqueuer surface is broker-typed, not
	// test-typed), so we panic on contract violation. A panic
	// during `go test` is a hard fail signal that the test runner
	// surfaces as a FAIL line — louder than t.Fatalf and bounded
	// to test-process scope, with zero surface-area impact on
	// the production broker.
	ps, ok := req.Payload.(MaterializeJobPayload)
	if !ok {
		panic("stubEnqueuer.Enqueue: expected MaterializeJobPayload struct Payload (fanout producer contract regression: producer must hand the broker a typed struct, NOT pre-marshaled bytes — pre-marshal design caused base64 double-encoding regression on July 2026)")
	}
	s.recorded = append(s.recorded, enqueueJob{
		Type:      req.Type,
		Payload:   ps,
		ActiveKey: req.ActiveKey,
	})
	if s.hookErr != nil {
		return nil, s.hookErr
	}
	return &job.Job{ID: "stub-" + req.Type, Type: req.Type}, nil
}

func (s *stubEnqueuer) last() enqueueJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n := len(s.recorded); n > 0 {
		return s.recorded[n-1]
	}
	return enqueueJob{}
}

// Compile-time pin: stubEnqueuer satisfies the texttracks
// MaterializeEnqueuer surface. AGENTS.md Pattern 0.
var _ MaterializeEnqueuer = (*stubEnqueuer)(nil)

// ── Probe 1: happy path enqueues with correct type + active_key + payload ────

func TestFanOut_EnqueueMaterializeOne_Success(t *testing.T) {
	stub := &stubEnqueuer{}
	f := NewMaterializeFanOut(stub, nil) // nil log → zap.NewNop() fallback

	const (
		assetID = "asset-yt-001"
		hashVal = "abc123def456"
		lang    = "en"
	)
	kinds := []detail.TextTrackKind{detail.TextTrackTranscript}

	if err := f.EnqueueMaterializeOne(
		context.Background(), assetID, lang, hashVal, kinds,
	); err != nil {
		t.Fatalf("EnqueueMaterializeOne returned error: %v", err)
	}

	if stub.calls != 1 {
		t.Fatalf("expected 1 Enqueue call, got %d", stub.calls)
	}
	last := stub.last()
	if last.Type != job.TypeAssetTextMaterialize {
		t.Fatalf("Type = %q, want %q", last.Type, job.TypeAssetTextMaterialize)
	}

	wantActiveKey := "asset.text.materialize:" + assetID + ":" + hashVal
	if last.ActiveKey != wantActiveKey {
		t.Fatalf("ActiveKey = %q, want %q", last.ActiveKey, wantActiveKey)
	}

	// Payload field assertions (post-fix producer contract hands
	// the broker a typed struct; probes inspect fields directly).
	ps, ok := last.Payload.(MaterializeJobPayload)
	if !ok {
		t.Fatalf("Payload type = %T, want MaterializeJobPayload", last.Payload)
	}
	if ps.AssetID != assetID {
		t.Fatalf("AssetID = %q, want %q", ps.AssetID, assetID)
	}
	if ps.SourceLanguage != lang {
		t.Fatalf("SourceLanguage = %q, want %q", ps.SourceLanguage, lang)
	}
	if ps.SourceTextHash != hashVal {
		t.Fatalf("SourceTextHash = %q, want %q", ps.SourceTextHash, hashVal)
	}
	if len(ps.TextKinds) != 1 || ps.TextKinds[0] != string(detail.TextTrackTranscript) {
		t.Fatalf("TextKinds = %v, want [%q]", ps.TextKinds, detail.TextTrackTranscript)
	}
	if ps.TargetLanguages != nil && len(ps.TargetLanguages) > 0 {
		// omitempty: nil target_languages is absent in the
		// JSON wire form — the fanout MUST NOT pre-populate
		// a target override from the helper.
		t.Fatalf("TargetLanguages unexpected non-empty: %v", ps.TargetLanguages)
	}

	// Wire-shape pin (godlike/06 SSOT): the CANONICAL broker
	// marshaler produces the expected JSON tag shape that
	// flows into SQLite payload_json and round-trips through
	// the worker. We marshal the struct locally and inspect
	// the raw keys — protects against future struct-tag drift
	// (e.g. someone renaming AssetID PascalCase to camelCase,
	// breaking the worker's json.Unmarshal into MaterializeJobPayload).
	wireBytes, err := json.Marshal(ps)
	if err != nil {
		t.Fatalf("payload marshal: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(wireBytes, &raw); err != nil {
		t.Fatalf("wire-shape map unmarshal: %v", err)
	}
	for _, wantKey := range []string{
		"asset_id", "source_language", "source_text_hash", "text_kinds",
	} {
		if _, ok := raw[wantKey]; !ok {
			t.Fatalf("wire shape missing %q key (struct tag drift; the worker json.Unmarshal into MaterializeJobPayload will fail at runtime)", wantKey)
		}
	}
}

// ── Probe 2: empty args surface typed ErrInvalidMaterializeRequest ────

func TestFanOut_EnqueueMaterializeOne_InvalidArgs(t *testing.T) {
	cases := []struct {
		name      string
		assetID   string
		lang      string
		hash      string
		kinds     []detail.TextTrackKind
		wantField string
	}{
		{
			name:      "empty asset_id",
			assetID:   "",
			lang:      "en",
			hash:      "h",
			kinds:     []detail.TextTrackKind{detail.TextTrackTranscript},
			wantField: "asset_id",
		},
		{
			name:      "empty source_language",
			assetID:   "a",
			lang:      "",
			hash:      "h",
			kinds:     []detail.TextTrackKind{detail.TextTrackTranscript},
			wantField: "source_language",
		},
		{
			name:      "empty source_text_hash",
			assetID:   "a",
			lang:      "en",
			hash:      "",
			kinds:     []detail.TextTrackKind{detail.TextTrackTranscript},
			wantField: "source_text_hash",
		},
		{
			name:      "empty text_kinds",
			assetID:   "a",
			lang:      "en",
			hash:      "h",
			kinds:     nil,
			wantField: "text_kinds",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubEnqueuer{}
			f := NewMaterializeFanOut(stub, nil)

			err := f.EnqueueMaterializeOne(
				context.Background(), tc.assetID, tc.lang, tc.hash, tc.kinds,
			)
			if err == nil {
				t.Fatalf("expected ErrInvalidMaterializeRequest, got nil")
			}
			var typed *ErrInvalidMaterializeRequest
			if !errors.As(err, &typed) {
				t.Fatalf("expected *ErrInvalidMaterializeRequest, got %T: %v", err, err)
			}
			if typed.Field != tc.wantField {
				t.Fatalf("typed.Field = %q, want %q", typed.Field, tc.wantField)
			}
			if stub.calls != 0 {
				t.Fatalf("expected 0 Enqueue calls on invalid arg, got %d", stub.calls)
			}
		})
	}
}

// ── Probe 3: broker error is wrapped + propagated ─────────────────

func TestFanOut_EnqueueMaterializeOne_BrokerErrorWrapped(t *testing.T) {
	stub := &stubEnqueuer{hookErr: errors.New("simulated broker down")}
	f := NewMaterializeFanOut(stub, nil)

	err := f.EnqueueMaterializeOne(
		context.Background(), "asset-x", "en", "hash",
		[]detail.TextTrackKind{detail.TextTrackTranscript},
	)
	if err == nil {
		t.Fatal("expected wrapped broker error, got nil")
	}
	if !strings.Contains(err.Error(), "simulated broker down") {
		t.Fatalf("err = %q, want substring %q", err.Error(), "simulated broker down")
	}
	if !strings.Contains(err.Error(), "texttracks.fanout.enqueue") {
		t.Fatalf("err = %q, want substring %q", err.Error(), "texttracks.fanout.enqueue")
	}
	// errors.Is probe — the underlying broker error is in the
	// chain so callers can errors.Is against the broker-sent
	// error. (Note: t.Fatalf does NOT support the %w verb;
	// gofmt + go vet surface this as a format-mismatch. We use
	// errors.Is separately to test the wrap, then assert the
	// result with a plain guard.)
	if !errors.Is(err, stub.hookErr) {
		t.Fatalf("errors.Is(stub.hookErr): expected true (broker-error wrap chain broken)")
	}
}

// ── Probe 4: multiple kinds are preserved in the payload ──────────

func TestFanOut_EnqueueMaterializeOne_MultipleKindsPreserved(t *testing.T) {
	stub := &stubEnqueuer{}
	f := NewMaterializeFanOut(stub, nil)

	kinds := []detail.TextTrackKind{
		detail.TextTrackTranscript,
		detail.TextTrackDescription,
		detail.TextTrackSummary,
	}
	if err := f.EnqueueMaterializeOne(
		context.Background(), "asset-multi", "en", "h", kinds,
	); err != nil {
		t.Fatalf("EnqueueMaterializeOne returned error: %v", err)
	}
	last := stub.last()
	ps, ok := last.Payload.(MaterializeJobPayload)
	if !ok {
		t.Fatalf("Payload type = %T, want MaterializeJobPayload", last.Payload)
	}
	if len(ps.TextKinds) != len(kinds) {
		t.Fatalf("TextKinds len = %d, want %d", len(ps.TextKinds), len(kinds))
	}
	for i, k := range kinds {
		if i >= len(ps.TextKinds) || ps.TextKinds[i] != string(k) {
			t.Fatalf("TextKinds[%d] = %q, want %q", i,
				safeIndex(ps.TextKinds, i), string(k))
		}
	}

	// Wire-shape pin: the multiple-kinds list survives the
	// canonical broker marshaler round-trip. Same marshaler
	// the broker uses, same json.RawMessage probe that Probe
	// 1 uses — keeping wire-shape coverage parallel between
	// probes reduces drift risk.
	wireBytes, err := json.Marshal(ps)
	if err != nil {
		t.Fatalf("payload marshal: %v", err)
	}
	var rt MaterializeJobPayload
	if err := json.Unmarshal(wireBytes, &rt); err != nil {
		t.Fatalf("payload round-trip unmarshal: %v", err)
	}
	if len(rt.TextKinds) != len(kinds) {
		t.Fatalf("round-trip TextKinds len = %d, want %d (kinds list corrupted by marshal)",
			len(rt.TextKinds), len(kinds))
	}
}

// ── Probe 5: ActiveKey composition is collapsed across text_kinds ───
//
// Same (asset_id, source_text_hash) repeated with different
// text_kinds MUST yield the same ActiveKey — the broker-side
// UNIQUE-constraint rescue collapses them to a single materialize
// job run. This invariant guarantees that callers don't need to
// pre-coordinate the kinds list when emitting post-publish
// enqueues.
func TestFanOut_EnqueueMaterializeOne_ActiveKeyCollapsedAcrossKinds(t *testing.T) {
	stub := &stubEnqueuer{}
	f := NewMaterializeFanOut(stub, nil)

	const (
		assetID = "asset-collapse"
		hashVal = "hash-collision"
	)
	if err := f.EnqueueMaterializeOne(
		context.Background(), assetID, "en", hashVal,
		[]detail.TextTrackKind{detail.TextTrackTranscript},
	); err != nil {
		t.Fatalf("first enqueue error: %v", err)
	}
	if err := f.EnqueueMaterializeOne(
		context.Background(), assetID, "en", hashVal,
		[]detail.TextTrackKind{detail.TextTrackDescription, detail.TextTrackSummary},
	); err != nil {
		t.Fatalf("second enqueue error: %v", err)
	}

	if stub.calls != 2 {
		t.Fatalf("expected 2 Enqueue calls, got %d", stub.calls)
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if len(stub.recorded) != 2 {
		t.Fatalf("expected 2 recorded enqueues, got %d", len(stub.recorded))
	}
	wantPrefix := "asset.text.materialize:" + assetID + ":" + hashVal
	if stub.recorded[0].ActiveKey != stub.recorded[1].ActiveKey {
		t.Fatalf("ActiveKey must be collapsed: %q vs %q",
			stub.recorded[0].ActiveKey, stub.recorded[1].ActiveKey)
	}
	// Literal-shape pin: a future contributor renaming the
	// ActiveKey prefix would otherwise pass the equality-only
	// assertion above. Surface as a precise failure.
	if stub.recorded[0].ActiveKey != wantPrefix {
		t.Fatalf("ActiveKey[0] = %q, want literal shape %q (ActiveKey shape drift)",
			stub.recorded[0].ActiveKey, wantPrefix)
	}
}

// ── helpers ────────────────────────────────────────────────────────

func safeIndex(s []string, i int) string {
	if i < 0 || i >= len(s) {
		return "<out-of-range>"
	}
	return s[i]
}
