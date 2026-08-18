// Package voiceover — usecase_graceful_degradation_test.go
//
// Regression guard for the godlike/07 minimal-blast-radius contract on the
// batch use case: a filename build failure (BuildVoiceoverFilename) is a
// SECONDARY concern and must degrade the single item to a StatusFailed
// result — never panic the whole batch/worker.
//
// The failure is unreachable through the public Execute boundary (cmd.Validate
// gates non-empty Text + Language), so this test drives processOneLanguage
// directly (white-box, same package) with an empty-Text item to force the
// defensive seam.
package voiceover

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestProcessOneLanguage_FilenameBuildFailure_DegradesToFailedResult(t *testing.T) {
	db := openProcessTestDB(t)

	uc := NewGenerateVoiceoversUseCase(UseCaseDeps{
		TTSProvider:         &stubProcessTTS{},
		DestinationResolver: &stubProcessDestResolver{folderID: "folder-1"},
		Publisher:           &stubProcessPublisher{},
		VoiceoverRepository: &stubProcessVoRepo{db: db},
		Finalizer:           &stubProcessFinalizer{},
		Logger:              zap.NewNop(),
	})

	// Empty Text forces BuildVoiceoverFilename to error. Pre-fix this
	// panicked the whole batch; post-fix it must return a failed item.
	res := uc.processOneLanguage(
		context.Background(),
		&GenerateVoiceoversCommand{},
		VoiceoverItem{Text: "", Language: "en"},
		"req-1",
		TextHash("hash-001"),
		&ResolvedDestination{FolderID: "folder-1"},
	)

	assert.Equal(t, StatusFailed, res.Status,
		"filename build failure must degrade this item to StatusFailed, not panic the batch")
	assert.Contains(t, res.Error, "filename",
		"failed item must carry a filename-prefixed error for operator audit")
}
