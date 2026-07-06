package usecase

import scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

// ── Plan-phase helpers ────────────────────────────────────────────────

// buildResolutionContext constructs a SourceResolutionContext from a
// GenerationItemV2. PR 4 (June 2026): the resolver signature now
// expects resolution-context as a separate arg so resolvers see
// operator-side traits (language, tone, model, style, target words)
// without hijacking SourceSpec.Guidelines. SourceSpec.Guidelines
// remains for the pure-text editorial-overrides path; here we
// explicitly read operator intent from item-style fields.
//
// Field mapping:
//   - ItemID    — item.ID
//   - Title     — item.Title (canonical document title)
//   - Language  — item.Language (real target language; the curate
//     resolver previously hijacked Guidelines here —
//     the bug class)
//   - Tone      — item.Tone
//   - Model     — item.Model
//   - Style     — item.Style
//   - TargetWords — item.ScriptParams.TargetWords
func buildResolutionContext(item scriptpkg.GenerationItemV2) scriptpkg.SourceResolutionContext {
	return scriptpkg.SourceResolutionContext{
		ItemID:        item.ID,
		Title:         item.Title,
		Language:      item.Language,
		Tone:          item.Tone,
		Model:         item.Model,
		Style:         item.Style,
		TargetWords:   item.ScriptParams.TargetWords,
		NumClips:      item.Source.NumClips,
		SegmentWords:  item.ScriptParams.SegmentWords,
		SegmentTopics: append([]string(nil), item.ScriptParams.SegmentTopics...),
		// P0 #3 (June 2026): DriveLink is only required when the
		// caller wants document or scene images. For text-only
		// generation, clips without Drive links are still usable.
		RequireDriveLink: item.Output.GenerateDocument || item.Output.GenerateSceneImages,
	}
}
