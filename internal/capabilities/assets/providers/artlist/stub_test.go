package artlist

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// stubTranscriber is a minimal Transcriber test double that returns a
// deterministic transcript and language code. It is used by every
// artlist test that constructs a Service so the mandatory
// PR-ARTLIST-MANDATORY-TRANSCRIPTION port is satisfied.
type stubTranscriber struct{}

func (s *stubTranscriber) Transcribe(_ context.Context, _ string) (string, string, error) {
	return "stub transcript", "en", nil
}

// stubTextTrackRepo is a minimal detail.TextTrackRepository test double.
// All writes are no-ops and reads return empty / not-found results so the
// artlist pipeline can run transcription without persisting real data.
type stubTextTrackRepo struct{}

func (s *stubTextTrackRepo) UpsertBatch(_ context.Context, _ []detail.TextTrack) error {
	return nil
}

func (s *stubTextTrackRepo) Find(_ context.Context, _ string, _ string, _ detail.TextTrackKind) (*detail.TextTrack, error) {
	return nil, nil
}

func (s *stubTextTrackRepo) ListByAsset(_ context.Context, _ string) ([]detail.TextTrack, error) {
	return nil, nil
}

func (s *stubTextTrackRepo) FindReady(_ context.Context, _ string, _ string, _ detail.TextTrackKind) (*detail.TextTrack, []detail.TimedCue, error) {
	return nil, nil, nil
}

func (s *stubTextTrackRepo) ListReadyLanguages(_ context.Context, _ string, _ detail.TextTrackKind) ([]string, error) {
	return nil, nil
}

func (s *stubTextTrackRepo) FindCurrentForTranslation(_ context.Context, _ string, _ detail.TextTrackKind, _ string, _ string, _ string, _ string, _ string) (*detail.TextTrack, error) {
	return nil, nil
}

func (s *stubTextTrackRepo) InsertTranslationWithAuditPredecessor(_ context.Context, _ detail.TextTrack) error {
	return nil
}

// Compile-time assertions: the stubs satisfy the canonical ports.
var _ Transcriber = (*stubTranscriber)(nil)
var _ detail.TextTrackRepository = (*stubTextTrackRepo)(nil)
