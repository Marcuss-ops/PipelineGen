// Package scriptgeneration — runner_lifecycle_test.go covers the
// pure-domain lifecycle helpers from model.go: IsRunCompletable,
// ResumeFrom, StageIndex, Stage.IsTerminal, RetryDelay,
// ShouldRetry, ResolveDocsConfig. These are pure functions with
// no I/O and no goroutines — the lifecycle state-machine of the
// GenerationRun.
//
// godlike/06 SSOT invariants asserted:
//
//   - IsRunCompletable gates on (result != nil) ∧ (len(Scenes) > 0)
//     ∧ (every scene has text for every requested language) ∧
//     (every requested language has a document) ∧ (render_job
//     present iff render_video=true).
//   - ResumeFrom maps terminal state to StageCompleted; FAILED +
//     non-empty FailedStage → that stage; RUNNING → CurrentStage;
//     PENDING → StageNormalizing; nil → StageNormalizing.
//   - StageIndex maps stage strings to a 0-based index 0..6 with
//     -1 for terminal/unknown.
//   - Stage.IsTerminal is true ONLY for StageCompleted + StageFailed.
//   - RetryDelay is exponential capped at 120s:
//     attempt 0→5s, 1→10s, 2→20s, 3→40s, 4→80s, 5+→120s.
//   - ShouldRetry requires Status FAILED | RUNNING ∧ AttemptCount
//     < MaxRetries ∧ NextRetryAt ≤ now (or nil).
//   - ResolveDocsConfig prefers the canonical Docs struct; falls
//     back to the deprecated DocsEnabled + DriveFolderID +
//     top-level Languages.
package scriptgeneration

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestIsRunCompletable(t *testing.T) {
	validResult := &GenerateResult{
		Scenes: []Scene{
			{Text: map[Language]string{"en": "text1", "es": "texto1"}},
			{Text: map[Language]string{"en": "text2", "es": "texto2"}},
		},
		Documents: map[Language]DocumentReference{
			"en": {ID: "doc-en", Link: "https://doc-en"},
			"es": {ID: "doc-es", Link: "https://doc-es"},
		},
		RenderJob: &RenderReference{JobID: "render-1", Status: "QUEUED"},
	}

	t.Run("nil result", func(t *testing.T) {
		assert.False(t, IsRunCompletable(nil, []Language{"en"}, false))
	})

	t.Run("empty scenes", func(t *testing.T) {
		assert.False(t, IsRunCompletable(&GenerateResult{}, []Language{"en"}, false))
	})

	t.Run("missing translation", func(t *testing.T) {
		r := &GenerateResult{
			Scenes: []Scene{
				{Text: map[Language]string{"en": "text1"}},
			},
			Documents: map[Language]DocumentReference{
				"en": {ID: "doc-en", Link: "https://doc-en"},
				"es": {ID: "doc-es", Link: "https://doc-es"},
			},
		}
		assert.False(t, IsRunCompletable(r, []Language{"en", "es"}, false),
			"missing ES translation should be incompletable")
	})

	t.Run("missing document", func(t *testing.T) {
		r := &GenerateResult{
			Scenes: []Scene{
				{Text: map[Language]string{"en": "text1", "es": "texto1"}},
			},
			Documents: map[Language]DocumentReference{
				"en": {ID: "doc-en", Link: "https://doc-en"},
			},
		}
		assert.False(t, IsRunCompletable(r, []Language{"en", "es"}, false),
			"missing ES doc should be incompletable")
	})

	t.Run("missing render job when video enabled", func(t *testing.T) {
		r := &GenerateResult{
			Scenes: []Scene{
				{Text: map[Language]string{"en": "text1", "es": "texto1"}},
			},
			Documents: map[Language]DocumentReference{
				"en": {ID: "doc-en", Link: "https://doc-en"},
				"es": {ID: "doc-es", Link: "https://doc-es"},
			},
		}
		assert.False(t, IsRunCompletable(r, []Language{"en", "es"}, true),
			"missing render job should be incompletable when render_video=true")
	})

	t.Run("valid complete result", func(t *testing.T) {
		assert.True(t, IsRunCompletable(validResult, []Language{"en", "es"}, true))
	})

	t.Run("no video needed", func(t *testing.T) {
		r := &GenerateResult{
			Scenes: []Scene{
				{Text: map[Language]string{"en": "text1"}},
			},
			Documents: map[Language]DocumentReference{
				"en": {ID: "doc-en", Link: "https://doc-en"},
			},
		}
		assert.True(t, IsRunCompletable(r, []Language{"en"}, false),
			"should be completable without render job when render_video=false")
	})
}

