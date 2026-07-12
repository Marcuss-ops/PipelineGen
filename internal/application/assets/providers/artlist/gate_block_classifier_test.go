package artlist

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// fakeGateBlockCounter is an in-memory test double for gateBlockCounter.
// It records the number of bump calls so the regression test can pin
// the counter-bump invariant verbatim.
type fakeGateBlockCounter struct {
	bumpCount int
}

func (f *fakeGateBlockCounter) bumpGateBlock() { f.bumpCount++ }

// fakeAssetProcessingFail records Fail calls. Used when the regression
// test verifies the gateBlockShortCircuit → asset_processing.Fail path.
type fakeAssetProcessingFail struct {
	calls []struct{ stage, msg string }
}

func (f *fakeAssetProcessingFail) fail(stage, msg string) error {
	f.calls = append(f.calls, struct{ stage, msg string }{stage, msg})
	return nil
}

// TestGateBlockClassify_AcquisitionModeBlocked pins the Fase 6 / Commit 1
// mapping: ErrAcquisitionModeBlocked → gateBlockMode → "blocked_mode".
//
// godlike/06 SSOT: this test is the canonical regression guard for the
// (error, gate_block_status, run_tag_status) triple. Future commits
// (2/3/4) add new sub-tests to extend the mapping.
func TestGateBlockClassify_AcquisitionModeBlocked(t *testing.T) {
	gb := classifyGateBlock(ErrAcquisitionModeBlocked)
	assert.Equal(t, gateBlockMode, gb, "ErrAcquisitionModeBlocked MUST classify as gateBlockMode")
	assert.Equal(t, "blocked_mode", gb.asRunTagStatus(), "gateBlockMode MUST map to canonical RunTagItem.Status = \"blocked_mode\"")
}

// TestGateBlockClassify_NilReturnsNone pins the godlike/07 fail-closed
// invariant: nil errors classify as gateBlockNone so the short-circuit
// is a no-op for the happy path.
func TestGateBlockClassify_NilReturnsNone(t *testing.T) {
	assert.Equal(t, gateBlockNone, classifyGateBlock(nil))
	assert.Equal(t, "", gateBlockNone.asRunTagStatus(), "gateBlockNone MUST map to empty RunTagItem.Status (no short-circuit)")
	assert.Equal(t, "", classifyGateBlock(errors.New("unrelated error")).asRunTagStatus(),
		"unknown errors MUST classify as gateBlockNone (no short-circuit, no false-block)")
}

// TestGateBlockShortCircuit_StampsAuditBumpsCounter calls for the
// canonical Commit 1 path: ErrAcquisitionModeBlocked goes through the
// helper, item.Status="blocked_mode", item.Error carries the typed-error
// text verbatim, the counter is bumped by 1, the failFn is called
// exactly once with stage="download".
func TestGateBlockShortCircuit_StampsAuditBumpsCounter(t *testing.T) {
	item := &RunTagItem{ClipID: "clip-abc"}
	wrappedErr := fmt.Errorf("artlist pipeline: download step: %w", ErrAcquisitionModeBlocked)
	counter := &fakeGateBlockCounter{}
	ap := &fakeAssetProcessingFail{}

	status := gateBlockShortCircuit(
		item, wrappedErr, counter, zap.NewNop(), ap.fail,
	)

	require.Equal(t, "blocked_mode", status, "the short-circuit MUST return the canonical RunTagItem.Status string")
	assert.Equal(t, "blocked_mode", item.Status, "item.Status MUST be stamped with \"blocked_mode\"")
	assert.Contains(t, item.Error, "manual_import", "item.Error MUST carry the typed-sentinel text verbatim (errors.Is walks the wrap chain)")
	assert.Equal(t, 1, counter.bumpCount, "the gate-block tally MUST be bumped exactly once")
	require.Len(t, ap.calls, 1, "asset_processing.Fail MUST be called exactly once")
	assert.Equal(t, "download", ap.calls[0].stage, "fail(stage) MUST be \"download\" (the gate-block short-circuit stage)")
	assert.Contains(t, ap.calls[0].msg, "blocked_mode", "fail(msg) MUST contain the canonical status tag for grep-ability")
}

// TestGateBlockShortCircuit_NilItemLeavesUntouched pins the godlike/07
// fail-closed invariant: a nil item MUST NOT panic. The helper is
// defensive against accidental nil-pointer call paths from the
// production orchestrator (e.g. a future refactor that passes nil
// instead of an item reference).
func TestGateBlockShortCircuit_NilItemLeavesUntouched(t *testing.T) {
	counter := &fakeGateBlockCounter{}
	status := gateBlockShortCircuit(nil, ErrAcquisitionModeBlocked, counter, zap.NewNop(), nil)
	assert.Equal(t, "", status, "nil item MUST return empty status (no short-circuit)")
	assert.Equal(t, 0, counter.bumpCount, "nil item MUST NOT bump counter (no audit without an item)")
}

