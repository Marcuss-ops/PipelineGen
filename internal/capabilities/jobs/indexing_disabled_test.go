// Package outbox_test — indexing_disabled_test.go (PR-QDRANT-INDEXCLIP-GUARD,
// July 2026)
//
// 4 TDD tests for the IndexingHandler's typed-sentinel + retry-pending
// path. When clipindexer.Service.IndexClip returns
// ErrIndexClipDisabledButEventRequested (cfg.Enabled=false but an
// asset.index.requested event arrived anyway):
//
//   - The handler MUST NOT mark the event as success (godlike/07
//     fail-closed — would silently absorb "indexer offline" inside
//     "indexer succeeded", the EXACT anti-pattern the user spec
//     forbids).
//   - The handler MUST stamp INDEXING_SKIPPED_NO_INDEXER on
//     media_assets.index_state via the IndexerStateUpdater port so
//     the canonical transient retry-pending state is observable on
//     dashboards.
//   - The handler MUST return a non-terminal, retryable error so the
//     outbox pool's IsTerminal classifier leaves the row in pending
//     and re-emits when the indexer is re-enabled (the pending+retry
//     contract per godlike/07 fail-closed).
//
// Test 5 (state_machine_extension) lives in
// internal/kernel/asset/index_state_skipped_test.go per godlike/06
// SSOT (one canonical test file per package — IndexState enum +
// Valid /IsTerminal /IsRetryPending predicates are owned by the asset
// package, not by the outbox handler).
//
// ── Fakes ──────────────────────────────────────────────────────────────────────
//
// mockIndexerStateUpdater records MarkIndexingSkippedNoIndexer
// invocations so tests can assert the state-write side-effect fired
// ONLY on the sentinel branch (and skipped on the success branch).
//
// mockIndexerFlippable extends mockIndexClipper with a per-clipID
// "disabled" toggle so the retry-on-flapping test can simulate:
//
//	t=0: indexer disabled → IndexClip returns the typed sentinel.
//	t=1: indexer re-enabled → IndexClip returns nil (success).
package jobs

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"go.uber.org/zap"

	outboxhandlers "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// mockIndexerStateUpdater satisfies clipindexer.IndexerStateUpdater.
// Per-clipID invocation count + captured last-err let tests assert
// that the write fired the right number of times AND that the
// underlying setIndexState round-trip completed cleanly.
//
// godlike/06 SSOT: this mock DOES NOT wrap *clipindexer.Service or
// import the package's *Service — it implements the typed port
// directly so the test pins the contract surface without coupling
// to the production concrete.
type mockIndexerStateUpdater struct {
	skips map[string]int
	// errOnReturn (optional): when non-nil, every
	// MarkIndexingSkippedNoIndexer call returns this error WITHOUT
	// recording the call in skips. Used by future tests that
	// exercise the godlike/07 "state-update failure → retry
	// continues" path. Default nil = success.
	errOnReturn error
}

func newMockIndexerStateUpdater() *mockIndexerStateUpdater {
	return &mockIndexerStateUpdater{skips: make(map[string]int)}
}

func (m *mockIndexerStateUpdater) MarkIndexingSkippedNoIndexer(_ context.Context, assetID string) error {
	if m.errOnReturn != nil {
		return m.errOnReturn
	}
	m.skips[assetID]++
	return nil
}

// mockIndexerFlippable is the dispatcher-side fake for the
// retry-on-flapping test. "disabled" is per-clipID keyed so a future
// parallel-test variant cannot cross-pollute state.
//
// semantic: when disabled[clipID]==true → IndexClip returns the typed
// sentinel. When disabled[clipID]==false → IndexClip returns nil
// (defensive default mirrors production "indexer online, no work to
// do" semantics for idempotent replay).
type mockIndexerFlippable struct {
	disabled map[string]bool
}

func (m *mockIndexerFlippable) IndexClip(_ context.Context, clipID string) error {
	if m.disabled[clipID] {
		return clipindexer.ErrIndexClipDisabledButEventRequested
	}
	return nil
}

