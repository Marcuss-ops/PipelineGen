package voiceover

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

const (
	canonicalStageTTS       kernobs.StageName = "tts"
	canonicalStageAudioPost kernobs.StageName = "audio_post"
	canonicalStagePublish   kernobs.StageName = "publish"
	canonicalStageFinalize  kernobs.StageName = "finalize"
)

// BuildVoiceoverIdempotencyKey derives the deterministic retry-safe
// deduplication key for the voiceover pipeline (FASE 3, July 2026).
// The canonical tuple (jobID + language + textHash + policyFingerprint)
// ensures that:
//   - Same job retried → same key (idempotency gate fires)
//   - Different job with same text → different key (no cross-job collision)
//   - Same job, different language → different key (per-language isolation)
//   - Same job/text but a DIFFERENT timing/post-processing policy →
//     different key, so a legacy audio-only cache entry is never reused
//     as a valid hit for a timing-required request.
//
// The key is a SHA-256 hex string of
// "jobID:language:textHash:policyFingerprint" so it is byte-stable across
// retries with the same inputs. Empty inputs produce a unique key that
// still hashes deterministically.
//
// godlike/07 minimum-blast-radius: the hash is computed via
// crypto/sha256 directly (no new dependencies).
func BuildVoiceoverIdempotencyKey(jobID string, language Language, textHash TextHash, policyFingerprint string) string {
	return digest.SHA256String(jobID + ":" + string(language) + ":" + string(textHash) + ":" + policyFingerprint)
}

// BuildVoiceoverTimingIdempotencyKey derives the deterministic retry-safe
// key for a timing bundle file (timing.json / SRT / VTT). It extends the
// canonical audio tuple with the timing policy fingerprint and a kind
// suffix so the timing files never collide with the audio upload's
// idempotency key, each projection retries to the same Drive file, and a
// changed timing policy produces a fresh timing file rather than reusing a
// stale one.
func BuildVoiceoverTimingIdempotencyKey(jobID string, language Language, textHash TextHash, policyFingerprint string, kind string) string {
	return digest.SHA256String(jobID + ":" + string(language) + ":" + string(textHash) + ":" + policyFingerprint + ":timing:" + kind)
}

// TimingPolicyFingerprint derives the stable cache fingerprint for the
// timing + post-processing policy. It captures the boundary mode, the
// timing schema version, the timing mode (disabled/best_effort/required)
// and the silence-removal policy, so a legacy audio-only cache entry (or
// an entry produced under a different policy) can never be reused as a
// valid hit for a request that expects a different timing bundle.
//
// nil timing falls back to the canonical defaults (best_effort / word /
// json), mirroring how the pipeline normalizes an absent policy.
func TimingPolicyFingerprint(timing *audio.TimingRequest, removeSilence bool) string {
	policy := audio.DefaultTimingRequest()
	if timing != nil {
		policy = timing.Normalized()
	}
	schema := 0
	if policy.Mode != audio.TimingDisabled {
		schema = audio.SpeechTimingVersion
	}
	return fmt.Sprintf("mode=%s;boundary=%s;schema=%d;silence=%t", policy.Mode, policy.BoundaryMode, schema, removeSilence)
}

// BuildVoiceoverContentFingerprint identifies reusable audio across jobs.
// The destination is included because the persisted artifact and its Drive
// ownership are part of the delivery contract. JobID is intentionally not
// included; retry isolation belongs to BuildVoiceoverIdempotencyKey.
func BuildVoiceoverContentFingerprint(textHash TextHash, language Language, voice, folderID string, timing *audio.TimingRequest, removeSilence bool) string {
	return digest.SHA256String("voiceover-content-v1:" + string(textHash) + ":" + string(language) + ":" + voice + ":" + folderID + ":" + TimingPolicyFingerprint(timing, removeSilence))
}
