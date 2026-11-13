package overlays

import (
	"fmt"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"strconv"
	"strings"
)

// dedupeCoTimedDateAnnotations removes a partial date annotation when the
// same spoken moment also has a fuller surface. The NLP extractor emits both
// "3 giugno 2007" and "3 giugno"/"giugno 2007" at the same certified start;
// keeping all of them creates duplicate cards that collide and consume the
// date lane. Dates at different speech positions remain separate cards.
func dedupeCoTimedDateAnnotations(in []TimedAnnotation) []TimedAnnotation {
	out := make([]TimedAnnotation, 0, len(in))
	for _, candidate := range in {
		if !strings.EqualFold(strings.TrimSpace(candidate.Type), "DATE") {
			out = append(out, candidate)
			continue
		}
		start := dateStartUS(candidate)
		duplicate := -1
		for i := range out {
			prior := out[i]
			if !strings.EqualFold(strings.TrimSpace(prior.Type), "DATE") {
				continue
			}
			if dateStartUS(prior) != start || !dateSurfacesOverlap(candidate.Text, prior.Text) {
				continue
			}
			duplicate = i
			break
		}
		if duplicate < 0 {
			out = append(out, candidate)
			continue
		}
		// Keep the more informative surface. Stable tie-breaking preserves the
		// rank order supplied by the planner.
		if dateSurfaceFields(candidate.Text) > dateSurfaceFields(out[duplicate].Text) {
			out[duplicate] = candidate
		}
		winner := out[duplicate]
		// A single full date can supersede more than one partial annotation at
		// the same start ("3 giugno" and "giugno 2007"); remove all of them.
		kept := make([]TimedAnnotation, 0, len(out))
		for i := range out {
			prior := out[i]
			if i != duplicate && strings.EqualFold(strings.TrimSpace(prior.Type), "DATE") &&
				dateStartUS(prior) == start && dateSurfacesOverlap(prior.Text, winner.Text) {
				continue
			}
			kept = append(kept, prior)
		}
		out = kept
	}
	return out
}

func dateStartUS(date TimedAnnotation) int64 {
	if date.StartUS > 0 {
		return date.StartUS
	}
	return date.StartMs * 1000
}

// dateSurfacesOverlap reports whether two surfaces describe the same date at
// the same spoken moment. A partial surface ("3 giugno") is a token subset of
// the full one ("3 giugno 2007"); identical surfaces are duplicates too. The
// year alone is deliberately not enough: "3 giugno 2007" and "4 giugno 2007"
// share a year but are different dates.
func dateSurfacesOverlap(a, b string) bool {
	af, bf := dateSurfaceTokens(a), dateSurfaceTokens(b)
	if len(af) == 0 || len(bf) == 0 {
		return false
	}
	return tokenSubset(af, bf) || tokenSubset(bf, af)
}

func dateSurfaceTokens(text string) map[string]struct{} {
	tokens := make(map[string]struct{})
	for _, field := range strings.Fields(strings.ToLower(text)) {
		field = strings.Trim(field, ".,;:!?()[]{}'\"")
		if field != "" {
			tokens[field] = struct{}{}
		}
	}
	return tokens
}

func tokenSubset(a, b map[string]struct{}) bool {
	if len(a) > len(b) {
		return false
	}
	for token := range a {
		if _, ok := b[token]; !ok {
			return false
		}
	}
	return true
}

func dateSurfaceFields(text string) int { return len(dateSurfaceTokens(text)) }

// dateSceneEndCap bounds a timeline date/time card to the owning scene. The
// producer sets SceneInput.EndUS; a card that cannot keep at least two
// readable seconds before the scene ends is dropped instead of bleeding into
// the next scene. Other number cards (metrics) keep their independent lane.
func dateSceneEndCap(date TimedAnnotation, sceneEndUS int64) (int64, bool) {
	if !isTimelineDateCardType(date.Type) {
		return 0, true
	}
	if sceneEndUS <= 0 {
		return 0, true
	}
	start := dateStartUS(date)
	if start >= sceneEndUS {
		return 0, false
	}
	remaining := sceneEndUS - start
	if remaining < minimumDateCardReadableUS {
		return 0, false
	}
	return remaining, true
}

const minimumDateCardReadableUS int64 = 2_000_000

func isTimelineDateCardType(entityType string) bool {
	return NumberPresentationTemplateForEntityType(entityType) == "TIMELINE_DATE_CARD"
}