// ── Test 1: HappyPath ────────────────────────────────────────────────

// TestIndexingHandler_HappyPath_WithStateUpdater: when the indexer
// is online AND IndexClip succeeds, the handler returns nil
// (success), the IndexerStateUpdater is NOT invoked (no skip
// side-effect), and the IndexClipper is invoked exactly once.
//
// PR-QDRANT-INDEXCLIP-GUARD (July 2026): the WithStateUpdater
// fluent setter returns the receiver so production wire code is
// one chained call. IndexerStateUpdater-wired-but-not-used is the
// canonical happy-path — confirms that wiring the port does NOT
// affect the existing happy path semantics.
func TestIndexingHandler_HappyPath_WithStateUpdater(t *testing.T) {
	indexer := &mockIndexClipper{}
	src := &mockSourceVersionQuerier{getResult: "h-OK"}
	stateUpd := newMockIndexerStateUpdater()

	h := outboxhandlers.NewIndexingHandler(indexer, src, zap.NewNop()).
		WithStateUpdater(stateUpd)

	err := h.Handle(context.Background(),
		indexEvt(t, validIndexRequestPayload(t, "clip-happy", "h-OK", "idem-happy")),
	)
	if err != nil {
		t.Fatalf("happy path: expected nil; got: %v", err)
	}
	if indexer.invoked != 1 {
		t.Errorf("happy path: IndexClip must run exactly once; got %d", indexer.invoked)
	}
	if len(stateUpd.skips) != 0 {
		t.Errorf("happy path: MarkIndexingSkippedNoIndexer must NOT fire on success; got calls=%v", stateUpd.skips)
	}
}

// ── Test 2: SkipOnDisabled ───────────────────────────────────────────────

// TestIndexingHandler_SkipOnDisabled_RoutesToRetryPending: when the
// indexer is disabled at runtime AND an asset.index.requested event
// arrived anyway (clipindexer returns
// ErrIndexClipDisabledButEventRequested), the handler must:
//
//   - Stamp INDEXING_SKIPPED_NO_INDEXER via IndexerStateUpdater
//     (calls MarkIndexingSkippedNoIndexer exactly once with the
//     event's assetID).
//   - Return a NON-NIL, NON-TERMINAL, NON-SUPERSEDE err so the
//     outbox pool's IsTerminal/IsSupersede classifiers leave the
//     row pending for retry (matches the godlike/07 fail-closed
//     pending+retry contract — the outbox worker re-emits the
//     event when the indexer is re-enabled).
//   - errors.Is the returned err against the typed sentinel so
//     downstream observability tools can branch on the skip signal
//     without parsing the message string.
func TestIndexingHandler_SkipOnDisabled_RoutesToRetryPending(t *testing.T) {
	indexer := &mockIndexClipper{
		err: clipindexer.ErrIndexClipDisabledButEventRequested,
	}
	src := &mockSourceVersionQuerier{getResult: "h-SKIP"}
	stateUpd := newMockIndexerStateUpdater()

	h := outboxhandlers.NewIndexingHandler(indexer, src, zap.NewNop()).
		WithStateUpdater(stateUpd)

	err := h.Handle(context.Background(),
		indexEvt(t, validIndexRequestPayload(t, "clip-skip", "h-SKIP", "idem-skip")),
	)

	if err == nil {
		t.Fatal("skip-on-disabled: handler MUST return non-nil err (godlike/07 fail-closed); got nil")
	}
	if outboxevents.IsTerminal(err) {
		t.Fatalf("skip-on-disabled: err MUST NOT be terminal (terminal = MarkCompleted, defeats retry); got: %v", err)
	}
	if outboxevents.IsSupersede(err) {
		t.Fatalf("skip-on-disabled: err MUST NOT be classified as supersede (supersede = MarkSuperseded, distinct path); got: %v", err)
	}
	if !errors.Is(err, clipindexer.ErrIndexClipDisabledButEventRequested) {
		t.Errorf("skip-on-disabled: errors.Is probe must hit the typed sentinel; err=%v", err)
	}
	// State-update side-effect MUST have fired with the event's assetID.
	if got := stateUpd.skips["clip-skip"]; got != 1 {
		t.Errorf("skip-on-disabled: MarkIndexingSkippedNoIndexer must fire exactly once with assetID=\"clip-skip\"; got count=%d (all calls=%v)", got, stateUpd.skips)
	}
	// IndexClip dispatched exactly once (the sentinel-returning call).
	if indexer.invoked != 1 {
		t.Errorf("skip-on-disabled: IndexClip must fire exactly once (the disabled-bool read); got %d", indexer.invoked)
	}
}

