package videocreate

import (
	"context"
	"errors"
	"testing"
	"time"

	assetdetail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// readinessRepoStub is a TextTrackRepository whose FindReady flips from
// "not yet" to READY after N calls (the async translation fan-out shape).
type readinessRepoStub struct {
	assetdetail.TextTrackRepository
	misses int
	calls  int
}

func (s *readinessRepoStub) FindReady(context.Context, string, string, assetdetail.TextTrackKind) (*assetdetail.TextTrack, []assetdetail.TimedCue, error) {
	s.calls++
	if s.calls <= s.misses {
		return nil, nil, nil
	}
	return &assetdetail.TextTrack{LanguageCode: "it"}, nil, nil
}

// TestTranscriptReadiness_WaitsForAsyncTranslation pins the sequencing
// contract: the workflow polls until the translation materializes (it must
// NOT race the render fan-out), then proceeds.
func TestTranscriptReadiness_WaitsForAsyncTranslation(t *testing.T) {
	t.Parallel()
	repo := &readinessRepoStub{misses: 2}
	w := &transcriptReadiness{repo: repo, every: time.Millisecond, timeout: time.Second}
	if err := w.WaitTranscriptReady(context.Background(), "yt_x_0_5_v1", "it"); err != nil {
		t.Fatalf("WaitTranscriptReady: %v", err)
	}
	if repo.calls != 3 {
		t.Errorf("FindReady calls = %d, want 3 (2 misses then READY)", repo.calls)
	}
}

// TestTranscriptReadiness_FailsClosedOnDeadline pins godlike/07: a
// translation that never materializes is a typed failure, never a silent
// render with the wrong-language track.
func TestTranscriptReadiness_FailsClosedOnDeadline(t *testing.T) {
	t.Parallel()
	repo := &readinessRepoStub{misses: 1 << 30}
	w := &transcriptReadiness{repo: repo, every: time.Millisecond, timeout: 5 * time.Millisecond}
	err := w.WaitTranscriptReady(context.Background(), "yt_x_0_5_v1", "it")
	if err == nil || !errors.Is(err, context.DeadlineExceeded) && err.Error() == "" {
		t.Fatalf("err = %v, want bounded failure", err)
	}
}
