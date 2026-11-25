// Package jobs — failure_class.go: the ONE classifier for durable-run failures.
//
// The rules below are derived from the measured taxonomy of the 2026-11-17..20
// window (135 failed job docs, grouped by stage/code/message). They exist for
// one decision only: does a new attempt have any chance of a different outcome?
//
// The durable request is immutable across attempts by construction
// (buildDurableRunRequest returns the persisted run.Request, never a rebuilt
// envelope), so a contract failure CANNOT change on retry. That is why the
// deterministic classes are allowed to skip the run's scheduled retry instead
// of replaying it.
//
// Rules stay narrow and message-anchored on purpose: a code alone is too coarse
// (ENQUEUE_FAILED covers both "invalid overlay plan" and "overlay render
// failed"), and an over-broad rule would turn a transient provider outage into
// a permanent failure.
package jobs

import (
	"strings"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// classifyRunFailure maps a durable-run failure onto its canonical class.
//
// Unknown is the default and is RETRYABLE: this classifier may only remove
// retries it can justify with a deterministic or configuration cause.
func classifyRunFailure(stage, code, message string) job.FailureClass {
	stage = strings.ToUpper(strings.TrimSpace(stage))
	code = strings.ToUpper(strings.TrimSpace(code))
	msg := strings.ToLower(strings.TrimSpace(message))
	has := func(needles ...string) bool {
		for _, needle := range needles {
			if strings.Contains(msg, needle) {
				return true
			}
		}
		return false
	}

	// Cross-code contract assertions: the output was validated against the
	// certified plan/audio and rejected. Same plan + same audio => same verdict.
	if has("does not match certified", "exceeds timeline duration") {
		return job.FailureDeterministic
	}

	switch code {
	case "EMPTY_RESULT", "SEMANTIC_CERTIFICATION_FAILED", "DOCUMENT_FAILED", "IMAGE_PREFLIGHT_FAILED":
		// Certification/contract: the same inputs produce the same verdict.
		return job.FailureDeterministic
	case "GENERATING_SCENE_TEXT_FAILED":
		if has("committed scenes", "expected ", "got ") {
			// Barrier mismatch (expected N committed scenes, got M): deterministic
			// on the same scene set.
			return job.FailureDeterministic
		}
	case "PUBLISHING_DOCUMENTS_FAILED":
		if has("cannot fit", "non-overlapping", "does not fit") {
			// Writer window validation: the plan cannot be laid out. Detected
			// after the render, but still deterministic.
			return job.FailureDeterministic
		}
		if has("invalid_runtime_assets", "stock window preparation capability is unavailable", "capability is unavailable") {
			// Remote FINALIZE capability drift: the same plan cannot succeed on
			// this service version. Retrying the durable job only burns another
			// full render; surface it as a configuration gap, not a provider
			// outage.
			return job.FailureConfiguration
		}
	case "ENQUEUE_FAILED":
		if has("plan compilation failed", "invalid plan", "unknown, retired", "compile v2", "seal:") {
			return job.FailureDeterministic
		}
		if has("has no certified rendered clip", "requires preset_id") {
			// The compiled plan references an artifact/preset the plan itself
			// should have produced: deterministic on the same plan.
			return job.FailureDeterministic
		}
		if has("ipc dial", "dial unix", "connection refused") {
			// Renderer socket absent: the host lost its daemon, not the input.
			return job.FailureInfrastructure
		}
		if has("render job failed", "render overlay item") {
			return job.FailureProvider
		}
	case "TRANSLATION_FAILED":
		if has("no provider available") {
			return job.FailureConfiguration
		}
	case "TEXT_GENERATION_FAILED":
		if has("circuit breaker") {
			// The Ollama breaker is a 15 s per-model cooldown that opens after
			// three failed calls on one worker; an open breaker means "no model
			// was even asked", not "no model can serve this request". Treating
			// it as configuration killed en/fr/pl of the r18 multilingual suite
			// at attempt 1/3 one second after submit, while the identical it
			// payload succeeded nine minutes later on another worker.
			return job.FailureProvider
		}
		if has("all models ski", "no model", "model not available") {
			return job.FailureConfiguration
		}
	case "REMOTE_CAPABILITY_MISSING":
		// The remote service version, not the payload, is the cause. No retry
		// of the same durable request can repair it.
		return job.FailureConfiguration
	case "PROVIDER_UNAVAILABLE", "PROVIDER_TIMEOUT":
		return job.FailureProvider
	case "VOICEOVER_FAILED", "PACKET_COPY_FAILED":
		// TTS circuit breaker / assembly transport: worth another attempt.
		return job.FailureProvider
	case "COMPILING_AUDIO_FAILED":
		if has("context canceled", "context deadline") {
			return job.FailureDeadline
		}
	case "NO_ERROR_CODE":
		if stage == "UNKNOWN_STAGE" || stage == "FAILED" {
			// A run left RUNNING with no error recorded is an orphaned worker,
			// not a contract verdict.
			return job.FailureInfrastructure
		}
	}

	if has("context deadline exceeded", "context canceled") {
		return job.FailureDeadline
	}
	return job.FailureUnknown
}
