package overlays

import (
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The short-phrase lane must be able to rotate through ten distinct typewriter
// gestures. A run of short phrases that only ever sees four of them repeats the
// same entrance even when the payload asks for variety, so the certified pool
// is pinned here: every word-count bucket offers all ten short ids, and none of
// the long-only typewriter recipes leaks into the short lane.
var certifiedShortTypewriterIDs = []string{
	"typewriter_glyph_drift_short",
	"typewriter_glyph_square_short",
	"typewriter_impact_push_short",
	"typewriter_rapid_fire_short",
	"typewriter_slow_type_short",
	"typewriter_soft_glide_short",
	"typewriter_staccato_short",
	"typewriter_word_burst_short",
	"typewriter_word_lift_short",
	"typewriter_word_step_short",
}

func TestEveryShortPhraseBucketOffersTenTypewriterGestures(t *testing.T) {
	buckets := ShortPhraseMotionsByWord()
	for words := 1; words <= 5; words++ {
		pool := buckets[strconv.Itoa(words)]
		if len(pool) == 0 {
			t.Fatalf("short-phrase bucket %d is empty", words)
		}
		var got []string
		for _, id := range pool {
			if strings.HasPrefix(id, "typewriter_") {
				got = append(got, id)
			}
		}
		sort.Strings(got)
		want := append([]string(nil), certifiedShortTypewriterIDs...)
		sort.Strings(want)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("bucket %d typewriter ids = %v, want %v", words, got, want)
		}
		if len(got) < 10 {
			t.Fatalf("bucket %d offers %d typewriter gestures, want at least 10", words, len(got))
		}
		for _, id := range got {
			if strings.HasSuffix(id, "_long") {
				t.Fatalf("bucket %d leaks the long-only typewriter recipe %q", words, id)
			}
		}
	}
}