func TestResumeFrom(t *testing.T) {
	t.Run("nil run", func(t *testing.T) {
		assert.Equal(t, StageNormalizing, ResumeFrom(nil))
	})

	t.Run("completed run", func(t *testing.T) {
		run := &GenerationRun{Status: RunStatusCompleted}
		assert.Equal(t, StageCompleted, ResumeFrom(run))
	})

	t.Run("failed at GENERATING_SCENE_TEXT", func(t *testing.T) {
		run := &GenerationRun{
			Status:      RunStatusFailed,
			FailedStage: StageGeneratingSceneText,
		}
		assert.Equal(t, StageGeneratingSceneText, ResumeFrom(run))
	})

	t.Run("failed with empty stage falls back to NORMALIZING", func(t *testing.T) {
		run := &GenerationRun{
			Status:      RunStatusFailed,
			FailedStage: "",
		}
		assert.Equal(t, StageNormalizing, ResumeFrom(run))
	})

	t.Run("running at TRANSLATING_SCENES", func(t *testing.T) {
		run := &GenerationRun{
			Status:       RunStatusRunning,
			CurrentStage: StageTranslatingScenes,
		}
		assert.Equal(t, StageTranslatingScenes, ResumeFrom(run))
	})

	t.Run("pending run", func(t *testing.T) {
		run := &GenerationRun{
			Status: RunStatusPending,
		}
		assert.Equal(t, StageNormalizing, ResumeFrom(run))
	})
}

func TestStageIndex(t *testing.T) {
	assert.Equal(t, 0, StageIndex(StageNormalizing), "NORMALIZING should be index 0")
	assert.Equal(t, 1, StageIndex(StageGeneratingSceneText))
	assert.Equal(t, 2, StageIndex(StageTranslatingScenes))
	assert.Equal(t, 3, StageIndex(StageGeneratingVoiceovers))
	assert.Equal(t, 4, StageIndex(StagePublishingDocuments))
	assert.Equal(t, 5, StageIndex(StageBuildingRenderPayload))
	assert.Equal(t, 6, StageIndex(StageEnqueuingRender))
	assert.Equal(t, -1, StageIndex(StageCompleted), "terminal stages should return -1")
	assert.Equal(t, -1, StageIndex(StageFailed), "terminal stages should return -1")
	assert.Equal(t, -1, StageIndex("UNKNOWN"), "unknown stage should return -1")
}

func TestStageIsTerminal(t *testing.T) {
	assert.True(t, StageCompleted.IsTerminal(), "COMPLETED should be terminal")
	assert.True(t, StageFailed.IsTerminal(), "FAILED should be terminal")
	assert.False(t, StageNormalizing.IsTerminal(), "NORMALIZING should not be terminal")
	assert.False(t, StageGeneratingSceneText.IsTerminal())
	assert.False(t, StageTranslatingScenes.IsTerminal())
	assert.False(t, StageGeneratingVoiceovers.IsTerminal())
	assert.False(t, StagePublishingDocuments.IsTerminal())
	assert.False(t, StageBuildingRenderPayload.IsTerminal())
	assert.False(t, StageEnqueuingRender.IsTerminal())
	assert.False(t, StageWorkerQueued.IsTerminal())
}

func TestRetryDelay(t *testing.T) {
	assert.Equal(t, 5*time.Second, RetryDelay(0), "attempt 0: 5s base delay")
	assert.Equal(t, 10*time.Second, RetryDelay(1), "attempt 1: 10s")
	assert.Equal(t, 20*time.Second, RetryDelay(2), "attempt 2: 20s")
	assert.Equal(t, 40*time.Second, RetryDelay(3), "attempt 3: 40s")
	assert.Equal(t, 80*time.Second, RetryDelay(4), "attempt 4: 80s")
	assert.Equal(t, 120*time.Second, RetryDelay(5), "attempt 5: capped at 120s")
	assert.Equal(t, 120*time.Second, RetryDelay(10), "attempt 10: capped at 120s")
}