// TestGateBlockShortCircuit_UnrelatedErrorNoOp pins the cross-cutting
// invariant: errors that are NOT a gate-block sentinel MUST return
// empty status (no short-circuit). The orchestrator continues with
// mediaProcessor.Process in this case — that's the historic behaviour
// for transport-level failures.
func TestGateBlockShortCircuit_UnrelatedErrorNoOp(t *testing.T) {
	item := &RunTagItem{ClipID: "clip-xyz"}
	counter := &fakeGateBlockCounter{}
	ap := &fakeAssetProcessingFail{}
	unrelated := errors.New("transport-layer timeout")
	status := gateBlockShortCircuit(
		item, unrelated, counter, zap.NewNop(), ap.fail,
	)
	assert.Equal(t, "", status, "unrelated errors MUST NOT short-circuit")
	assert.Empty(t, item.Status, "unrelated errors MUST NOT stamp item.Status")
	assert.Empty(t, item.Error, "unrelated errors MUST NOT stamp item.Error")
	assert.Equal(t, 0, counter.bumpCount, "unrelated errors MUST NOT bump the gate-block tally")
	assert.Empty(t, ap.calls, "unrelated errors MUST NOT call asset_processing.Fail via this helper")
}

// TestGateBlockClassify_DailyLimitExhaustedReserved pins the reservation
// for Commit 2. The test passes today (no ErrDailyLimitExhausted exists)
// AND surfaces a build/lint error if a future refactor accidentally
// hard-codes the sentinel to a non-gate-block path. Future Commit 2
// adds the actual mapping case and removes this reservation test.
//
// godlike/06 SSOT lockstep: every typed gate-block sentinel MUST have
// (a) a sentinel declaration in ports.go, (b) a gateBlockStatus const,
// (c) a classifyGateBlock case, (d) an asRunTagStatus case, AND (e) a
// regression test. Commit 1 delivers (a)+(b)+(c)+(d)+(e) for
// ErrAcquisitionModeBlocked; Commit 2 does the same for ErrDailyLimitExhausted.
func TestGateBlockClassify_DailyLimitExhaustedReserved(t *testing.T) {
	// Reservation: when Commit 2 lands, this test should be REPLACED
	// by the actuals:
	//   err := ErrDailyLimitExhausted
	//   assert.Equal(t, gateBlockDailyLimit, classifyGateBlock(err))
	//   assert.Equal(t, "blocked_daily_limit", gateBlockDailyLimit.asRunTagStatus())
	// For now, the test asserts: NO current sentinel classifies as
	// gateBlockDailyLimit (none exists yet) — keeps the build green
	// while reserving the slot.
	assert.NotEqual(t, gateBlockDailyLimit, classifyGateBlock(ErrAcquisitionModeBlocked),
		"reservation: gateBlockDailyLimit slot is reserved for Commit 2 ErrDailyLimitExhausted; must not collide with Commit 1 sentinel")
}

// TestGateBlockClassify_UnauthorizedReserved mirrors the reservation
// pattern for Commit 3 (ErrAccountUnauthorized → gateBlockUnauthorized).
func TestGateBlockClassify_UnauthorizedReserved(t *testing.T) {
	assert.NotEqual(t, gateBlockUnauthorized, classifyGateBlock(ErrAcquisitionModeBlocked),
		"reservation: gateBlockUnauthorized slot is reserved for Commit 3 ErrAccountUnauthorized; must not collide with Commit 1 sentinel")
}

// TestGateBlockClassify_SessionExpiredReserved mirrors the reservation
// pattern for Commit 4 (ErrSessionExpired → gateBlockSessionExpired).
func TestGateBlockClassify_SessionExpiredReserved(t *testing.T) {
	assert.NotEqual(t, gateBlockSessionExpired, classifyGateBlock(ErrAcquisitionModeBlocked),
		"reservation: gateBlockSessionExpired slot is reserved for Commit 4 ErrSessionExpired; must not collide with Commit 1 sentinel")
}

// TestRunRespGateBlockCounter_Bump pins the counter adapter: it MUST
// mutate resp.Failed by +1 per bump call. Used by the orchestrator's
// stageProcessBatch gate-block short-circuit; the regression test
// catches the regression where a future refactor breaks the wiring
// (e.g. forgets the counter, points to the wrong field).
func TestRunRespGateBlockCounter_Bump(t *testing.T) {
	resp := &RunTagResponse{Term: "test", Found: 5, OK: true}
	counter := newGateBlockCounterFor(resp)
	require.NotNil(t, counter)
	counter.bumpGateBlock()
	assert.Equal(t, 1, resp.Failed, "newGateBlockCounterFor(resp).bumpGateBlock() MUST bump resp.Failed by 1")
	counter.bumpGateBlock()
	assert.Equal(t, 2, resp.Failed, "repeated bumps MUST accumulate (no de-dup)")
}

// TestNewGateBlockCounterFor_NilResp pins the godlike/07 fail-closed
// invariant: nil resp MUST return nil counter (no panic, no audit).
func TestNewGateBlockCounterFor_NilResp(t *testing.T) {
	assert.Nil(t, newGateBlockCounterFor(nil),
		"newGateBlockCounterFor(nil) MUST return nil (callers short-circuit on nil)")
}
