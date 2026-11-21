// Package jobs — failure_class_test.go pins the taxonomy against the REAL
// failure messages measured in the 2026-11-17..20 window. Each case is a
// message copied from ops/jobs, so a future change to the classifier has to
// argue with production text, not with synthetic strings.
package jobs

import (
	"errors"
	"strings"
	"testing"
	"time"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

func TestClassifyRunFailure_MeasuredMessages(t *testing.T) {
	cases := []struct {
		name    string
		stage   string
		code    string
		message string
		want    job.FailureClass
	}{
		{
			name:    "invalid overlay plan is a contract verdict",
			stage:   "COMPILING_AUDIO",
			code:    "ENQUEUE_FAILED",
			message: "overlay plan compilation failed: overlay plan: compile v2: semantic-engine editorial plan worker: editorial plan: invalid plan.items.text-phrase-rise: window [38592000,685...",
			want:    job.FailureDeterministic,
		},
		{
			name:    "unknown retired motion is a contract verdict",
			stage:   "COMPILING_AUDIO",
			code:    "DOCUMENT_FAILED",
			message: `overlay plan: item "text-phrase-reveal__x": motion "text_phrase_reveal" is unknown, retired, renderer-unsupported`,
			want:    job.FailureDeterministic,
		},
		{
			name:    "non-overlapping writer window is a contract verdict",
			stage:   "PUBLISHING_DOCUMENTS",
			code:    "PUBLISHING_DOCUMENTS_FAILED",
			message: `remote final job: final_job overlay "typewriter-word-step-short__x" cannot fit a non-overlapping 38-frame window in scene "scene-01"`,
			want:    job.FailureDeterministic,
		},
		{
			name:    "empty certified pool is a contract verdict",
			stage:   "COMPILING_AUDIO",
			code:    "EMPTY_RESULT",
			message: "overlay plan: compile v2: editorial plan: invalid canvas.entity_image_motions: a non-empty certified pool is required when an entity image",
			want:    job.FailureDeterministic,
		},
		{
			name:    "semantic certification shortfall is a contract verdict",
			stage:   "GENERATING_SCENE_TEXT",
			code:    "SEMANTIC_CERTIFICATION_FAILED",
			message: "vidrush semantic certification failed: CERTIFIED=false (IMAGE FANOUT: images available = 0, target at least 2)",
			want:    job.FailureDeterministic,
		},
		{
			name:    "scene barrier mismatch is a contract verdict",
			stage:   "GENERATING_SCENE_TEXT",
			code:    "GENERATING_SCENE_TEXT_FAILED",
			message: "vidrush barrier: expected 5 committed scenes, got 7 (extra indexes [5 6])",
			want:    job.FailureDeterministic,
		},
		{
			name:    "published scene shorter than certified audio is a contract verdict",
			stage:   "PUBLISHING_DOCUMENTS",
			code:    "PUBLISHING_DOCUMENTS_FAILED",
			message: "remote final job: final_job scene duration 261992ms does not match certified final audio 266292ms",
			want:    job.FailureDeterministic,
		},
		{
			name:    "audio longer than the timeline is a contract verdict",
			stage:   "COMPILING_AUDIO",
			code:    "COMPILING_AUDIO_FAILED",
			message: "editing timeline preflight failed: editing timeline: audio duration 266292000 exceeds timeline duration 250000000",
			want:    job.FailureDeterministic,
		},
		{
			name:    "plan references a clip it never certified",
			stage:   "PUBLISHING_DOCUMENTS",
			code:    "ENQUEUE_FAILED",
			message: `remote final job: final_job clip-only scene "scene-01" has no certified rendered clip for item "x"`,
			want:    job.FailureDeterministic,
		},
		{
			name:    "missing renderer socket is infrastructure",
			stage:   "COMPILING_AUDIO",
			code:    "ENQUEUE_FAILED",
			message: "render job run_x failed: processor: render: ipc dial /run/chronon3d-b/chronon.sock: connect: connection refused",
			want:    job.FailureInfrastructure,
		},
		{
			name:    "missing translation provider is a configuration gap",
			stage:   "GENERATING_SCENE_TEXT",
			code:    "TRANSLATION_FAILED",
			message: "scene ready downstream failed: translate ready scene scene-01 to de: translation: no provider available for it->de",
			want:    job.FailureConfiguration,
		},
		{
			name:    "no warmable model is a configuration gap",
			stage:   "GENERATING_SCENE_TEXT",
			code:    "TEXT_GENERATION_FAILED",
			message: "engine: ollama generation failed: script generation failed: all models skipped",
			want:    job.FailureConfiguration,
		},
		{
			name:    "provider timeout is transient",
			stage:   "GENERATING_SCENE_TEXT",
			code:    "PROVIDER_TIMEOUT",
			message: "geocode grounded place: nominatim: request: context deadline exceeded (Client.Timeout exceeded)",
			want:    job.FailureProvider,
		},
		{
			name:    "render provider failure is transient",
			stage:   "COMPILING_AUDIO",
			code:    "PROVIDER_UNAVAILABLE",
			message: `overlay render for it failed: render overlay item "air-rise-type-on__x": render job failed`,
			want:    job.FailureProvider,
		},
		{
			name:    "tts circuit breaker is transient",
			stage:   "GENERATING_VOICEOVERS",
			code:    "VOICEOVER_FAILED",
			message: "voiceover TTS circuit breaker is open",
			want:    job.FailureProvider,
		},
		{
			name:    "cancelled audio compile is a deadline",
			stage:   "COMPILING_AUDIO",
			code:    "COMPILING_AUDIO_FAILED",
			message: "context canceled",
			want:    job.FailureDeadline,
		},
		{
			name:    "orphaned running run is infrastructure",
			stage:   "UNKNOWN_STAGE",
			code:    "NO_ERROR_CODE",
			message: "no error message recorded",
			want:    job.FailureInfrastructure,
		},
		{
			name:    "unmatched code stays retryable",
			stage:   "PUBLISHING_DOCUMENTS",
			code:    "SOMETHING_NEW",
			message: "remote final job is still PENDING",
			want:    job.FailureUnknown,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyRunFailure(tc.stage, tc.code, tc.message); got != tc.want {
				t.Fatalf("classifyRunFailure = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestIncompleteRunError_DeterministicFailureDoesNotDefer is the retry gate:
// even with a retry scheduled by the runner, a deterministic verdict must fail
// the job instead of handing back an attempt that would replay the same input.
func TestIncompleteRunError_DeterministicFailureDoesNotDefer(t *testing.T) {
	retryAt := time.Now().UTC().Add(30 * time.Second)
	run := &scriptgen.GenerationRun{
		ID:           "run_contract",
		Status:       scriptgen.RunStatusFailed,
		FailedStage:  scriptgen.StagePublishingDocuments,
		AttemptCount: 1,
		NextRetryAt:  &retryAt,
		ErrorCode:    "PUBLISHING_DOCUMENTS_FAILED",
		ErrorMessage: `final_job overlay "x" cannot fit a non-overlapping 38-frame window`,
	}

	err := incompleteRunError(run)
	if _, deferred := job.AsDeferral(err); deferred {
		t.Fatalf("a deterministic verdict must not defer, got %v", err)
	}
	classified, ok := job.ClassifiedFailureFrom(err)
	if !ok {
		t.Fatalf("a deterministic failure must carry its class, got %v", err)
	}
	if classified.Class != job.FailureDeterministic || !classified.Permanent() {
		t.Fatalf("class = %s permanent=%v, want deterministic/permanent", classified.Class, classified.Permanent())
	}
	if !strings.Contains(err.Error(), "durable run failed") {
		t.Fatalf("terminal message must stay canonical, got %q", err.Error())
	}
}

// TestIncompleteRunError_ProviderFailureStillDefers keeps the gate from
// swallowing the legitimate wait: a transient class with a scheduled retry must
// still hand the attempt back.
func TestIncompleteRunError_ProviderFailureStillDefers(t *testing.T) {
	retryAt := time.Now().UTC().Add(30 * time.Second)
	run := &scriptgen.GenerationRun{
		ID:           "run_provider",
		Status:       scriptgen.RunStatusFailed,
		FailedStage:  scriptgen.StageGeneratingSceneText,
		AttemptCount: 1,
		NextRetryAt:  &retryAt,
		ErrorCode:    "PROVIDER_TIMEOUT",
		ErrorMessage: "nominatim: context deadline exceeded",
	}

	err := incompleteRunError(run)
	if _, ok := job.AsDeferral(err); !ok {
		t.Fatalf("a provider failure with budget must defer, got %v", err)
	}
	if !strings.Contains(err.Error(), "class=provider") {
		t.Fatalf("deferral must name its class, got %q", err.Error())
	}
}

// TestIncompleteRunError_UnknownStaysRetryable is the conservative default: a
// new, unclassified message must not become permanent.
func TestIncompleteRunError_UnknownStaysRetryable(t *testing.T) {
	retryAt := time.Now().UTC().Add(time.Second)
	run := &scriptgen.GenerationRun{
		ID: "run_unknown", Status: scriptgen.RunStatusFailed,
		FailedStage: scriptgen.StageFailed, AttemptCount: 1,
		NextRetryAt: &retryAt, ErrorCode: "SOMETHING_NEW", ErrorMessage: "new shape",
	}
	err := incompleteRunError(run)
	if _, ok := job.AsDeferral(err); !ok {
		t.Fatalf("an unknown failure with budget must defer, got %v", err)
	}
	if classified, ok := job.ClassifiedFailureFrom(err); ok {
		t.Fatalf("a deferral must not be a classified terminal failure: %v", classified)
	}
}

// TestClassifyRunFailure_EmptyInputsDoNotPanic covers the nil-ish path.
func TestClassifyRunFailure_EmptyInputsDoNotPanic(t *testing.T) {
	if got := classifyRunFailure("", "", ""); got != job.FailureUnknown {
		t.Fatalf("empty failure classified as %s, want unknown", got)
	}
	if err := incompleteRunError(nil); err == nil || errors.Is(err, job.ErrDeferred) {
		t.Fatalf("nil run must be a plain terminal error, got %v", err)
	}
}
