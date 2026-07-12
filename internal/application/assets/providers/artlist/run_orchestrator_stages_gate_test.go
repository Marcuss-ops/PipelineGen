package artlist

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// gateBlockFakeStager_SUTStageSource returns err from StageSource and
// records the call so the test can pin the gate-block short-circuit
// timing invariant: the resolver's gate fires BEFORE the
// mediaProcessor.Process path is reached.
type gateBlockFakeStager_SUTStageSource struct {
	err           error
	stageCalls    atomic.Int32
	cleanupCalls  atomic.Int32
}

var _ assets.SourceStager = (*gateBlockFakeStager_SUTStageSource)(nil)

func (f *gateBlockFakeStager_SUTStageSource) StageSource(_ context.Context, _ assets.SourceRef) (*assets.StagedAsset, error) {
	f.stageCalls.Add(1)
	return nil, f.err
}

func (f *gateBlockFakeStager_SUTStageSource) Cleanup(_ context.Context, _ *assets.StagedAsset) error {
	f.cleanupCalls.Add(1)
	return nil
}

// panickingMediaProcessor_MustNotBeCalled returns a typed sentinel
// error if Process is called. The integration test asserts Process was
// NEVER called; if the gate-block short-circuit regresses
// (e.g. orchestrator forgets to `return` after short-circuit), Process
// gets called and the typed sentinel surfaces in the test output.
type panickingMediaProcessor_MustNotBeCalled struct{}

var _ asset.Processor = (*panickingMediaProcessor_MustNotBeCalled)(nil)

var errProcessorShouldNotBeCalled = errors.New(
	"panickingMediaProcessor_MustNotBeCalled: Process was invoked — the gate-block short-circuit did NOT short-circuit (a godlike/07 regression). The orchestrator MUST return early after gateBlockShortCircuit fires so mediaProcessor.Process is NEVER invoked on a typed gate block",
)

func (p *panickingMediaProcessor_MustNotBeCalled) Process(_ context.Context, _ *asset.ProcessInput) (*asset.ProcessResult, error) {
	return nil, errProcessorShouldNotBeCalled
}

// TestStageProcessBatch_GateBlockShortCircuit_AcquisitionMode is the
// canonical integration test for Fase 6 / Commit 1. It exercises the
// orchestrator's stageProcessBatch end-to-end on the gate-block
// short-circuit path:
//
//  1. Stager returns ErrAcquisitionModeBlocked from StageSource.
//  2. The short-circuit fires (via gateBlockShortCircuit call site).
//  3. item.Status="blocked_mode", item.Error carries sentinel text.
//  4. resp.Failed++ by exactly 1.
//  5. resp.Items receives the blocked item (with the canonical Status).
//  6. mediaProcessor.Process is NEVER called (the panickingMediaProcessor
//     returns a typed sentinel if invoked).
//  7. asset_processing lifecycle is updated via Fail(stage="download",
//     msg="<status>: <err>").
//
// godlike/06 SSOT: this test is the canonical regression guard for the
// COMMIT 1 wire-level contract. Subsequent Commits 2/3/4 ADD identical
// sub-tests for the next 3 sentinels (in lockstep with the classifier
// extension). DO NOT delete this test when extending the classifier —
// extend it.
func TestStageProcessBatch_GateBlockShortCircuit_AcquisitionMode(t *testing.T) {
	ctx := context.Background()

	fakeStager := &gateBlockFakeStager_SUTStageSource{
		err: ErrAcquisitionModeBlocked,
	}
	proc := &panickingMediaProcessor_MustNotBeCalled{}

	// Minimal Service with only the fields stageProcessBatch touches.
	// mediaProcessor + stager are wired; assetProcessing is nil
	// (optional per the runRespGateBlockCounter nil-safe contract).
	svc := &Service{
		stager:          fakeStager,
		mediaProcessor:  proc,
		assetProcessing: nil,
		log:             zap.NewNop(),
	}
	orchestrator := &RunOrchestratorService{svc: svc}

	resp := &RunTagResponse{
		OK:        true,
		Term:      "test",
		Found:     1,
		Items:     []RunTagItem{},
	}
	workItem := clipWork{
		item: RunTagItem{
			ClipID: "clip-mode-block",
			Name:   "stub",
			Status: "",
		},
		processInput: &asset.ProcessInput{
			ID:        "clip-mode-block",
			Name:      "stub",
			SourceURL: "https://artlist.io/clip/123",
		},
	}
	ps := &pipelineState{
		resp:        resp,
		workItems:   []clipWork{workItem},
		concurrency: 1,
	}

	err := orchestrator.stageProcessBatch(ctx, ps)

	// The orchestrator MUST NOT return an error — the gate-block
	// short-circuit is the EXPECTED happy path of this test
	// (godlike/07 fail-closed: the block IS the success state).
	require.NoError(t, err,
		"stageProcessBatch MUST NOT return an error when the gate-block short-circuit fires (gate-block is the expected happy path; godlike/07 fail-closed)")

	// 1. Stager was called exactly once.
	assert.Equal(t, int32(1), fakeStager.stageCalls.Load(),
		"the stager MUST be invoked exactly once per pipeline item")

	// 2. mediaProcessor.Process was NOT called (short-circuit fired).
	assert.NotEqual(t, errProcessorShouldNotBeCalled,
		errors.Unwrap(err),
		"mediaProcessor.Process MUST NOT be invoked on a typed gate-block (a godlike/07 fake-availability regression)")

	// 3. resp.Failed bumped exactly once.
	assert.Equal(t, 1, ps.resp.Failed,
		"resp.Failed MUST be bumped exactly once by gateBlockShortCircuit (the canonical tally operators read on /api/artlist/runs/:id)")

	// 4. resp.Items contains exactly the blocked item.
	require.Len(t, ps.resp.Items, 1,
		"resp.Items MUST contain exactly the blocked item (no transport-layer append)")

	// 5. The blocked item's Status is the canonical "blocked_mode".
	blocked := ps.resp.Items[0]
	assert.Equal(t, "blocked_mode", blocked.Status,
		"RunTagItem.Status MUST be the canonical \"blocked_mode\" wire string (per-item audit grep-ability)")
	assert.Equal(t, workItem.item.ClipID, blocked.ClipID,
		"the appended item MUST carry the original ClipID")

	// 6. The blocked item's Error carries the typed-sentinel text.
	assert.Contains(t, blocked.Error, "manual_import",
		"RunTagItem.Error MUST carry the typed-sentinel text verbatim (errors.Is walks the wrap chain)")

	// 7. cleaned-up via SourceStager short-circuit (Cleanup is
	// ONLY invoked on the happy-path (staged != nil); typed gate-
	// block short-circuit returns BEFORE Cleanup, which is correct
	// because typed gate-block does NOT create a staged file).
	assert.Equal(t, int32(0), fakeStager.cleanupCalls.Load(),
		"Cleanup MUST NOT be invoked on a typed gate-block (short-circuit returns BEFORE the staged-asset cleanup defer)")
}

