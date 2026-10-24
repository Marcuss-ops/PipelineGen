package overlays

import (
	"strings"
	"testing"
)

// TestHeavyLaneRotatesFamilies pins the r47 lesson: with a family-sized
// caller pool (e.g. the full catalog expanded from phrase_motion_family)
// every high-priority phrase lands in the heavy lane, so the heavy lane
// itself must rotate visual families. Windowing the raw catalog order filled
// the whole window with the first classic entries and no brush, documentary
// or spatial 3D phrase ever rendered in production.
func TestHeavyLaneRotatesFamilies(t *testing.T) {
	familyOf := func(id string) string {
		switch longPhraseFamilyBucketOf(id) {
		case longFamilyDocumentary:
			return "documentary"
		case longFamily3D:
			return "3d"
		case longFamilyModernApple:
			return "modern_apple"
		case longFamilyClassicApple:
			return "classic_apple"
		default:
			return "unknown"
		}
	}
	pool := CertifiedPhraseMotions()
	seen := map[string]bool{}
	for ordinal := 0; ordinal < 10; ordinal++ {
		motion := selectHeavyPhraseMotionLimited("heavy-family-probe", "run", ordinal, pool, 10)
		if motion == "" {
			t.Fatalf("ordinal %d selected no motion", ordinal)
		}
		seen[familyOf(motion)] = true
	}
	for _, family := range []string{"documentary", "3d", "modern_apple", "classic_apple"} {
		if !seen[family] {
			t.Fatalf("heavy lane omitted %s across 10 ordinals: %v", family, seen)
		}
	}
}

func TestAutomaticHeavyLaneLimitKeepsAllLongFamilies(t *testing.T) {
	seen := map[string]bool{}
	for ordinal := 0; ordinal < 10; ordinal++ {
		motion := selectHeavyPhraseMotionLimited("heavy-auto-family-probe", "run", ordinal, nil, 10)
		if motion == "" {
			t.Fatalf("ordinal %d selected no motion", ordinal)
		}
		switch longPhraseFamilyBucketOf(motion) {
		case longFamilyDocumentary:
			seen["documentary"] = true
		case longFamily3D:
			seen["text_3d"] = true
		case longFamilyModernApple:
			seen["modern_apple"] = true
		case longFamilyClassicApple:
			seen["classic_apple"] = true
		}
	}
	for _, family := range []string{"documentary", "text_3d", "modern_apple", "classic_apple"} {
		if !seen[family] {
			t.Errorf("automatic heavy lane omitted %s under a 10-motion limit: %v", family, seen)
		}
	}
}

// TestHeavyLaneSmallPoolStaysVerbatim pins that small explicit caller pools
// keep their order and are not forced through the family window.
func TestHeavyLaneSmallPoolStaysVerbatim(t *testing.T) {
	pool := []string{"phrase_apple_clean_07_slide_up_soft", "doc_06_red_keyword_punch", "text_3d_yaw_flip_in"}
	first := selectHeavyPhraseMotionLimited("verbatim-probe", "run", 0, pool, 0)
	found := false
	for _, id := range pool {
		if id == first {
			found = true
		}
	}
	if !found {
		t.Fatalf("small explicit pool drew %q from outside the pool", first)
	}
	if strings.TrimSpace(first) == "" {
		t.Fatal("small explicit pool drew an empty motion")
	}
}
