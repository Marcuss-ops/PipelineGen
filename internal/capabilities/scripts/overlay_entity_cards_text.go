package scriptgeneration

import (
	"regexp"
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func sameGroundedDate(left, right string) bool {
	leftParts, rightParts := groundedDateIdentityPattern.FindStringSubmatch(strings.TrimSpace(left)), groundedDateIdentityPattern.FindStringSubmatch(strings.TrimSpace(right))
	if len(leftParts) == 0 || len(rightParts) == 0 {
		return false
	}
	if leftParts[1] != rightParts[1] || !strings.EqualFold(leftParts[2], rightParts[2]) {
		return false
	}
	return leftParts[3] == "" || rightParts[3] == "" || leftParts[3] == rightParts[3]
}

func dateHasYear(text string) bool {
	fields := strings.Fields(strings.TrimSpace(text))
	return len(fields) == 3 && len(fields[2]) == 4
}

func scoreForDate(score float64) float64 {
	if score <= 0 {
		return 0.8
	}
	return score
}

var explicitDatePattern = regexp.MustCompile(`(?i)\b(?:[0-9]{1,2}\s+(?:gennaio|febbraio|marzo|aprile|maggio|giugno|luglio|agosto|settembre|ottobre|novembre|dicembre)\s+[0-9]{4}|[0-9]{4})\b`)
var groundedDateIdentityPattern = regexp.MustCompile(`(?i)^([0-9]{1,2})\s+(gennaio|febbraio|marzo|aprile|maggio|giugno|luglio|agosto|settembre|ottobre|novembre|dicembre)(?:\s+([0-9]{4}))?$`)

func explicitDateSurfaces(text string) []string {
	matches := explicitDatePattern.FindAllString(text, -1)
	seen := make(map[string]struct{}, len(matches))
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		key := strings.ToLower(strings.TrimSpace(match))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, strings.TrimSpace(match))
	}
	return out
}

// dedupeOverlappingGroundedDates collapses partial mentions of one spoken
// date (for example "17 dicembre", "dicembre 2009" and "17 dicembre 2009")
// into the most complete source-grounded surface. The annotations must overlap
// in certified speech timing and share only date tokens, so separate dates in
// the same scene remain distinct.
func dedupeOverlappingGroundedDates(numbers []capabilityoverlay.TimedAnnotation) []capabilityoverlay.TimedAnnotation {
	dateTokens := func(text string) map[string]struct{} {
		months := map[string]struct{}{"gennaio": {}, "febbraio": {}, "marzo": {}, "aprile": {}, "maggio": {}, "giugno": {}, "luglio": {}, "agosto": {}, "settembre": {}, "ottobre": {}, "novembre": {}, "dicembre": {}}
		tokens := strings.Fields(strings.ToLower(strings.TrimSpace(text)))
		if len(tokens) == 0 {
			return nil
		}
		out := make(map[string]struct{}, len(tokens))
		for _, token := range tokens {
			if _, ok := months[token]; ok {
				out[token] = struct{}{}
				continue
			}
			if len(token) != 4 && len(token) > 2 {
				return nil
			}
			for _, r := range token {
				if r < '0' || r > '9' {
					return nil
				}
			}
			out[token] = struct{}{}
		}
		return out
	}
	subset := func(left, right map[string]struct{}) bool {
		if len(left) == 0 || len(left) > len(right) {
			return false
		}
		for token := range left {
			if _, ok := right[token]; !ok {
				return false
			}
		}
		return true
	}
	dropped := make([]bool, len(numbers))
	for i := range numbers {
		if !strings.EqualFold(strings.TrimSpace(numbers[i].Type), "DATE") {
			continue
		}
		left := dateTokens(numbers[i].Text)
		if len(left) == 0 {
			continue
		}
		leftStart, leftEnd := numbers[i].StartUS, numbers[i].StartUS+numbers[i].DurationUS
		if leftStart <= 0 {
			leftStart = numbers[i].StartMs * 1000
		}
		if leftEnd <= leftStart {
			leftEnd = numbers[i].EndMs * 1000
		}
		for j := range numbers {
			if i == j || dropped[j] || !strings.EqualFold(strings.TrimSpace(numbers[j].Type), "DATE") {
				continue
			}
			right := dateTokens(numbers[j].Text)
			if len(right) == 0 || !(subset(left, right) || subset(right, left)) {
				continue
			}
			rightStart, rightEnd := numbers[j].StartUS, numbers[j].StartUS+numbers[j].DurationUS
			if rightStart <= 0 {
				rightStart = numbers[j].StartMs * 1000
			}
			if rightEnd <= rightStart {
				rightEnd = numbers[j].EndMs * 1000
			}
			overlap := min(leftEnd, rightEnd) - max(leftStart, rightStart)
			shorter := min(leftEnd-leftStart, rightEnd-rightStart)
			if overlap <= 0 || shorter <= 0 || overlap*2 < shorter {
				continue
			}
			if len(right) > len(left) || len(right) == len(left) && len(numbers[j].Text) > len(numbers[i].Text) {
				dropped[i] = true
				break
			}
			dropped[j] = true
		}
	}
	out := make([]capabilityoverlay.TimedAnnotation, 0, len(numbers))
	for i, number := range numbers {
		if !dropped[i] {
			out = append(out, number)
		}
	}
	return out
}

