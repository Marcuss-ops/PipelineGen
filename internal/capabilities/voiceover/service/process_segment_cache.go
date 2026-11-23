// Package voiceover — cross-run cache phase extraction
// (BON-08: Execute decomposition).
//
// lookupCachedResult owns the "reuse a previously produced voiceover"
// decision that used to be inline in ProcessSegmentUseCase.Execute. The
// only branch that skips synthesis is a verified hit whose timing
// artifact has been hydrated when the caller requires timing; every
// other outcome (nil cache port, forced synthesis, lookup error, a
// candidate without timing) falls through to the full pipeline.
//
// godlike/07 NO-FAKE-AVAILABILITY: a timing-bearing cache hit is never
// reusable on a link or summary alone. The overlay compiler consumes
// the word-level artifact, so the cache port must have downloaded and
// validated it before this helper reports a hit.
package voiceover

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"go.uber.org/zap"
)

// timingPolicyFor resolves the canonical timing policy for a command.
// An absent policy means the provider defaults; an explicit policy is
// normalized exactly once at the pipeline boundary.
func timingPolicyFor(cmd *ProcessSegmentCommand) audio.TimingRequest {
	if cmd != nil && cmd.Timing != nil {
		return cmd.Timing.Normalized()
	}
	return audio.DefaultTimingRequest()
}

// cacheHitReusable reports whether a cache hit may short-circuit the
// pipeline. Timing-disabled runs reuse any hit; timing-bearing runs
// reuse only hits whose word-level artifact was hydrated and validated.
func cacheHitReusable(policy audio.TimingRequest, hit *VoiceoverCacheHit) bool {
	return hit != nil && (policy.Mode == audio.TimingDisabled || hit.Artifact != nil)
}

// lookupCachedResult probes the optional cross-run cache and returns a
// completed result only for a reusable hit. It reports false for every
// fall-through case so the caller continues with synthesis.
func (u *ProcessSegmentUseCase) lookupCachedResult(
	ctx context.Context,
	cmd *ProcessSegmentCommand,
	timingPolicy audio.TimingRequest,
	log *zap.Logger,
) (*VoiceoverItemResult, bool) {
	if cmd.ForceSynthesis || u.deps.Cache.VoiceoverCache == nil {
		return nil, false
	}

	fingerprint := u.contentFingerprint(
		cmd.TextHash, cmd.Language, cmd.Voice, cmd.Dest.FolderID,
		cmd.Timing, cmd.RemoveSilence,
		voiceoverPostPolicy(cmd.TTSRate, cmd.SilencePolicy),
	)
	hit, lookupErr := u.deps.Cache.VoiceoverCache.Lookup(ctx, fingerprint, timingPolicy.Mode != audio.TimingDisabled)
	switch {
	case lookupErr != nil:
		log.Warn("voiceover cache lookup error — falling through to full pipeline",
			zap.String("fingerprint", fingerprint),
			zap.String("language", string(cmd.Language)),
			zap.Error(lookupErr))
	case cacheHitReusable(timingPolicy, hit):
		log.Info("voiceover cache HIT — reusing existing audio, skipping TTS + upload + finalize",
			zap.String("fingerprint", fingerprint),
			zap.String("cached_id", hit.ID),
			zap.String("drive_file_id", hit.DriveFileID),
			zap.String("language", string(cmd.Language)))
		return buildCachedResult(cmd, hit, timingPolicy, log), true
	case hit != nil:
		log.Warn("voiceover cache candidate lacks validated word timing; falling through to synthesis",
			zap.String("fingerprint", fingerprint),
			zap.String("language", string(cmd.Language)))
	}

	log.Info("voiceover cache MISS — full pipeline will run",
		zap.String("fingerprint", fingerprint),
		zap.String("language", string(cmd.Language)))
	return nil, false
}
