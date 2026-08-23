package artlist

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ── Test doubles for the transcription failure paths ──────────────────────

// failingTranscriber is a Transcriber double that always returns err.
type failingTranscriber struct {
	err error
}

func (f *failingTranscriber) Transcribe(_ context.Context, _ string) (string, string, error) {
	return "", "", f.err
}

// failingTextTrackRepo is a TextTrackRepository double whose UpsertBatch
// always returns err. All other methods are delegated to the default
// no-op stub so the test fixture stays small.
type failingTextTrackRepo struct {
	stubTextTrackRepo
	err error
}

func (f *failingTextTrackRepo) UpsertBatch(_ context.Context, _ []asset.TextTrack) error {
	return f.err
}

// succeedMediaProcessor is a minimal Processor double that returns a
// successful ProcessResult with LocalPath populated so the downstream
// transcription path has a path to read.
type succeedMediaProcessor struct{}

func (s *succeedMediaProcessor) Process(_ context.Context, input *asset.ProcessInput) (*asset.ProcessResult, error) {
	return &asset.ProcessResult{
		ID:            input.ID,
		Filename:      input.Name + ".mp4",
		LocalPath:     input.OutputDir + "/" + input.Name + ".mp4",
		LegacyFileMD5: "hash-test",
		Status:        "processed",
	}, nil
}

// newTranscriptionTestOrchestrator builds a RunOrchestratorService whose
// stageProcessBatch will exercise the transcription path. mediaProcessor
// always succeeds; the caller can inject custom transcriber / text track
// repo doubles to test failure branches.
func newTranscriptionTestOrchestrator(t *testing.T, transcriber Transcriber, ttRepo asset.TextTrackRepository) *RunOrchestratorService {
	t.Helper()

	svc := &Service{
		mediaProcessor:  &succeedMediaProcessor{},
		transcriber:     transcriber,
		textTrackRepo:   ttRepo,
		assetProcessing: nil,
		log:             zap.NewNop(),
	}
	return &RunOrchestratorService{svc: svc}
}

// newTranscriptionPipelineState builds a pipelineState with a single
// clipWork item ready for stageProcessBatch.
func newTranscriptionPipelineState() *pipelineState {
	return &pipelineState{
		resp: &RunTagResponse{
			OK:    true,
			Term:  "transcription-test",
			Found: 1,
			Items: []RunTagItem{},
		},
		workItems: []clipWork{
			{
				item: RunTagItem{
					ClipID: "clip-transcription-001",
					Name:   "stub",
				},
				processInput: &asset.ProcessInput{
					ID:        "clip-transcription-001",
					Name:      "stub",
					SourceURL: "https://artlist.io/clip/001",
					OutputDir: "/tmp/artlist-test",
				},
			},
		},
		concurrency: 1,
	}
}

// TestStageProcessBatch_TranscriberError_BumpsFailedWithTranscriptionFailed
// verifies the PR-ARTLIST-MANDATORY-TRANSCRIPTION fail-closed branch:
// when the transcriber returns an error, the clip is counted as Failed,
// carries Status="transcription_failed", and the error message is
// surfaced in RunTagItem.Error.
func TestStageProcessBatch_TranscriberError_BumpsFailedWithTranscriptionFailed(t *testing.T) {
	ctx := context.Background()
	expectedErr := errors.New("whisper decode failure")

	transcriber := &failingTranscriber{err: expectedErr}
	ttRepo := &stubTextTrackRepo{}
	orchestrator := newTranscriptionTestOrchestrator(t, transcriber, ttRepo)
	ps := newTranscriptionPipelineState()

	err := orchestrator.stageProcessBatch(ctx, ps)

	require.NoError(t, err,
		"stageProcessBatch must return nil and record the per-clip failure in resp.Failed")
	assert.Equal(t, 1, ps.resp.Failed,
		"resp.Failed must be incremented when transcription fails")
	require.Len(t, ps.resp.Items, 1,
		"resp.Items must contain the failed clip")

	item := ps.resp.Items[0]
	assert.Equal(t, "transcription_failed", item.Status,
		"RunTagItem.Status must be the canonical 'transcription_failed' string")
	assert.Contains(t, item.Error, "transcription failed",
		"RunTagItem.Error must surface the transcription failure")
	assert.Contains(t, item.Error, expectedErr.Error(),
		"RunTagItem.Error must include the original transcriber error")
}

// TestStageProcessBatch_TranscriptPersistError_BumpsFailedWithTranscriptPersistFailed
// verifies the PR-ARTLIST-MANDATORY-TRANSCRIPTION persist failure branch:
// when the transcriber succeeds but the TextTrackRepository cannot persist
// the transcript, the clip is counted as Failed with
// Status="transcript_persist_failed" and the error is surfaced in
// RunTagItem.Error.
func TestStageProcessBatch_TranscriptPersistError_BumpsFailedWithTranscriptPersistFailed(t *testing.T) {
	ctx := context.Background()
	expectedErr := errors.New("sqlite locked")

	transcriber := &stubTranscriber{}
	ttRepo := &failingTextTrackRepo{err: expectedErr}
	orchestrator := newTranscriptionTestOrchestrator(t, transcriber, ttRepo)
	ps := newTranscriptionPipelineState()

	err := orchestrator.stageProcessBatch(ctx, ps)

	require.NoError(t, err,
		"stageProcessBatch must return nil and record the per-clip failure in resp.Failed")
	assert.Equal(t, 1, ps.resp.Failed,
		"resp.Failed must be incremented when transcript persistence fails")
	require.Len(t, ps.resp.Items, 1,
		"resp.Items must contain the failed clip")

	item := ps.resp.Items[0]
	assert.Equal(t, "transcript_persist_failed", item.Status,
		"RunTagItem.Status must be the canonical 'transcript_persist_failed' string")
	assert.Contains(t, item.Error, "transcript persist failed",
		"RunTagItem.Error must surface the transcript persist failure")
	assert.Contains(t, item.Error, expectedErr.Error(),
		"RunTagItem.Error must include the original repository error")
}