func phrasesAtLeastThreeWords(phrases []capabilityoverlay.TimedAnnotation) []capabilityoverlay.TimedAnnotation {
	filtered := make([]capabilityoverlay.TimedAnnotation, 0, len(phrases))
	for _, phrase := range phrases {
		if len(strings.Fields(strings.TrimSpace(phrase.Text))) >= 3 {
			filtered = append(filtered, phrase)
		}
	}
	return filtered
}

func entityImageURL(binding *scriptpkg.EntityImageBinding) string {
	if binding == nil {
		return ""
	}
	if strings.TrimSpace(binding.PreviewURL) != "" {
		return binding.PreviewURL
	}
	return binding.DriveLink
}

// PhraseAnchoringDiagnostic reports one important-phrase candidate considered
// by the overlay planner together with whether it anchored to the certified
// speech timing. It exists purely for observability: a phrase that fails to
// anchor is dropped silently, which makes "the phrases are not taken"
// impossible to diagnose from logs alone.

// normalizeOverlayText lowercases and collapses whitespace so a phrase span
// and a heavy sentence match regardless of casing or spacing.
func normalizeOverlayText(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// applyHeavySentenceBoost raises phrase candidate scores with the Rust
// phrase-impact worker's heavy-sentence importance (goal E2b). A phrase whose
// normalized text is contained in (or contains) a heavy sentence inherits
// max(span score, heavy importance), so the heavy summary directly pilots
// the prominent-motion lane behind PlanInput.HeavyPhrasePriority.
// Scores only rise, never fall: admission stays a restriction, never a
// widening. Matching is source-language text; translated overlays keep their
// annotation scores when nothing matches.
func applyHeavySentenceBoost(in *capabilityoverlay.SceneInput, heavies []scriptpkg.ImportantSentence) {
	if in == nil || len(in.Phrases) == 0 || len(heavies) == 0 {
		return
	}
	type normHeavy struct {
		text       string
		importance float64
	}
	norm := make([]normHeavy, 0, len(heavies))
	for _, h := range heavies {
		t := normalizeOverlayText(h.Text)
		if t == "" || h.Importance <= 0 {
			continue
		}
		norm = append(norm, normHeavy{text: t, importance: h.Importance})
	}
	if len(norm) == 0 {
		return
	}
	for i := range in.Phrases {
		pt := normalizeOverlayText(in.Phrases[i].Text)
		if pt == "" {
			continue
		}
		best := in.Phrases[i].Score
		for _, h := range norm {
			if strings.Contains(h.text, pt) || strings.Contains(pt, h.text) {
				if h.importance > best {
					best = h.importance
				}
			}
		}
		in.Phrases[i].Score = best
	}
}