func numberOverlayItem(planID, sceneID string, number TimedAnnotation, animationCounts map[string]int, dateWindowCapUS ...int64) capmodel.OverlayItem {
	id := itemID(sceneID, "number", number.Text)
	templateID, motionID := "NUMBER", SelectTextMotion(planID, sceneID, id)
	if numberType := strings.TrimSpace(number.Type); numberType != "" {
		limit := animationCounts["metric_stat"]
		if NumberPresentationTemplateForEntityType(numberType) == "TIMELINE_DATE_CARD" {
			limit = animationCounts["timeline_date"]
		}
		if presentationTemplate, presentationMotion := NumberPresentationForEntityTypeWithLimit(planID, sceneID, id, numberType, limit); presentationTemplate != "" {
			templateID, motionID = presentationTemplate, presentationMotion
		}
	}
	params := map[string]any{"position": "center", "style": "stat", "priority": number.Score}
	switch templateID {
	case "TIMELINE_DATE_CARD":
		capUS := int64(0)
		if len(dateWindowCapUS) > 0 {
			capUS = dateWindowCapUS[0]
		}
		number = minimumDatePresentationDuration(number, capUS)
		params["font_size_px"] = float64(SharedTextFontSizePX + PresentationTextFontIncreasePX)
	case "METRIC_STAT_CARD":
		params["font_size_px"] = float64(SharedTextFontSizePX + PresentationTextFontIncreasePX)
	}
	return capmodel.OverlayItem{
		ID: id, SceneID: sceneID, PresetID: selectWordPreset(planID, sceneID, id),
		MotionID: motionID, Kind: "number", TemplateID: templateID, Text: normalizeItalianNumberWords(number.Text),
		StartMs: number.StartMs, EndMs: number.EndMs, StartUS: number.StartUS, DurationUS: number.DurationUS,
		Params: params,
	}
}

// normalizeItalianNumberWords converts Italian cardinal number words in a
// number overlay to digits while leaving surrounding date/unit words intact
// (for example, "tredici agosto" becomes "13 agosto"). Spoken narration is
// never changed; this is only the visual callout label.
func normalizeItalianNumberWords(text string) string {
	fields := strings.Fields(text)
	for i, field := range fields {
		start, end := 0, len(field)
		for start < end && !isItalianNumberLetter(rune(field[start])) {
			start++
		}
		for end > start && !isItalianNumberLetter(rune(field[end-1])) {
			end--
		}
		if start == end {
			continue
		}
		if value, ok := italianCardinal(strings.ToLower(field[start:end])); ok {
			fields[i] = field[:start] + value + field[end:]
		}
	}
	return strings.Join(fields, " ")
}

func isItalianNumberLetter(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == 'à' || r == 'è' || r == 'é' || r == 'ì' || r == 'ò' || r == 'ù'
}

func italianCardinal(word string) (string, bool) {
	value, ok := italianCardinalValue(word)
	if !ok {
		return "", false
	}
	return fmt.Sprint(value), true
}

func italianCardinalValue(word string) (int, bool) {
	units := map[string]int{"zero": 0, "uno": 1, "una": 1, "due": 2, "tre": 3, "tré": 3, "quattro": 4, "cinque": 5, "sei": 6, "sette": 7, "otto": 8, "nove": 9, "dieci": 10, "undici": 11, "dodici": 12, "tredici": 13, "quattordici": 14, "quindici": 15, "sedici": 16, "diciassette": 17, "diciotto": 18, "diciannove": 19}
	if value, ok := units[word]; ok {
		return value, true
	}
	tens := []struct {
		word  string
		value int
	}{{"novanta", 90}, {"ottanta", 80}, {"settanta", 70}, {"sessanta", 60}, {"cinquanta", 50}, {"quaranta", 40}, {"trenta", 30}, {"venti", 20}}
	for _, ten := range tens {
		if word == ten.word {
			return ten.value, true
		}
		prefix := ten.word[:len(ten.word)-1]
		for _, base := range []string{ten.word, prefix} {
			if !strings.HasPrefix(word, base) {
				continue
			}
			suffix := strings.TrimPrefix(word, base)
			if suffix == "uno" || suffix == "otto" {
				return ten.value + units[suffix], true
			}
			if unit, ok := units[suffix]; ok && unit > 0 && unit < 10 {
				return ten.value + unit, true
			}
		}
	}
	if word == "cento" {
		return 100, true
	}
	if at := strings.Index(word, "cento"); at >= 0 {
		multiplier := 1
		if at > 0 {
			parsed, ok := italianCardinalValue(word[:at])
			if !ok || parsed < 2 || parsed > 9 {
				return 0, false
			}
			multiplier = parsed
		}
		remainder := word[at+len("cento"):]
		if remainder == "" {
			return multiplier * 100, true
		}
		if rest, ok := italianCardinalValue(remainder); ok && rest < 100 {
			return multiplier*100 + rest, true
		}
	}
	if word == "mille" {
		return 1000, true
	}
	if at := strings.Index(word, "mila"); at >= 0 {
		prefix, ok := italianCardinalValue(word[:at])
		if !ok || prefix < 2 || prefix > 999 {
			return 0, false
		}
		remainder := word[at+len("mila"):]
		if remainder == "" {
			return prefix * 1000, true
		}
		if rest, ok := italianCardinalValue(remainder); ok && rest < 1000 {
			return prefix*1000 + rest, true
		}
	}
	if strings.HasPrefix(word, "mille") {
		remainder := strings.TrimPrefix(word, "mille")
		if rest, ok := italianCardinalValue(remainder); ok && rest < 1000 {
			return 1000 + rest, true
		}
	}
	if value, err := strconv.Atoi(word); err == nil {
		return value, true
	}
	return 0, false
}

