package scriptgeneration

import (
	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func setDefaultLocalizedIdentityMatcherForTests(m LocalizedIdentityMatcher) {
	defaultLocalizedIdentityMatcher.Store(localizedMatcherHook{matcher: m})
}

func semanticBulletSpans(transcript string, inputs []scriptpkg.SceneAnalysisInput, bundles map[string]*SemanticCandidateBundle) ([]scriptpkg.SemanticBulletSpan, bool) {
	if len(inputs) == 0 {
		return nil, false
	}
	for _, in := range inputs {
		if in.StartByte == nil || in.EndByte == nil || *in.StartByte < 0 || *in.EndByte <= *in.StartByte || *in.EndByte > len(transcript) || transcript[*in.StartByte:*in.EndByte] != in.Text {
			return nil, false
		}
	}
	spans := make([]scriptpkg.SemanticBulletSpan, 0)
	for _, in := range inputs {
		bundle := bundles[in.SceneID]
		if bundle == nil {
			continue
		}
		for _, candidate := range bundle.Bullets {
			if candidate.Start < 0 || candidate.End <= candidate.Start || candidate.End > len(in.Text) || in.Text[candidate.Start:candidate.End] != candidate.Text {
				continue
			}
			start, end := *in.StartByte+candidate.Start, *in.StartByte+candidate.End
			if end > len(transcript) || transcript[start:end] != candidate.Text {
				continue
			}
			score := candidate.Score
			spans = append(spans, scriptpkg.SemanticBulletSpan{StartByte: start, EndByte: end, Text: candidate.Text, Score: &score})
		}
	}
	return spans, true
}

func randomClipStartSFXIntents(timeline capabilityaudio.CanonicalTimeline) []scriptpkg.SoundEffectIntent {
	return scriptaudio.RandomClipStartSFXIntents(timeline)
}