// ── Test 3: RetryOnFlapping ────────────────────────────────────────────

// TestIndexingHandler_RetryOnFlapping_RecoversOnReEnable: simulates
// the operator-workflow scenario where:
//
//	t=0: indexer disabled (cfg.Enabled flipped to false mid-cycle).
//	t=1: operator re-enables (cfg.Enabled flipped back to true).
//
// First handler.Handle invocation lands on the disabled branch →
// returns retryable err + stamps INDEXING_SKIPPED_NO_INDEXER.
// Second handler.Handle invocation lands on the enabled branch →
// returns nil (success) + state-updater NOT called again.
//
// The state-update DOES NOT auto-clear on retry; the asset row stays
// in INDEXING_SKIPPED_NO_INDEXER until the next writer (e.g.
// tryFastPath) transitions it to the canonical EMBEDDING/INDEXED
// chain. godlike/06 SSOT: a single state-write per skip event; the
// retry-success path takes ownership of the next transition.
//
// The flippable mock's `disabled[clipID]` toggle is initially true
// (sentinel mode) then flipped to false (success mode) inside the
// test BEFORE the second Handle call — no time.Sleep, no async.
func TestIndexingHandler_RetryOnFlapping_RecoversOnReEnable(t *testing.T) {
	clipID := "clip-flap"
	flippable := &mockIndexerFlippable{
		disabled: map[string]bool{clipID: true},
	}
	src := &mockSourceVersionQuerier{getResult: "h-FLAP"}
	stateUpd := newMockIndexerStateUpdater()

	h := outboxhandlers.NewIndexingHandler(flippable, src, zap.NewNop()).
		WithStateUpdater(stateUpd)

	// t=0: disabled → sentinel branch.
	err := h.Handle(context.Background(),
		indexEvt(t, validIndexRequestPayload(t, clipID, "h-FLAP", "idem-flap")),
	)
	if err == nil {
		t.Fatal("flap@disabled: handler MUST return non-nil err; got nil")
	}
	if !errors.Is(err, clipindexer.ErrIndexClipDisabledButEventRequested) {
		t.Errorf("flap@disabled: errors.Is probe must hit the sentinel; err=%v", err)
	}
	if got := stateUpd.skips[clipID]; got != 1 {
		t.Errorf("flap@disabled: state-updater must fire exactly once; got %d", got)
	}

	// Operator re-enables the indexer between retry slots.
	flippable.disabled[clipID] = false

	// t=1: enabled → success branch (different idempotency_key so
	// the outbox schema-version+source_version surfaces differ
	// from the first delivery — production's source_version
	// supersede gate is bypassed by the same source_version here
	// so the handler route is the success path, not the supersede
	// path).
	err = h.Handle(context.Background(),
		indexEvt(t, validIndexRequestPayload(t, clipID, "h-FLAP", "idem-flap-2")),
	)
	if err != nil {
		t.Fatalf("flap@re-enabled: handler MUST return nil (success); got: %v", err)
	}
	// State-updater MUST NOT re-fire on the success path (godlike/07
	// minimal-blast-radius: only the sentinel branch writes the
	// skip state — replay-success is handled by IndexClip's
	// canonical chain, not by the retry-pending state machine).
	if got := stateUpd.skips[clipID]; got != 1 {
		t.Errorf("flap@re-enabled: state-updater must NOT re-fire on success (still 1, not 2); got %d", got)
	}
}

