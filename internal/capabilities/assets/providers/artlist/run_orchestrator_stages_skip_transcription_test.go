package assets

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ── Test doubles for the skip-transcription gate ──────────────────────────

// countingTranscriber is a Transcriber double that increments a counter
// every time Transcribe is invoked. Tests assert the counter is EXACTLY
// 1 (mandatory) or EXACTLY 0 (skip) — Equal, not GreaterOrEqual, so a
// regression that fires twice breaks the test immediately.
type countingTranscriber struct {
	calls int32
}

func (c *countingTranscriber) Transcribe(_ context.Context, _ string) (string, string, error) {
	atomic.AddInt32(&c.calls, 1)
	return "counting-transcript", "en", nil
}

// countingTextTrackRepo wraps the package-level stubTextTrackRepo with a
// UpsertBatch counter. Same Equal-1 / Equal-0 contract as the transcriber.
type countingTextTrackRepo struct {
	stubTextTrackRepo
	calls int32
}

func (c *countingTextTrackRepo) UpsertBatch(_ context.Context, _ []asset.TextTrack) error {
	atomic.AddInt32(&c.calls, 1)
	return nil
}

// newSkipTranscriptionTestOrchestrator builds an orchestrator whose
// stageProcessBatch exercises the transcription gate.
func newSkipTranscriptionTestOrchestrator(skip bool) (*RunOrchestratorService, *countingTranscriber, *countingTextTrackRepo) {
	tr := &countingTranscriber{}
	repo := &countingTextTrackRepo{}
	svc := &Service{
		cfg: &config.Config{
			External: config.ExternalConfig{ArtlistSkipTranscription: skip},
		},
		mediaProcessor: &succeedMediaProcessor{},
		transcriber:    tr,
		textTrackRepo:  repo,
		log:            zap.NewNop(),
	}
	return &RunOrchestratorService{svc: svc}, tr, repo
}

// ── skip_transcription gate tests ─────────────────────────────────────────

// TestStageProcessBatch_SkipTranscriptionTrue_BypassesTranscriber pins
// the PR-ARTLIST-SKIP-TRANSCRIPTION-OPT-IN contract: flag=true means
// transcriber counter=0, repo counter=0, item.Status="processed".
func TestStageProcessBatch_SkipTranscriptionTrue_BypassesTranscriber(t *testing.T) {
	ctx := context.Background()
	orch, tr, repo := newSkipTranscriptionTestOrchestrator(true)
	ps := newTranscriptionPipelineState()

	err := orch.stageProcessBatch(ctx, ps)

	require.NoError(t, err,
		"stageProcessBatch must return nil and treat the clip as processed when skip flag is on")
	assert.Equal(t, int32(0), atomic.LoadInt32(&tr.calls),
		"transcriber.Transcribe MUST NOT be called when ArtlistSkipTranscription=true (operator escape hatch)")
	assert.Equal(t, int32(0), atomic.LoadInt32(&repo.calls),
		"textTrackRepo.UpsertBatch MUST NOT be called when ArtlistSkipTranscription=true (no transcript to persist)")
	require.Len(t, ps.resp.Items, 1,
		"resp.Items MUST still contain the processed clip (no-transcript variant)")
	item := ps.resp.Items[0]
	assert.Equal(t, "processed", item.Status,
		"item.Status MUST be 'processed' (NOT 'transcription_failed' / 'transcript_persist_failed') when skip flag is on")
	assert.Equal(t, "", item.Error,
		"item.Error MUST be empty when skip flag is on (no transcription failure occurred)")
	assert.Equal(t, 0, ps.resp.Failed,
		"resp.Failed MUST stay at zero — the clip is processed, not failed")
}

// TestStageProcessBatch_SkipTranscriptionFalse_PreservesMandatorySemantics
// pins the canonical godlike/07 mandatory-transcription contract:
// flag=false means transcriber counter=1, repo counter=1 (exactly one
// call each — Equal pins check that no regression re-fires twice).
func TestStageProcessBatch_SkipTranscriptionFalse_PreservesMandatorySemantics(t *testing.T) {
	ctx := context.Background()
	orch, tr, repo := newSkipTranscriptionTestOrchestrator(false)
	ps := newTranscriptionPipelineState()

	err := orch.stageProcessBatch(ctx, ps)

	require.NoError(t, err,
		"stageProcessBatch must return nil; mandatory transcription uses no-failure doubles here")
	assert.Equal(t, int32(1), atomic.LoadInt32(&tr.calls),
		"transcriber.Transcribe MUST be called exactly once when ArtlistSkipTranscription=false (mandatory semantics)")
	assert.Equal(t, int32(1), atomic.LoadInt32(&repo.calls),
		"textTrackRepo.UpsertBatch MUST be called exactly once when ArtlistSkipTranscription=false (mandatory transcript persist)")
	require.Len(t, ps.resp.Items, 1)
	item := ps.resp.Items[0]
	assert.Equal(t, "processed", item.Status,
		"item.Status MUST be 'processed' on the happy path")
	assert.Equal(t, 0, ps.resp.Failed)
}

// TestStageProcessBatch_SkipTranscription_NilCfgPreservesMandatorySemantics
// pins the cfg=nil guard: pre-existing tests in this package build a
// RunOrchestratorService WITHOUT cfg (the gate's `o.svc.cfg != nil`
// guard makes that work). The contract is: nil cfg MUST NOT flip on
// the escape hatch — mandatory semantics preserved.
func TestStageProcessBatch_SkipTranscription_NilCfgPreservesMandatorySemantics(t *testing.T) {
	ctx := context.Background()
	tr := &countingTranscriber{}
	repo := &countingTextTrackRepo{}
	svc := &Service{
		// cfg intentionally nil — mirrors existing tests in the package
		mediaProcessor: &succeedMediaProcessor{},
		transcriber:    tr,
		textTrackRepo:  repo,
		log:            zap.NewNop(),
	}
	orch := &RunOrchestratorService{svc: svc}
	ps := newTranscriptionPipelineState()

	err := orch.stageProcessBatch(ctx, ps)

	require.NoError(t, err)
	assert.Equal(t, int32(1), atomic.LoadInt32(&tr.calls),
		"transcriber.Transcribe MUST be called once when cfg is nil (the `o.svc.cfg != nil` guard preserves mandatory semantics)")
	assert.Equal(t, int32(1), atomic.LoadInt32(&repo.calls),
		"textTrackRepo.UpsertBatch MUST be called once when cfg is nil (mandatory semantics preserved)")
}
