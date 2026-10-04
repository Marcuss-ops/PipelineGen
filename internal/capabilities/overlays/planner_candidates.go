package overlays

import (
	"sort"
	"strings"
	"unicode"
)

// clampImageWindow enforces the shared hard editorial ceiling for image-like overlays.
func clampImageWindow(candidate ImageCandidate) ImageCandidate {
	if candidate.StartUS > 0 || candidate.DurationUS > 0 {
		if candidate.DurationUS > MaxImageOverlayDurationMS*1000 {
			candidate.DurationUS = MaxImageOverlayDurationMS * 1000
			candidate.EndMs = (candidate.StartUS + candidate.DurationUS + 999) / 1000
		}
		return candidate
	}
	if candidate.EndMs-candidate.StartMs > MaxImageOverlayDurationMS {
		candidate.EndMs = candidate.StartMs + MaxImageOverlayDurationMS
	}
	return candidate
}

func rankedValid(in []TimedAnnotation, maxWords int) []TimedAnnotation {
	valid := make([]TimedAnnotation, 0, len(in))
	for _, candidate := range in {
		if strings.TrimSpace(candidate.Text) == "" || candidate.StartMs < 0 || candidate.EndMs <= candidate.StartMs {
			continue
		}
		if maxWords > 0 && len(strings.Fields(candidate.Text)) > maxWords {
			continue
		}
		candidate.Text = strings.TrimSpace(candidate.Text)
		valid = append(valid, candidate)
	}
	sort.SliceStable(valid, func(i, j int) bool { return valid[i].Score > valid[j].Score })
	seen := make(map[string]struct{}, len(valid))
	out := make([]TimedAnnotation, 0, len(valid))
	for _, candidate := range valid {
		key := strings.ToLower(strings.Join(strings.Fields(candidate.Text), " "))
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

func rankedPhraseValid(in []TimedAnnotation, maxWords int) []TimedAnnotation {
	valid := rankedValid(in, maxWords)
	sort.SliceStable(valid, func(i, j int) bool {
		wi, wj := len(strings.Fields(valid[i].Text)), len(strings.Fields(valid[j].Text))
		if wi != wj {
			return wi > wj
		}
		return valid[i].Score > valid[j].Score
	})
	return valid
}

func rankedImages(in []ImageCandidate) []ImageCandidate {
	valid := make([]ImageCandidate, 0, len(in))
	for _, candidate := range in {
		if strings.TrimSpace(candidate.AssetID) == "" || candidate.StartMs < 0 || candidate.EndMs <= candidate.StartMs {
			continue
		}
		valid = append(valid, candidate)
	}
	sort.SliceStable(valid, func(i, j int) bool { return valid[i].Score > valid[j].Score })
	return valid
}

func itemID(sceneID, kind, value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(sceneID + "-" + kind + "-" + value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else if b.Len() > 0 {
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