// ── Test 4: SentinelDistinct ───────────────────────────────────────────

// TestErrIndexClipDisabledButEventRequested_DistinctFromSupersede pins
// the godlike/07 typed-error contract invariant:
//
//   - ErrIndexClipDisabledButEventRequested and ErrIndexSuperseded
//     are SEPARATE sentinels representing orthogonal failure modes.
//
//   - Either:
//     (a) errors.Is(newErr, ErrIndexDisabled) is FALSE when newErr
//     is ErrIndexSuperseded (or vice versa);
//     (b) errors.Is(err, X) succeeds only when the chain contains
//     the SAME sentinel (no false positives across the typed
//     universe);
//     (c) A wrapped sentinel (fmt.Errorf "...: %w", sentinel)
//     remains errors.Is-probeable.
//
//   - This prevents a future refactor from accidentally merging the
//     skipped-no-indexer signal with the CAS-supersede signal —
//     distinct routing rules depend on the typed distinction
//     (supersede routes to MarkSuperseded, skip routes to retry).
func TestErrIndexClipDisabledButEventRequested_DistinctFromSupersede(t *testing.T) {
	superseded := &clipindexer.ErrIndexSuperseded{ClipID: "x", SourceVersion: "v"}
	// (Note: ErrIndexSuperseded has pointer receiver Error, so
	// the typed carrier MUST be a pointer to satisfy the
	// error interface — a value `clipindexer.ErrIndexSuperseded{}`
	// does NOT implement error and cannot satisfy errors.Is
	// probes.)

	// Distinctness (a): direct probe of either sentinel against the
	// OTHER typed-carrier must report false. The two sentinels are
	// errors.New(...) — neither wraps the other.
	if errors.Is(superseded, clipindexer.ErrIndexClipDisabledButEventRequested) {
		t.Errorf("distinctness: errors.Is(superseded, sentinel) MUST be false; sentinel of skip should not match supersede carrier")
	}
	if errors.Is(clipindexer.ErrIndexClipDisabledButEventRequested, superseded) {
		t.Errorf("distinctness: errors.Is(sentinel, superseded) MUST be false; sentinel of skip should not match the supersede carrier")
	}

	// Distinctness (b): errors.Is(err, X) succeeds only when the
	// chain CONTAINS X — no cross-pollination between the two
	// sentinels.
	if errors.Is(&clipindexer.ErrIndexSuperseded{}, clipindexer.ErrIndexClipDisabledButEventRequested) {
		t.Errorf("distinctness: zero-value supersede must not match the skip sentinel")
	}

	// Wrapped-chain (c): production wire surfaces wrap the sentinel
	// via `fmt.Errorf("...: %w", sentinel)` per the godlike/07
	// typed-error contract. errors.Is traverses Go's standard
	// Unwrap() chain. 1-layer + 2-layer wraps MUST remain
	// errors.Is-probeable.
	wrapped1 := fmt.Errorf("1-layer: %w", clipindexer.ErrIndexClipDisabledButEventRequested)
	if !errors.Is(wrapped1, clipindexer.ErrIndexClipDisabledButEventRequested) {
		t.Errorf("wrapped-chain: 1-layer wrap must preserve errors.Is probe; wrapped1=%v", wrapped1)
	}
	if errors.Is(wrapped1, superseded) {
		t.Errorf("wrapped-chain: 1-layer wrap must NOT match an unrelated sentinel; wrapped1=%v", wrapped1)
	}
	wrapped2 := fmt.Errorf("2-layer-outer: %w",
		fmt.Errorf("2-layer-inner: %w", clipindexer.ErrIndexClipDisabledButEventRequested))
	if !errors.Is(wrapped2, clipindexer.ErrIndexClipDisabledButEventRequested) {
		t.Errorf("wrapped-chain: 2-layer wrap must preserve errors.Is probe; wrapped2=%v", wrapped2)
	}
}
