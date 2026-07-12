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
package texttracks_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
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
type enqueueJob struct {
	Type      string
	Payload   []byte
	ActiveKey string
}

func (s *stubEnqueuer) Enqueue(_ context.Context, req *job.EnqueueRequest) (*job.Job, error) {
	atomic.AddInt32(&s.calls, 1)
	s.mu.Lock()
	defer s.mu.Unlock()
	// *job.EnqueueRequest.Payload is `any` (the canonical union of
	// map[string]any, raw bytes, or typed structs). The fanout
	// sends raw []byte; assert HARD-FAIL (not silent nil) so a
	// future contributor changing the fanout's payload contract
	// (e.g. to map[string]any) trips this test immediately rather
	// than passing with malformed recorded state.
	//
	// godlike/07 fail-closed: the stub does NOT take *testing.T
	// (the MaterializeEnqueuer surface is broker-typed, not
	// test-typed), so we panic on contract violation. A panic
	// during `go test` is a hard fail signal that the test runner
	// surfaces as a FAIL line — louder than t.Fatalf and bounded
	// to test-process scope, with zero surface-area impact on
	// the production broker.
	pb, ok := req.Payload.([]byte)
	if !ok {
		panic("stubEnqueuer.Enqueue: expected []byte Payload shape (fanout producer contract regression: producer must marshal a typed struct or raw bytes)")
	}
	payloadCopy := append([]byte(nil), pb...)
	s.recorded = append(s.recorded, enqueueJob{
		Type:      req.Type,
		Payload:   payloadCopy,
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
var _ texttracks.MaterializeEnqueuer = (*stubEnqueuer)(nil)

// ── Probe 1: happy path enqueues with correct type + active_key + payload ────

func TestFanOut_EnqueueMaterializeOne_Success(t *testing.T) {
	stub := &stubEnqueuer{}
	f := texttracks.NewMaterializeFanOut(stub, nil) // nil log → zap.NewNop() fallback

	const (
		assetID = "asset-yt-001"
		hashVal = "abc123def456"
		lang    = "en"
	)
	kinds := []asset.TextTrackKind{asset.TextTrackTranscript}

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

	// Payload JSON shape.
	var p texttracks.MaterializeJobPayload
	if err := json.Unmarshal(last.Payload, &p); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if p.AssetID != assetID {
		t.Fatalf("AssetID = %q, want %q", p.AssetID, assetID)
	}
	if p.SourceLanguage != lang {
		t.Fatalf("SourceLanguage = %q, want %q", p.SourceLanguage, lang)
	}
	if p.SourceTextHash != hashVal {
		t.Fatalf("SourceTextHash = %q, want %q", p.SourceTextHash, hashVal)
	}
	if len(p.TextKinds) != 1 || p.TextKinds[0] != string(asset.TextTrackTranscript) {
		t.Fatalf("TextKinds = %v, want [%q]", p.TextKinds, asset.TextTrackTranscript)
	}
	if p.TargetLanguages != nil && len(p.TargetLanguages) > 0 {
		// omitempty: nil target_languages serializes as []byte
		// absent — the runtime Enqueue call MUST NOT pre-populate
		// a target override from the fanout helper.
		t.Fatalf("TargetLanguages unexpected non-empty: %v", p.TargetLanguages)
	}
}

// ── Probe 2: empty args surface typed ErrInvalidMaterializeRequest ────

func TestFanOut_EnqueueMaterializeOne_InvalidArgs(t *testing.T) {
	cases := []struct {
		name      string
		assetID   string
		lang      string
		hash      string
		kinds     []asset.TextTrackKind
		wantField string
	}{
		{
			name:      "empty asset_id",
			assetID:   "",
			lang:      "en",
			hash:      "h",
			kinds:     []asset.TextTrackKind{asset.TextTrackTranscript},
			wantField: "asset_id",
		},
		{
			name:      "empty source_language",
			assetID:   "a",
			lang:      "",
			hash:      "h",
			kinds:     []asset.TextTrackKind{asset.TextTrackTranscript},
			wantField: "source_language",
		},
		{
			name:      "empty source_text_hash",
			assetID:   "a",
			lang:      "en",
			hash:      "",
			kinds:     []asset.TextTrackKind{asset.TextTrackTranscript},
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
			f := texttracks.NewMaterializeFanOut(stub, nil)

			err := f.EnqueueMaterializeOne(
				context.Background(), tc.assetID, tc.lang, tc.hash, tc.kinds,
			)
			if err == nil {
				t.Fatalf("expected ErrInvalidMaterializeRequest, got nil")
			}
			var typed *texttracks.ErrInvalidMaterializeRequest
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
	f := texttracks.NewMaterializeFanOut(stub, nil)

	err := f.EnqueueMaterializeOne(
		context.Background(), "asset-x", "en", "hash",
		[]asset.TextTrackKind{asset.TextTrackTranscript},
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
	f := texttracks.NewMaterializeFanOut(stub, nil)

	kinds := []asset.TextTrackKind{
		asset.TextTrackTranscript,
		asset.TextTrackDescription,
		asset.TextTrackSummary,
	}
	if err := f.EnqueueMaterializeOne(
		context.Background(), "asset-multi", "en", "h", kinds,
	); err != nil {
		t.Fatalf("EnqueueMaterializeOne returned error: %v", err)
	}
	last := stub.last()
	var p texttracks.MaterializeJobPayload
	if err := json.Unmarshal(last.Payload, &p); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if len(p.TextKinds) != len(kinds) {
		t.Fatalf("TextKinds len = %d, want %d", len(p.TextKinds), len(kinds))
	}
	for i, k := range kinds {
		if i >= len(p.TextKinds) || p.TextKinds[i] != string(k) {
			t.Fatalf("TextKinds[%d] = %q, want %q", i,
				safeIndex(p.TextKinds, i), string(k))
		}
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
	f := texttracks.NewMaterializeFanOut(stub, nil)

	const (
		assetID = "asset-collapse"
		hashVal = "hash-collision"
	)
	if err := f.EnqueueMaterializeOne(
		context.Background(), assetID, "en", hashVal,
		[]asset.TextTrackKind{asset.TextTrackTranscript},
	); err != nil {
		t.Fatalf("first enqueue error: %v", err)
	}
	if err := f.EnqueueMaterializeOne(
		context.Background(), assetID, "en", hashVal,
		[]asset.TextTrackKind{asset.TextTrackDescription, asset.TextTrackSummary},
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
