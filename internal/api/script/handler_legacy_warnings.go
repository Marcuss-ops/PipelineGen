package script

import (
	"fmt"
	"strings"

	"go.uber.org/zap"
)

// warnIgnoredLegacyFields logs deprecation warnings for silently ignored fields
// in the legacy generate-from-clips request. These fields are accepted for
// backward compatibility but have no effect on the generation pipeline.
func (h *ScriptFlowHandler) warnIgnoredLegacyFields(req *LegacyGenerateFromClipsRequest) {
	if h == nil || h.log == nil || req == nil {
		return
	}
	log := h.log.With(zap.String("endpoint", "generate-from-clips"))

	for i, c := range req.Clips {
		if strings.TrimSpace(c.Title) != "" {
			log.Warn("legacy_field_ignored",
				zap.String("field", fmt.Sprintf("clips[%d].title", i)),
				zap.String("value", c.Title),
				zap.String("note", "only clip_id is consumed; title is ignored"))
		}
		if strings.TrimSpace(c.URL) != "" {
			log.Warn("legacy_field_ignored",
				zap.String("field", fmt.Sprintf("clips[%d].url", i)),
				zap.String("value", c.URL),
				zap.String("note", "only clip_id is consumed; url is ignored"))
		}
	}

	if len(req.IntroClipIDs) > 0 {
		log.Warn("legacy_field_ignored",
			zap.String("field", "intro_clip_ids"),
			zap.Int("count", len(req.IntroClipIDs)),
			zap.String("note", "not consumed by resolver/engine/binder"))
	}
	if len(req.IntroClips) > 0 {
		log.Warn("legacy_field_ignored",
			zap.String("field", "intro_clips"),
			zap.Int("count", len(req.IntroClips)),
			zap.String("note", "not consumed by resolver/engine/binder"))
	}

	if strings.TrimSpace(req.SourceText) != "" && (len(req.ClipIDs) > 0 || len(req.Clips) > 0) {
		log.Warn("legacy_field_ignored",
			zap.String("field", "source_text"),
			zap.String("note", "replaced by resolver-built clip evidence text"))
	}

	if strings.TrimSpace(req.TranscriptPolicy) != "" {
		log.Warn("legacy_field_best_effort",
			zap.String("field", "transcript_policy"),
			zap.String("value", req.TranscriptPolicy),
			zap.String("note", "not fully enforced; 500-char transcript excerpt always used"))
	}

	if req.MinQualityScore != 0 {
		log.Warn("legacy_field_best_effort",
			zap.String("field", "min_quality_score"),
			zap.Float64("value", req.MinQualityScore),
			zap.String("note", "not enforced by BuildClipContext"))
	}
}
