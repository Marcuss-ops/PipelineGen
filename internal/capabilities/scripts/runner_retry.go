package scriptgeneration

import (
	"errors"
	"strings"

	kernelasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ErrFinalAudioLocalArtifactMissing marks the one deterministic local-file
// failure that is recoverable by replaying the audio compile phase. The
// checkpoint resume path clears the stale reference before compiling again.
var ErrFinalAudioLocalArtifactMissing = errors.New("final audio local artifact missing")

// deriveErrorCode extracts a stable machine-readable error code from
// the error chain and the failing stage. Returns a canonical string
// suitable for persisting as GenerationRun.ErrorCode.
//
// P0 verdetto: error codes must be stable so clients (retry bots,
// dashboards, monitoring) can branch on them reliably.
func deriveErrorCode(err error, stage Stage) string {
	if err == nil {
		return string(stage) + "_FAILED"
	}
	errStr := err.Error()

	// Typed errors take precedence over message heuristics so the durable
	// error code remains stable even when provider details change.
	if errors.Is(err, ErrMediaPreflight) {
		return "MEDIA_PREFLIGHT_FAILED"
	}
	// A remote render that is still running is a DEFERRAL, not an incident: the
	// code is stable so a retry bot can tell "come back later" apart from a real
	// failure. It must precede the message heuristics below, which would
	// otherwise classify the wrapped transport timeout as PROVIDER_TIMEOUT.
	if errors.Is(err, ErrFinalJobPending) {
		return "REMOTE_RENDER_PENDING"
	}
	// A stale remote identity (canceled preparation, lease held elsewhere) is
	// recoverable ONLY by preparing a fresh one. The code is stable so a retry
	// bot can tell "this attempt will rotate the remote identity" apart from a
	// render failure, and dashboards can count identity rotations.
	if errors.Is(err, ErrFinalJobStaleReceipt) {
		return "REMOTE_JOB_STALE"
	}
	// A plan the Master refused is the producer's defect, not an identity
	// incident. The code is stable so a retry bot stops instead of rotating the
	// attempt and resubmitting the same plan.
	if errors.Is(err, ErrFinalJobInvalidPlan) {
		return "REMOTE_PLAN_REJECTED"
	}
	// A remote service missing a required capability is a configuration gap,
	// not a provider outage: replaying the same final plan cannot add the
	// capability. Keep the code stable so the retry gate and dashboards agree.
	if errors.Is(err, ErrFinalJobCapabilityMissing) {
		return "REMOTE_CAPABILITY_MISSING"
	}
	// A producer that offers bytes which do not hash to the content address it
	// named has violated its contract; retrying the same payload cannot repair
	// it. The code is stable so a retry bot stops instead of re-rendering.
	if errors.Is(err, kernelasset.ErrContentAddressMismatch) {
		return "ASSET_CONTENT_MISMATCH"
	}
	// A localized/fixed-media render failure is dispatched from the voiceover
	// phase but is not a TTS failure. Its own code keeps the ledger honest and
	// lets a retry bot distinguish a renderer/font/IPC defect from a provider
	// outage.
	if errors.Is(err, ErrLocalizedRenderFailure) {
		return "LOCALIZED_RENDER_FAILED"
	}
	// A fail-closed word-budget rejection is a deterministic editorial
	// verdict: retrying the same provider/payload cannot repair it by
	// transport luck. Keep a stable code for retry bots and dashboards.
	if errors.Is(err, scriptpkg.ErrWordBudgetExceeded) {
		return "WORD_BUDGET_FAILED"
	}
	// A total image-provider outage is a preflight verdict, distinct from the
	// terminal certification failure. The stable code lets a retry bot wait
	// for the cooldown instead of treating the run as unrenderable content.
	if errors.Is(err, ErrImageProviderUnavailable) {
		return "IMAGE_PROVIDER_UNAVAILABLE"
	}

	// Check for known error patterns in the error message.
	// This is a lightweight heuristic; a future improvement could
	// use typed error interfaces (e.g. RetryableError, TransientError).
	switch {
	case containsAny(errStr, "INCOMPLETE_RENDER_SET"):
		return "INCOMPLETE_RENDER_SET"
	// The semantic certification verdict (CERTIFIED=false) is the most actionable
	// terminal class in the failure log — IMAGE FANOUT / CROSS-SCENE REUSE name
	// the exact fix — but the generic heuristics below could never see it: the
	// "render" substring inside the message routed it to ENQUEUE_FAILED, hiding
	// the editorial root cause under an infrastructure label. It must be
	// classified BEFORE those heuristics for the same reason the typed errors
	// above are.
	case containsAny(errStr, "CERTIFIED=false"):
		return "SEMANTIC_CERTIFICATION_FAILED"
	case containsAny(errStr, "timeout", "deadline exceeded", "context deadline"):
		return "PROVIDER_TIMEOUT"
	case containsAny(errStr, "unavailable", "not configured", "not initialized", "not found", "connection refused"):
		return "PROVIDER_UNAVAILABLE"
	case containsAny(errStr, "invalid response", "malformed", "decode failed", "parse error"):
		return "PROVIDER_BAD_RESPONSE"
	case containsAny(errStr, "empty", "zero", "no scenes", "no results"):
		return "EMPTY_RESULT"
	case containsAny(errStr, "generate scene text failed"):
		return "TEXT_GENERATION_FAILED"
	case containsAny(errStr, "translate"):
		return "TRANSLATION_FAILED"
	case containsAny(errStr, "voiceover"):
		return "VOICEOVER_FAILED"
	case containsAny(errStr, "document", "upsert", "google doc"):
		return "DOCUMENT_FAILED"
	case containsAny(errStr, "enqueue", "render", "worker"):
		return "ENQUEUE_FAILED"
	default:
		return string(stage) + "_FAILED"
	}
}

// isPermanentMediaPreflightFailure reports a static VidRush registration
// failure that cannot be repaired by retrying within the same frozen runtime
// composition. Other preflight failures retain the normal retry policy.
func isPermanentMediaPreflightFailure(err error) bool {
	if errors.Is(err, ErrFinalJobReceiptCheckpoint) {
		return true
	}
	var preflightErr *MediaPreflightError
	if !errors.As(err, &preflightErr) || preflightErr == nil {
		return false
	}
	for _, failure := range preflightErr.Result.Failures {
		if failure.Category == "vidrush_provider" {
			return true
		}
	}
	return false
}

// containsAny reports whether s contains any of the substrings.
func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