func TestShouldRetry(t *testing.T) {
	future := time.Now().Add(1 * time.Hour)
	past := time.Now().Add(-1 * time.Hour)

	t.Run("nil run", func(t *testing.T) {
		assert.False(t, ShouldRetry(nil))
	})

	t.Run("completed run", func(t *testing.T) {
		run := &GenerationRun{Status: RunStatusCompleted}
		assert.False(t, ShouldRetry(run))
	})

	t.Run("max retries exhausted", func(t *testing.T) {
		run := &GenerationRun{
			Status:       RunStatusFailed,
			AttemptCount: MaxRetries,
		}
		assert.False(t, ShouldRetry(run))
	})

	t.Run("retry in future not yet", func(t *testing.T) {
		run := &GenerationRun{
			Status:       RunStatusFailed,
			AttemptCount: 1,
			NextRetryAt:  &future,
		}
		assert.False(t, ShouldRetry(run), "should not retry before NextRetryAt")
	})

	t.Run("retry window open", func(t *testing.T) {
		run := &GenerationRun{
			Status:       RunStatusFailed,
			AttemptCount: 1,
			NextRetryAt:  &past,
		}
		assert.True(t, ShouldRetry(run), "should retry when NextRetryAt is in the past")
	})

	t.Run("no next retry set", func(t *testing.T) {
		run := &GenerationRun{
			Status:       RunStatusFailed,
			AttemptCount: 1,
			NextRetryAt:  nil,
		}
		assert.True(t, ShouldRetry(run), "should retry when NextRetryAt is nil")
	})

	t.Run("running with retries left", func(t *testing.T) {
		run := &GenerationRun{
			Status:       RunStatusRunning,
			AttemptCount: 1,
		}
		assert.True(t, ShouldRetry(run), "RUNNING status with retries left should allow retry")
	})
}

func TestResolveDocsConfig(t *testing.T) {
	t.Run("docs struct takes priority", func(t *testing.T) {
		req := GenerateRequest{
			Docs: DocumentsConfig{
				Enabled:   true,
				Languages: []Language{"en", "es"},
				FolderID:  "folder-new",
			},
			DocsEnabled:   false,
			DriveFolderID: "folder-old",
			Languages:     []Language{"fr", "de"},
		}
		enabled, langs, folderID := req.ResolveDocsConfig()
		assert.True(t, enabled)
		assert.Equal(t, []Language{"en", "es"}, langs)
		assert.Equal(t, "folder-new", folderID)
	})

	t.Run("fallback to deprecated fields", func(t *testing.T) {
		req := GenerateRequest{
			Docs:          DocumentsConfig{}, // Enabled false, Languages empty
			DocsEnabled:   true,
			DriveFolderID: "folder-old",
			Languages:     []Language{"en", "es"},
		}
		enabled, langs, folderID := req.ResolveDocsConfig()
		assert.True(t, enabled, "DocsEnabled should enable docs")
		assert.Equal(t, []Language{"en", "es"}, langs, "should fallback to top-level Languages")
		assert.Equal(t, "folder-old", folderID, "should fallback to DriveFolderID")
	})

	t.Run("disabled by default", func(t *testing.T) {
		req := GenerateRequest{
			Languages: []Language{"en"},
		}
		enabled, langs, _ := req.ResolveDocsConfig()
		assert.False(t, enabled, "docs should be disabled by default")
		assert.Empty(t, langs, "langs should be empty when docs are disabled")
	})

	t.Run("empty languages when enabled", func(t *testing.T) {
		req := GenerateRequest{
			Docs:      DocumentsConfig{Enabled: true},
			Languages: nil,
		}
		enabled, langs, _ := req.ResolveDocsConfig()
		assert.True(t, enabled)
		assert.Empty(t, langs, "langs should be empty when no languages configured")
	})
}