// TestStageProcessBatch_GateBlockShortCircuit_UnrelatedErrorContinue
// pins the godlike/07 fail-closed negative: an error that is NOT a
// typed gate-block MUST continue to mediaProcessor.Process (the
// historic behaviour for transport-layer / I/O failures). This guards
// against a regression where classifyGateBlock starts matching ALL
// errors as gate-blocks (a false-positive bug that would silently
// strip every transport failure from the per-item audit).
func TestStageProcessBatch_GateBlockShortCircuit_UnrelatedErrorContinue(t *testing.T) {
	ctx := context.Background()

	unrelated := errors.New("transport-layer timeout: dial tcp: i/o timeout")
	fakeStager := &gateBlockFakeStager_SUTStageSource{
		err: unrelated,
	}
	proc := &panickingMediaProcessor_MustNotBeCalled{}

	svc := &Service{
		stager:          fakeStager,
		mediaProcessor:  proc,
		assetProcessing: nil,
		log:             zap.NewNop(),
	}
	orchestrator := &RunOrchestratorService{svc: svc}

	resp := &RunTagResponse{
		OK:        true,
		Term:      "test",
		Found:     1,
		Items:     []RunTagItem{},
	}
	workItem := clipWork{
		item: RunTagItem{
			ClipID: "clip-unrelated",
			Name:   "stub",
		},
		processInput: &asset.ProcessInput{
			ID:        "clip-unrelated",
			Name:      "stub",
			SourceURL: "https://artlist.io/clip/456",
		},
	}
	ps := &pipelineState{
		resp:        resp,
		workItems:   []clipWork{workItem},
		concurrency: 1,
	}

	err := orchestrator.stageProcessBatch(ctx, ps)

	// The unrelated error MUST propagate up via mediaProcessor's
	// typed sentinel — proving the short-circuit did NOT intercept
	// it (godlike/07 negative: classifyGateBlock must be narrow).
	require.Error(t, err, "the unrelated transport-layer error MUST propagate through mediaProcessor.Process")
	require.ErrorIs(t, err, errProcessorShouldNotBeCalled,
		"unrelated errors MUST NOT short-circuit (the orchestrator MUST fall through to mediaProcessor.Process for non-gate-block errors; godlike/07 fail-closed negative test)")

	assert.Equal(t, 0, ps.resp.Failed,
		"resp.Failed MUST NOT be bumped for unrelated (non-gate-block) errors")
	assert.Empty(t, ps.resp.Items,
		"resp.Items MUST be empty when mediaProcessor.Process propagates an error (the failure is recorded at the procErr branch below the short-circuit)")
}