// minimumDatePresentationDuration preserves the word-aligned start while
// keeping a Date card on screen for at least five seconds.
func minimumDatePresentationDuration(date TimedAnnotation, maxDurationUS int64) TimedAnnotation {
	const minimumDurationUS int64 = 5_000_000
	if date.DurationUS <= 0 {
		date.StartUS = date.StartMs * 1000
		date.DurationUS = (date.EndMs - date.StartMs) * 1000
	}
	target := minimumDurationUS
	const minimumReadableDateUS int64 = 2_000_000
	if maxDurationUS >= minimumReadableDateUS && maxDurationUS < target {
		target = maxDurationUS
	}
	// A timing cap is a hard visual boundary. Merely using it as the minimum
	// target leaves longer source annotations unchanged, which lets a date
	// remain underneath a later image overlay.
	if maxDurationUS >= minimumReadableDateUS && date.DurationUS > maxDurationUS {
		date.DurationUS = maxDurationUS
	}
	if date.DurationUS < target {
		date.DurationUS = target
	}
	date.StartMs = date.StartUS / 1000
	date.EndMs = (date.StartUS + date.DurationUS + 999) / 1000
	return date
}

// dateCardWindowCap keeps a run of date cards readable while ending each card
// shortly before the next spoken date in the same scene. A single date keeps
// the established five-second presentation window.
func dateCardWindowCap(date TimedAnnotation, all []TimedAnnotation) int64 {
	if !strings.EqualFold(strings.TrimSpace(date.Type), "DATE") {
		return 0
	}
	start := date.StartUS
	if start <= 0 {
		start = date.StartMs * 1000
	}
	const guardUS int64 = 100_000
	nearestGap := int64(0)
	for _, next := range all {
		if !strings.EqualFold(strings.TrimSpace(next.Type), "DATE") {
			continue
		}
		nextStart := next.StartUS
		if nextStart <= 0 {
			nextStart = next.StartMs * 1000
		}
		gap := nextStart - start
		if gap > 0 && gap < 5_000_000 && (nearestGap == 0 || gap < nearestGap) {
			nearestGap = gap
		}
	}
	if nearestGap > 0 {
		capUS := nearestGap - guardUS
		if capUS >= 2_000_000 {
			return capUS
		}
	}
	return 0
}

// dateCardWindowCapBeforeImage prevents a centered date card from being hidden
// by a scene image that begins while the card is still visible. Keep the card
// word-anchored and shorten only its visible tail; two seconds remains the
// minimum readable window enforced by minimumDatePresentationDuration.
func dateCardWindowCapBeforeImage(date TimedAnnotation, images []ImageCandidate, currentCapUS int64) int64 {
	if !strings.EqualFold(strings.TrimSpace(date.Type), "DATE") {
		return currentCapUS
	}
	start := date.StartUS
	if start <= 0 {
		start = date.StartMs * 1000
	}
	const guardUS int64 = 100_000
	const minimumReadableUS int64 = 2_000_000
	for _, image := range images {
		imageStart := image.StartUS
		if imageStart <= 0 {
			imageStart = image.StartMs * 1000
		}
		gap := imageStart - start
		if gap <= 0 || gap >= 5_000_000 {
			continue
		}
		capUS := gap - guardUS
		if capUS < minimumReadableUS {
			continue
		}
		if currentCapUS == 0 || capUS < currentCapUS {
			currentCapUS = capUS
		}
	}
	return currentCapUS
}
