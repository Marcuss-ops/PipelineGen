package asset

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestArtifactStageStateContract(t *testing.T) {
	for _, state := range []ArtifactStageState{
		ArtifactStageStateStaged,
		ArtifactStageStatePublished,
		ArtifactStageStateSucceeded,
		ArtifactStageStateFailedPermanent,
	} {
		if !state.IsValid() {
			t.Fatalf("state %q must be valid", state)
		}
	}
	for _, state := range []ArtifactStageState{ArtifactStageStateSucceeded, ArtifactStageStateFailedPermanent} {
		if !state.IsTerminal() {
			t.Fatalf("state %q must be terminal", state)
		}
	}
	for _, state := range []ArtifactStageState{ArtifactStageStateStaged, ArtifactStageStatePublished} {
		if state.IsTerminal() {
			t.Fatalf("state %q must not be terminal", state)
		}
	}
	if ArtifactStageState("unknown").IsValid() {
		t.Fatal("unknown state must be rejected")
	}
}

func TestRequirementContract(t *testing.T) {
	if !RequirementOptional.IsValid() || !RequirementRequired.IsValid() {
		t.Fatal("canonical requirements must be valid")
	}
	if Requirement("mandatory").IsValid() {
		t.Fatal("unknown requirement must be rejected")
	}
}

func TestArtifactStageErrorWrapping(t *testing.T) {
	err := WrapArtifactStageNotFound("art-1")
	if !errors.Is(err, ErrArtifactStageNotFound) || err.Error() == "" {
		t.Fatalf("wrapped not-found error lost its sentinel or message: %v", err)
	}
	if !errors.Is(WrapArtifactRequiredMissing("job-1", "required", "art-1"), ErrArtifactRequiredMissing) {
		t.Fatal("wrapped required-missing error lost its sentinel")
	}
}

func TestArtifactStageRepositoryContract(t *testing.T) {
	var _ ArtifactStageRepository = (*compileTimeArtifactStageRepository)(nil)
}

type compileTimeArtifactStageRepository struct{}

func (*compileTimeArtifactStageRepository) Insert(context.Context, *ArtifactStage) error { return nil }
func (*compileTimeArtifactStageRepository) GetByID(context.Context, string) (*ArtifactStage, error) {
	return nil, nil
}
func (*compileTimeArtifactStageRepository) ListByJob(context.Context, string) ([]ArtifactStage, error) {
	return nil, nil
}
func (*compileTimeArtifactStageRepository) ListByState(context.Context, ArtifactStageState, int) ([]ArtifactStage, error) {
	return nil, nil
}
func (*compileTimeArtifactStageRepository) MarkPublished(context.Context, string, string, time.Time) error {
	return nil
}
func (*compileTimeArtifactStageRepository) MarkSucceeded(context.Context, string) error { return nil }
func (*compileTimeArtifactStageRepository) MarkFailedPermanent(context.Context, string, string) error {
	return nil
}
func (*compileTimeArtifactStageRepository) IncrementAttemptCount(context.Context, string) error {
	return nil
}
func (*compileTimeArtifactStageRepository) InsertWithOutbox(context.Context, *ArtifactStage, string, []byte) (string, error) {
	return "", nil
}
