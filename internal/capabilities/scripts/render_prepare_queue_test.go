package scriptgeneration

import (
	"context"
	"errors"
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

type prepareQueueStub struct {
	submitErr error
	submitted RenderQueueJob
}

func (s *prepareQueueStub) Submit(_ context.Context, job RenderQueueJob) error {
	s.submitted = job
	return s.submitErr
}

func (*prepareQueueStub) Get(context.Context, string) (RenderQueueJob, error) {
	return RenderQueueJob{}, errors.New("not used")
}

func validPrepareQueueRequest() capabilityoverlay.PrepareRequest {
	return capabilityoverlay.PrepareRequest{
		SchemaVersion: capabilityoverlay.SchemaVersionPrepare,
		PlanID:        "run-prepare-001",
		VideoID:       "run-prepare-001",
		Width:         1280,
		Height:        720,
		FPSNum:        30,
		FPSDen:        1,
		Intents: []capmodel.OverlayIntent{{
			Version:     capmodel.OverlayIntentVersion,
			IntentID:    "intent-scene-0-tom-hanks",
			SceneID:     "scene-0",
			SceneIndex:  0,
			Entity:      capmodel.EntityBinding{Type: "PERSON", CanonicalName: "Tom Hanks"},
			Source:      capmodel.IntentSourceEntity,
			Kind:        string(capmodel.KindEntityCard),
			TemplateID:  "person_default",
			Payload:     capmodel.IntentPayload{Name: "Tom Hanks"},
			TimingState: capmodel.TimingStatePending,
		}},
	}
}

// TestQueuePrepareEnqueuerTreatsExistingJobAsSuccess pins the advisory
// prepare contract: a deterministic job id that is already present, including
// one whose request identity changed, is an idempotent no-op rather than a run
// failure. Unlike render, prepare never owns a content outcome.
func TestQueuePrepareEnqueuerTreatsExistingJobAsSuccess(t *testing.T) {
	for _, submitErr := range []error{ErrJobExists, ErrRenderRequestConflict} {
		t.Run(submitErr.Error(), func(t *testing.T) {
			client := &prepareQueueStub{submitErr: submitErr}
			enqueuer, err := NewQueuePrepareEnqueuer(client)
			if err != nil {
				t.Fatalf("new enqueuer: %v", err)
			}
			req := validPrepareQueueRequest()
			if err := enqueuer.EnqueuePrepare(context.Background(), req); err != nil {
				t.Fatalf("existing prepare job must be idempotent, got %v", err)
			}
			if client.submitted.ID != "prepare-"+req.PlanID {
				t.Fatalf("submitted job id = %q, want deterministic prepare id", client.submitted.ID)
			}
		})
	}
}

// TestQueuePrepareEnqueuerPropagatesRealSubmitErrors keeps the idempotency
// exception narrow: a transport or queue failure is still a run failure.
func TestQueuePrepareEnqueuerPropagatesRealSubmitErrors(t *testing.T) {
	sentinel := errors.New("queue unavailable")
	client := &prepareQueueStub{submitErr: sentinel}
	enqueuer, err := NewQueuePrepareEnqueuer(client)
	if err != nil {
		t.Fatalf("new enqueuer: %v", err)
	}
	err = enqueuer.EnqueuePrepare(context.Background(), validPrepareQueueRequest())
	if !errors.Is(err, sentinel) {
		t.Fatalf("submit error = %v, want wrapped sentinel", err)
	}
}
