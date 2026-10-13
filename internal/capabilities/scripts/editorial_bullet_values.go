package scriptgeneration

import (
	"context"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// visualBulletCandidateLimit bounds the VisualNER sentences requested per
// scene. Bullets are one per entity-bearing sentence, so a small bound is
// enough and keeps the persistent worker's output bounded.
const visualBulletCandidateLimit = 12

// attachVisualBullets replaces each scene's phrase-impact bullets with the
// first VisualNER sentence bullet that re-maps onto the Go sentence
// splitter. Exactly one bullet per scene is kept (the visual contract); a
// scene with no mappable candidate keeps its existing bullets. Failures keep
// the existing bullets; they never fail the generation.
func (r *Runner) attachVisualBullets(ctx context.Context, language string, inputs []scriptpkg.SceneAnalysisInput, highlights []scriptpkg.SceneHighlight) []scriptpkg.SceneHighlight {
	if r == nil || r.sceneBulletAnalyzer == nil || len(highlights) == 0 {
		return highlights
	}
	texts := make(map[string]string, len(inputs))
	for _, in := range inputs {
		texts[in.SceneID] = in.Text
	}
	for i := range highlights {
		text := texts[highlights[i].SceneID]
		if strings.TrimSpace(text) == "" {
			continue
		}
		candidates, err := r.sceneBulletAnalyzer.BulletCandidates(ctx, language, text, visualBulletCandidateLimit)
		if err != nil {
			r.log.Warn("visualner bullet analysis unavailable; keeping existing bullets",
				zap.String("scene_id", highlights[i].SceneID), zap.Error(err))
			continue
		}
		if replacement, ok := selectVisualBullet(candidates, text); ok {
			highlights[i].Bullets = replacement
			highlights[i].GloballyIndexed = false
		}
	}
	return highlights
}

// selectVisualBullet returns the first candidate whose text re-maps onto a
// contiguous run of Go sentences. The span uses the Go splitter's indices so
// validEditorialBullet accepts it; the Rust sentence index is only a
// preference hint, never trusted blindly.
func selectVisualBullet(candidates []BulletCandidate, sceneText string) ([]scriptpkg.SceneBulletSpan, bool) {
	sentences := editorialSentences(sceneText)
	if len(sentences) == 0 {
		return nil, false
	}
	for _, candidate := range candidates {
		trimmed := strings.TrimSpace(candidate.Text)
		if trimmed == "" {
			continue
		}
		if !strings.Contains(sceneText, candidate.Text) {
			continue
		}
		if span, ok := mapBulletToSentences(sentences, trimmed); ok {
			return []scriptpkg.SceneBulletSpan{{
				SentenceStart: span[0],
				SentenceEnd:   span[1],
				Text:          trimmed,
			}}, true
		}
	}
	return nil, false
}

// mapBulletToSentences finds a contiguous sentence run whose space-joined
// text equals the candidate (case-insensitive, trimmed). Single-sentence
// matches win; multi-sentence matches cover splitter drift.
func mapBulletToSentences(sentences []string, candidate string) ([2]int, bool) {
	for s := range sentences {
		if strings.EqualFold(strings.TrimSpace(sentences[s]), candidate) {
			return [2]int{s, s + 1}, true
		}
	}
	for s := range sentences {
		joined := strings.TrimSpace(sentences[s])
		for e := s + 1; e < len(sentences); e++ {
			joined = strings.TrimSpace(joined + " " + strings.TrimSpace(sentences[e]))
			if strings.EqualFold(joined, candidate) {
				return [2]int{s, e + 1}, true
			}
		}
	}
	return [2]int{}, false
}
