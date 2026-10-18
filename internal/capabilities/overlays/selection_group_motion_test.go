package overlays

import "testing"

// TestSelectImageGroupMotionIsCertifiedDeterministicAndNoRepeat pins the
// group-motion contract: the choice always comes from the certified
// image.group.count_N candidates, is reproducible for the same
// (seed, video, group), never repeats the previous group's motion when an
// alternative exists, and varies with the seed.
func TestSelectImageGroupMotionIsCertifiedDeterministicAndNoRepeat(t *testing.T) {
	for count := 2; count <= 5; count++ {
		candidates := AnimationCandidates("image.group.count_" + string(rune('0'+count)))
		if len(candidates) == 0 {
			t.Fatalf("count %d: no certified group motion candidates", count)
		}
		inPool := make(map[string]bool, len(candidates))
		for _, id := range candidates {
			inPool[id] = true
		}

		first := SelectImageGroupMotion("seed-a", "video-1", "group-1", count, "")
		if !inPool[first] {
			t.Fatalf("count %d: selected %q is not a certified candidate", count, first)
		}
		if again := SelectImageGroupMotion("seed-a", "video-1", "group-1", count, ""); again != first {
			t.Fatalf("count %d: same inputs gave %q then %q", count, first, again)
		}

		if len(candidates) > 1 {
			if next := SelectImageGroupMotion("seed-a", "video-1", "group-1", count, first); next == first || !inPool[next] {
				t.Fatalf("count %d: no-repeat violated, previous %q next %q", count, first, next)
			}
		}

		varied := false
		for seed := 0; seed < 64 && !varied; seed++ {
			if SelectImageGroupMotion("seed-"+string(rune('a'+seed%26))+string(rune('a'+seed/26)), "video-1", "group-1", count, "") != first {
				varied = true
			}
		}
		if len(candidates) > 1 && !varied {
			t.Fatalf("count %d: seed never changed the selection among %d candidates", count, len(candidates))
		}
	}

	if got := SelectImageGroupMotion("seed-a", "video-1", "group-1", 6, ""); got != "" {
		t.Fatalf("count 6 has no certified candidates, got %q", got)
	}
}
