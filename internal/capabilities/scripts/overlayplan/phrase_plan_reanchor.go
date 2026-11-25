package overlayplan

import (
	"fmt"
	"strings"
	"unicode"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
)

// reanchorPhrasePlanItems makes the certified scene word timing the final
// authority for phrase-card windows. The Rust planner still selects and writes
// the card copy; this projection prevents stale or scene-relative planner
// offsets from stretching a short spoken phrase across the rest of a scene.
func reanchorPhrasePlanItems(plan *capmodel.OverlayPlan, result *contract.GenerateResult) error {
	if plan == nil || result == nil || len(result.SceneSpeechTimings) == 0 {
		return nil
	}
	timings := make(map[string]capabilityaudio.SceneSpeechTiming, len(result.SceneSpeechTimings))
	for _, timing := range result.SceneSpeechTimings {
		timings[timing.SceneID] = timing
	}
	for i := range plan.Items {
		item := &plan.Items[i]
		timing, ok := timings[item.SceneID]
		if !ok {
			continue
		}
		baseUS := int64(0)
		if len(timing.Phrases) > 0 {
			baseUS = timing.Phrases[0].TimelineStartUS
		}
		if isPhraseItem(*item) {
			start, end, err := locateTimedSurface(timing.Words, item.Text, item.StartUSValue()-baseUS)
			if err != nil {
				return fmt.Errorf("overlay plan: phrase %q in scene %q: %w", item.ID, item.SceneID, err)
			}
			setPhraseWindow(item, baseUS+start, end-start)
			continue
		}
		isPhraseChain := strings.EqualFold(strings.TrimSpace(item.Kind), capmodel.KindPhraseChain) ||
			strings.EqualFold(strings.TrimSpace(item.TemplateID), capmodel.TemplatePhraseChain)
		isMixedChain := strings.EqualFold(strings.TrimSpace(item.Kind), capmodel.KindMixedChain) ||
			strings.EqualFold(strings.TrimSpace(item.TemplateID), capmodel.TemplateMixedChain)
		if !isPhraseChain && !isMixedChain {
			continue
		}
		segments, ok := item.Segments()
		if !ok {
			continue
		}
		changed := false
		var first, last int64
		for j := range segments {
			segment := &segments[j]
			if isPhraseSegment(*segment) {
				start, end, err := locateTimedSurface(timing.Words, segment.Text, segment.StartUS-baseUS)
				if err != nil {
					return fmt.Errorf("overlay plan: phrase chain %q segment %d in scene %q: %w", item.ID, j, item.SceneID, err)
				}
				segment.StartUS = baseUS + start
				segment.DurationUS = end - start
				changed = true
			}
			if segment.DurationUS <= 0 {
				continue
			}
			if first == 0 || segment.StartUS < first {
				first = segment.StartUS
			}
			if segment.StartUS+segment.DurationUS > last {
				last = segment.StartUS + segment.DurationUS
			}
		}
		if changed {
			if first == 0 || last <= first {
				return fmt.Errorf("overlay plan: phrase chain %q in scene %q has no positive child window", item.ID, item.SceneID)
			}
			item.SetChainSegments(segments)
			setPhraseWindow(item, first, last-first)
		}
	}
	return nil
}

func isPhraseItem(item capmodel.OverlayItem) bool {
	return strings.EqualFold(strings.TrimSpace(item.Kind), "text_phrase") ||
		strings.EqualFold(strings.TrimSpace(item.TemplateID), "IMPORTANT_PHRASE")
}

func isPhraseSegment(segment capmodel.ChainSegment) bool {
	return strings.EqualFold(strings.TrimSpace(segment.Kind), "text_phrase") ||
		strings.EqualFold(strings.TrimSpace(segment.TemplateID), "IMPORTANT_PHRASE")
}

func setPhraseWindow(item *capmodel.OverlayItem, startUS, durationUS int64) {
	item.StartUS = startUS
	item.DurationUS = durationUS
	item.StartMs = startUS / 1000
	item.EndMs = (startUS + durationUS + 999) / 1000
}

// locateTimedSurface matches whole tokens against the provider's certified
// word boundaries. If a repeated phrase exists, choose the occurrence nearest
// the planner's proposed position so its editorial selection stays intact.
func locateTimedSurface(words []capabilityaudio.SpeechWordTiming, surface string, proposedStartUS int64) (int64, int64, error) {
	query := phraseTokens(surface)
	if len(query) == 0 {
		return 0, 0, fmt.Errorf("empty phrase surface")
	}
	type tokenBoundary struct {
		token string
		word  int
	}
	flat := make([]tokenBoundary, 0, len(words))
	for i, word := range words {
		for _, token := range phraseTokens(word.Text) {
			flat = append(flat, tokenBoundary{token: token, word: i})
		}
	}
	var bestStart, bestEnd int64
	bestDistance := int64(-1)
	for offset := 0; offset+len(query) <= len(flat); offset++ {
		matched := true
		for j := range query {
			if query[j] != flat[offset+j].token {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		first, last := words[flat[offset].word], words[flat[offset+len(query)-1].word]
		distance := first.StartUS - proposedStartUS
		if distance < 0 {
			distance = -distance
		}
		if bestDistance < 0 || distance < bestDistance {
			bestStart, bestEnd, bestDistance = first.StartUS, last.EndUS, distance
		}
	}
	if bestDistance < 0 || bestEnd <= bestStart {
		return 0, 0, fmt.Errorf("phrase is not anchored in certified voiceover timing")
	}
	return bestStart, bestEnd, nil
}

func phraseTokens(text string) []string {
	var tokens []string
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			tokens = append(tokens, strings.ToLower(current.String()))
			current.Reset()
		}
	}
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			current.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return tokens
}
