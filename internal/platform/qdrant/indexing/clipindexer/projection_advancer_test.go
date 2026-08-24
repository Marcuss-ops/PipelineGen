package clipindexer

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"
)

type recordingAdvancer struct {
	calls int
	err   error
}

func (a *recordingAdvancer) AdvanceActiveProjectionSequence(context.Context) error {
	a.calls++
	return a.err
}

func TestAdvanceProjectionSequenceInvokesWiredAdvancer(t *testing.T) {
	svc := NewService(nil, nil, "", zap.NewNop())
	adv := &recordingAdvancer{}
	svc.SetProjectionSequenceAdvancer(adv)

	svc.advanceProjectionSequence(context.Background(), "clip-1")
	if adv.calls != 1 {
		t.Fatalf("advancer calls: got %d, want 1", adv.calls)
	}
}

func TestAdvanceProjectionSequenceNilAdvancerIsNoop(t *testing.T) {
	svc := NewService(nil, nil, "", zap.NewNop())
	// No SetProjectionSequenceAdvancer call: must not panic and must not error.
	svc.advanceProjectionSequence(context.Background(), "clip-1")
}

func TestAdvanceProjectionSequenceErrorIsLoggedNotPropagated(t *testing.T) {
	svc := NewService(nil, nil, "", zap.NewNop())
	svc.SetProjectionSequenceAdvancer(&recordingAdvancer{err: errors.New("boom")})

	// Best-effort: the error must not panic and must not abort the caller.
	svc.advanceProjectionSequence(context.Background(), "clip-1")
}
