package overlays

import (
	"fmt"
	"strconv"
	"strings"
)

func numberOverlayItem(planID, sceneID string, number TimedAnnotation, animationCounts map[string]int, dateWindowCapUS ...int64) OverlayItem {
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
	if templateID == "TIMELINE_DATE_CARD" {
		capUS := int64(0)
		if len(dateWindowCapUS) > 0 {
			capUS = dateWindowCapUS[0]
		}
		number = minimumDatePresentationDuration(number, capUS)
		params["font_size_px"] = float64(SharedTextFontSizePX + PresentationTextFontIncreasePX)
	} else if templateID == "METRIC_STAT_CARD" {
		params["font_size_px"] = float64(SharedTextFontSizePX + PresentationTextFontIncreasePX)
	}
	return OverlayItem{
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
