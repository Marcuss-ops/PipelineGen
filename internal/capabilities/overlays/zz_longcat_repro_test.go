package overlays

import (
	"fmt"
	"testing"
)

func TestZZLongcatRepro(t *testing.T) {
	pool := []string{
		"apple_expand_from_center", "apple_focus_rise", "apple_hero_statement",
		"apple_scale_settle", "apple_soft_scale", "cinematic_camera_push",
		"glass_morphism_fade", "hero_scale_focus",
		"phrase_apple_clean_01_blur_soft_reveal", "phrase_apple_clean_07_slide_up_soft",
		"phrase_apple_clean_10_slide_from_right_apple", "phrase_apple_clean_25_opacity_soft_reveal",
		"phrase_apple_clean_28_opacity_cinematic", "phrase_apple_clean_29_opacity_hero_settle",
		"phrase_apple_clean_30_opacity_clean_apple", "phrase_apple_clean_03_blur_scale_clean",
		"phrase_apple_clean_04_blur_tracking_drift", "phrase_apple_clean_05_blur_apple_fade",
		"phrase_apple_clean_06_blur_gravity", "phrase_apple_clean_08_slide_up_spring",
	}
	longSeen := map[string]int{}
	for o := 0; o < 20; o++ {
		m := selectLongPhraseMotionLimited("seedA", "run", o, pool, 0)
		longSeen[m]++
	}
	fmt.Println("LONG distinct:", len(longSeen))
	shortSeen := map[string]int{}
	for o := 0; o < 20; o++ {
		m := selectShortPhraseMotionLimited("seedA", "short_phrase_group", o, 12, pool, 0)
		shortSeen[m]++
	}
	fmt.Println("SHORT distinct:", len(shortSeen))
	for m := range shortSeen {
		fmt.Println("  short used:", m)
	}
}
