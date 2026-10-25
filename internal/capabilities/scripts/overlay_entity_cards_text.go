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
