package clipresolver

import (
	"velox/go-master/pkg/models"
)

// ApplyNegativeFilter filters out clips containing negative terms
func ApplyNegativeFilter(clips []ClipScore, avoidTerms map[string]bool) []ClipScore {
	filtered := make([]ClipScore, 0)
	for _, cs := range clips {
		if containsNegativeTerm(cs.Clip, avoidTerms) {
			cs.RejectReason = "Contains negative term"
			continue
		}
		filtered = append(filtered, cs)
	}
	return filtered
}

func containsNegativeTerm(clip *models.MediaAsset, avoidTerms map[string]bool) bool {
	// Check name
	for term := range avoidTerms {
		if containsIgnoreCase(clip.Name, term) {
			return true
		}
	}
	// Check tags
	for _, tag := range clip.Tags {
		for term := range avoidTerms {
			if containsIgnoreCase(tag, term) {
				return true
			}
		}
	}
	return false
}

func containsIgnoreCase(s, substr string) bool {
	if len(s) < len(substr) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if stringsEqualIgnoreCase(s[i:i+len(substr)], substr) {
			return true
		}
	}
	return false
}

func stringsEqualIgnoreCase(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if toLower(a[i]) != toLower(b[i]) {
			return false
		}
	}
	return true
}

func toLower(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}
