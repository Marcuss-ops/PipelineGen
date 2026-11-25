package scriptgeneration

import (
	"errors"
	"testing"
)

func TestDeriveErrorCodeClassifiesLocalizedRenderFailure(t *testing.T) {
	err := errors.Join(ErrLocalizedRenderFailure, errors.New("font asset missing"))
	if got := deriveErrorCode(err, StageRenderingVideo); got != "LOCALIZED_RENDER_FAILED" {
		t.Fatalf("error code = %q, want LOCALIZED_RENDER_FAILED", got)
	}
	if got, want := StageIndex(StageRenderingVideo), StageIndex(StageGeneratingVoiceovers); got != want {
		t.Fatalf("StageIndex(RENDERING_VIDEO) = %d, want %d (resume must re-enter the voiceover fan-out)", got, want)
	}
}

func TestLocalizedRenderFailureKeepsUnderlyingCause(t *testing.T) {
	cause := errors.New("ipc timeout")
	err := errors.Join(ErrLocalizedRenderFailure, cause)
	if !errors.Is(err, cause) {
		t.Fatal("classified render error must preserve the underlying cause")
	}
}
