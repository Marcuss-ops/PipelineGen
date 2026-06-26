package scripts

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type generationCaptureEnqueuer struct {
	calls int
	req   *job.EnqueueRequest
}

func (e *generationCaptureEnqueuer) Enqueue(_ context.Context, req *job.EnqueueRequest) (*job.Job, error) {
	e.calls++
	e.req = req
	return &job.Job{ID: "job-1", Status: job.StatusQueued}, nil
}

func TestGenerationServiceUsesVersionedPayloadAndForcesGoogleDoc(t *testing.T) {
	enq := &generationCaptureEnqueuer{}
	svc := NewGenerationService(enq, nil, zap.NewNop())
	result, err := svc.EnqueueFromClips(context.Background(), scriptpkg.GenerationSpec{
		Title:           "Clip story",
		ClipIDs:         []string{"clip-a", "clip-b"},
		ExtractEntities: true,
	})
	require.NoError(t, err)
	require.Equal(t, "job-1", result.JobID)
	require.Equal(t, 1, enq.calls)
	require.Equal(t, job.TypeClipScriptGenerate, enq.req.Type)
	raw, ok := enq.req.Payload.([]byte)
	require.True(t, ok)
	payload, err := scriptpkg.DecodeGeneratePayload(raw)
	require.NoError(t, err)
	require.Equal(t, 1, payload.Version)
	require.Equal(t, scriptpkg.PresetCustom, payload.Preset)
	require.True(t, payload.Spec.CreateDoc)
	require.True(t, payload.Spec.ExtractEntities)
	require.Equal(t, []string{"clip-a", "clip-b"}, payload.Spec.ClipIDs)
}

func TestGenerationServiceRejectsEmptySpecBeforeEnqueue(t *testing.T) {
	enq := &generationCaptureEnqueuer{}
	svc := NewGenerationService(enq, nil, zap.NewNop())
	_, err := svc.EnqueueFromClips(context.Background(), scriptpkg.GenerationSpec{})
	require.ErrorIs(t, err, scriptpkg.ErrInvalidPayload)
	require.Equal(t, 0, enq.calls)
}
