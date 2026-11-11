package voiceover

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// Aliased from internal/kernel/observability: the registry is the single
// owner of every phase literal, so the voiceover service cannot drift from
// the taxonomy a report is joined against.
const (
	canonicalStageTTS       = kernobs.StageTTS
	canonicalStageAudioPost = kernobs.StageAudioPost
	canonicalStagePublish   = kernobs.StagePublish
	canonicalStageFinalize  = kernobs.StageFinalize
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
//
// Version v2 (2026-08-26): the voiceover output format was canonicalized to
// 48 kHz stereo MP3 (remove_silence now normalizes in the same pass that
// strips silence). The format token is part of the fingerprint so a v1
// (24 kHz mono) cache row is never reused as a valid hit for a v2 synthesis:
// old entries go cold and are re-synthesized once, then cached under v2.
func BuildVoiceoverContentFingerprint(textHash TextHash, language Language, voice, folderID string, timing *audio.TimingRequest, removeSilence bool) string {
	return digest.SHA256String("voiceover-content-v2:" + string(textHash) + ":" + string(language) + ":" + voice + ":" + folderID + ":" + TimingPolicyFingerprint(timing, removeSilence) + ":format=mp3-48k-stereo")
}

// idempotencyPolicyFingerprint folds the provider identity into the timing
// policy ONLY when the provider declares one. Providers without an identity
// keep the pre-v3 key verbatim, so enabling the local pilot invalidates
// exactly the routed languages and nothing else.
func idempotencyPolicyFingerprint(provider TTSProvider, language Language, timing *audio.TimingRequest, removeSilence bool) string {
	base := TimingPolicyFingerprint(timing, removeSilence)
	identity := providerIdentityForTTS(provider, language)
	if identity == "" {
		return base
	}
	return base + ";provider=" + identity
}

// ttsIdentityProvider is the optional port extension used by the cache
// fingerprint. The language router implements it; simple fakes that don't
// keep the legacy behaviour (empty identity) and stay deterministic.
type ttsIdentityProvider interface {
	ProviderIdentityFor(Language) string
}

func providerIdentityForTTS(provider TTSProvider, language Language) string {
	identity := ""
	if provider == nil {
	} else if providerIdentity, ok := provider.(ttsIdentityProvider); ok {
		identity = strings.TrimSpace(providerIdentity.ProviderIdentityFor(language))
	}
	if strings.EqualFold(string(language), "it") {
		identity += ";rate=+1%;silence=-40dB/120ms"
	}
	return identity
}

// BuildVoiceoverContentFingerprintV3 is the provider-aware content
// fingerprint. Version v3 (2026-11-09) adds the provider/model identity to
// the tuple: switching Edge -> Pocket/Phonon (or Pocket -> Phonon) MUST
// invalidate cached audio instead of reusing the previous engine's file,
// even when text, language, voice and destination are identical.
//
// An empty identity is hashed as "unspecified" (never silently equal to
// "edge-tts"), so a caller that forgets to expose an identity can only
// miss the cache, never reuse the wrong engine's audio.
func BuildVoiceoverContentFingerprintV3(textHash TextHash, language Language, voice, providerID, folderID string, timing *audio.TimingRequest, removeSilence bool) string {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		providerID = "unspecified"
	}
	return digest.SHA256String("voiceover-content-v3:" + string(textHash) + ":" + string(language) + ":" + voice + ":provider=" + providerID + ":" + folderID + ":" + TimingPolicyFingerprint(timing, removeSilence) + ":format=mp3-48k-stereo")
}

// contentFingerprint is the use-case seam that resolves the provider
// identity for the request language before hashing.
func (u *ProcessSegmentUseCase) contentFingerprint(textHash TextHash, language Language, voice, folderID string, timing *audio.TimingRequest, removeSilence bool) string {
	return BuildVoiceoverContentFingerprintV3(
		textHash, language, voice,
		providerIdentityForTTS(u.deps.TTSProvider, language),
		folderID, timing, removeSilence,
	)
}
