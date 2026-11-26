package overlayplan

import (
	"fmt"
	"log"
	"sort"
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
				degradePhraseAnchor(item, err)
				continue
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
					degradePhraseAnchor(item, err)
					continue
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

// degradePhraseAnchor keeps the planner's own window when the certified
// voiceover timing cannot locate the surface. Failing the entire generation is
// the wrong contract: a replayed or cached plan can outlive the TTS transcript
// that anchored it (the replay regenerates voiceover with a slightly different
// normalization), and one unmatched phrase must not discard every other
// overlay. The fallback is explicit on the item and in the process log, so a
// genuinely wrong planner window is still diagnosable.
func degradePhraseAnchor(item *capmodel.OverlayItem, cause error) {
	if item == nil {
		return
	}
	if item.Params == nil {
		item.Params = make(map[string]any, 2)
	}
	item.Params["phrase_anchor"] = "planner_window"
	item.Params["phrase_anchor_reason"] = "not_anchored"
	log.Printf("overlay plan: phrase %q in scene %q kept on planner timing: %v",
		item.ID, item.SceneID, cause)
}

// dedupeReanchoredPhrasePlanItems removes identical phrase surfaces that were
// anchored to the same spoken words by the Go seal boundary. Rust deduplicates
// before sealing, but chapter bullets and extracted phrases can be projected
// onto the same certified word window after that pass.
func dedupeReanchoredPhrasePlanItems(plan *capmodel.OverlayPlan) {
	if plan == nil || len(plan.Items) < 2 {
		return
	}
	indices := make([]int, 0, len(plan.Items))
	for i := range plan.Items {
		if isPhraseItem(plan.Items[i]) {
			indices = append(indices, i)
		}
	}
	// Prefer the more intentional editorial surface when the same phrase is
	// projected twice: chapter bullet, scene bullet, then extracted phrase.
	phraseRank := func(item capmodel.OverlayItem) int {
		role, _ := item.Params["editorial_role"].(string)
		switch role {
		case "chapter_bullet":
			return 3
		case "scene_bullet":
			return 2
		default:
			return 1
		}
	}
	priority := func(item capmodel.OverlayItem) float64 {
		value, _ := item.Params["priority"].(float64)
		return value
	}
	for i := 0; i < len(indices); i++ {
		for j := i + 1; j < len(indices); j++ {
			a, b := plan.Items[indices[i]], plan.Items[indices[j]]
			if phraseRank(b) > phraseRank(a) || (phraseRank(b) == phraseRank(a) && priority(b) > priority(a)) {
				indices[i], indices[j] = indices[j], indices[i]
			}
		}
	}
	dropped := make(map[int]struct{})
	kept := make([]int, 0, len(indices))
	for _, index := range indices {
		item := plan.Items[index]
		normalized := strings.Join(phraseTokens(item.Text), " ")
		if normalized == "" {
			kept = append(kept, index)
			continue
		}
		start, end := item.StartUSValue(), item.EndUSValue()
		duplicate := false
		for chainIndex := range plan.Items {
			chain := &plan.Items[chainIndex]
			if chainIndex == index || chain.SceneID != item.SceneID || !isPhraseChain(*chain) {
				continue
			}
			segments, ok := chain.Segments()
			if !ok {
				continue
			}
			for _, segment := range segments {
				if !isPhraseSegment(segment) || strings.Join(phraseTokens(segment.Text), " ") != normalized {
					continue
				}
				if start < segment.StartUS+segment.DurationUS && segment.StartUS < end {
					duplicate = true
					break
				}
			}
			if duplicate {
				break
			}
		}
		if !duplicate {
			for _, keptIndex := range kept {
				previous := plan.Items[keptIndex]
				if previous.SceneID == item.SceneID &&
					strings.Join(phraseTokens(previous.Text), " ") == normalized &&
					start < previous.EndUSValue() && previous.StartUSValue() < end {
					duplicate = true
					break
				}
			}
		}
		if duplicate {
			dropped[index] = struct{}{}
		} else {
			kept = append(kept, index)
		}
	}
	if len(dropped) == 0 {
		return
	}
	items := make([]capmodel.OverlayItem, 0, len(plan.Items)-len(dropped))
	for i, item := range plan.Items {
		if _, ok := dropped[i]; !ok {
			items = append(items, item)
		}
	}
	plan.Items = items
}

func isPhraseChain(item capmodel.OverlayItem) bool {
	return strings.EqualFold(strings.TrimSpace(item.Kind), capmodel.KindPhraseChain) ||
		strings.EqualFold(strings.TrimSpace(item.TemplateID), capmodel.TemplatePhraseChain) ||
		strings.EqualFold(strings.TrimSpace(item.Kind), capmodel.KindMixedChain) ||
		strings.EqualFold(strings.TrimSpace(item.TemplateID), capmodel.TemplateMixedChain)
}

func isPhraseItem(item capmodel.OverlayItem) bool {
	return strings.EqualFold(strings.TrimSpace(item.Kind), "text_phrase") ||
		strings.EqualFold(strings.TrimSpace(item.TemplateID), "IMPORTANT_PHRASE")
}

func isNumericFactItem(item capmodel.OverlayItem) bool {
	switch strings.ToUpper(strings.TrimSpace(item.TemplateID)) {
	case "TIMELINE_DATE_CARD", "METRIC_STAT_CARD":
		return true
	}
	return strings.EqualFold(strings.TrimSpace(item.Kind), "number")
}

// isVisualImageItem reports asset-driven overlays. They are not centered text
// cards: an image deliberately shares the canvas with a phrase caption, so the
// text-card collision pass must not treat that overlap as a conflict.
func isVisualImageItem(item capmodel.OverlayItem) bool {
	switch strings.TrimSpace(item.Kind) {
	case "image", string(capmodel.KindEntityImage), string(capmodel.KindImagePopup), string(capmodel.KindProduct), string(capmodel.KindLogo):
		return true
	}
	return false
}

// resolveReanchoredPhraseOverlaps repairs visual collisions introduced when
// phrase windows are moved from the planner's provisional schedule onto the
// certified speech-word boundaries. Rust already resolves provisional item
// windows; this second pass is needed because certified reanchoring happens in
// Go after the Rust overlap phase.
func resolveReanchoredPhraseOverlaps(plan *capmodel.OverlayPlan) {
	if plan == nil || len(plan.Items) < 2 {
		return
	}
	items := plan.Items
	// A numeric fact can keep its original timing when its tail merely touches a
	// phrase's certified speech window. Trim that tail to the next phrase start
	// if at least 600 ms remains readable; otherwise normal priority admission
	// keeps the numeric fact and drops the colliding phrase.
	for i := range items {
		if !isNumericFactItem(items[i]) {
			continue
		}
		start, end := items[i].StartUSValue(), items[i].EndUSValue()
		for j := range items {
			if i == j || !isPhraseItem(items[j]) || (items[i].SceneID != "" && items[j].SceneID != items[i].SceneID) {
				continue
			}
			phraseStart, phraseEnd := items[j].StartUSValue(), items[j].EndUSValue()
			if start < phraseStart && phraseStart < end && phraseEnd > start {
				trimmed := phraseStart - start
				if trimmed >= 600_000 {
					setPhraseWindow(&items[i], start, trimmed)
					end = phraseStart
				}
			}
			if phraseStart < start && start < phraseEnd && phraseEnd > end {
				trimmed := start - phraseStart
				if trimmed >= 600_000 {
					setPhraseWindow(&items[j], phraseStart, trimmed)
				}
			}
		}
	}

	priority := func(item capmodel.OverlayItem) (int, float64, int64, string) {
		if isNumericFactItem(item) {
			return 500, 0, item.EndUSValue() - item.StartUSValue(), item.ID
		}
		role, _ := item.Params["editorial_role"].(string)
		rank := 200
		switch strings.TrimSpace(role) {
		case "scene_title":
			rank = 400
		case "chapter_bullet":
			rank = 350
		case "scene_bullet":
			rank = 300
		}
		score, _ := item.Params["priority"].(float64)
		return rank, score, item.EndUSValue() - item.StartUSValue(), item.ID
	}
	indices := make([]int, len(items))
	for i := range indices {
		indices[i] = i
	}
	sort.SliceStable(indices, func(i, j int) bool {
		a, b := items[indices[i]], items[indices[j]]
		ar, as, ad, aid := priority(a)
		br, bs, bd, bid := priority(b)
		if ar != br {
			return ar > br
		}
		if as != bs {
			return as > bs
		}
		if ad != bd {
			return ad > bd
		}
		if a.StartUSValue() != b.StartUSValue() {
			return a.StartUSValue() < b.StartUSValue()
		}
		return aid < bid
	})
	keep := make(map[int]bool, len(items))
	accepted := make([]int, 0, len(items))
	for _, index := range indices {
		item := items[index]
		conflict := false
		for _, priorIndex := range accepted {
			prior := items[priorIndex]
			if isVisualImageItem(item) || isVisualImageItem(prior) {
				continue
			}
			if !isPhraseItem(item) && !isPhraseItem(prior) {
				continue
			}
			if item.StartUSValue() < prior.EndUSValue() && prior.StartUSValue() < item.EndUSValue() {
				conflict = true
				break
			}
		}
		if conflict {
			continue
		}
		keep[index] = true
		accepted = append(accepted, index)
	}
	if len(keep) == len(items) {
		return
	}
	filtered := make([]capmodel.OverlayItem, 0, len(keep))
	for i, item := range items {
		if keep[i] {
			filtered = append(filtered, item)
		}
	}
	plan.Items = filtered
	plan.Intake.RecordStage("reanchored_overlap", len(items), len(filtered))
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
