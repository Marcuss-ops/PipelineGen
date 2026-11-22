package overlays

import (
	"strconv"
	"strings"
	"testing"
)

// The web gestures live in the same registry family as the typewriter
// entrances, so the planner can alternate them with the existing short and
// long phrase pools. Pin every bucket: a web id missing from one word count
// would silently make that sentence length unable to select the new family.
func TestWebPhraseGesturesAreOfferedByBothPhraseLanes(t *testing.T) {
	buckets := ShortPhraseMotionsByWord()
	web := make(map[string]bool, 30)
	for words := 1; words <= 5; words++ {
		pool := buckets[strconv.Itoa(words)]
		found := 0
		for _, id := range pool {
			if strings.HasPrefix(id, "short_phrase_web_") {
				web[id] = true
				found++
			}
		}
		if found != 30 {
			t.Fatalf("short-phrase bucket %d offers %d web gestures, want 30", words, found)
		}
	}
	if len(web) != 30 {
		t.Fatalf("short-phrase buckets expose %d distinct web gestures, want 30", len(web))
	}

	long := make(map[string]bool, len(LongPhraseMotions()))
	for _, id := range LongPhraseMotions() {
		long[id] = true
	}
	for id := range web {
		if !long[id] {
			t.Errorf("long-phrase pool omits web gesture %q", id)
		}
	}
}
