package overlay

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// A "+"-joined x4-x5 composite parent id exceeds RenderingGen's 128-rune
// group contract; bounding must keep it inside the contract without
// collapsing distinct groups onto one id.
func TestBoundAnimationSubgroupIDKeepsCompositesInsideContract(t *testing.T) {
	short := "overlay-scene-alberto-stasi"
	if got := boundAnimationSubgroupID(short); got != short {
		t.Fatalf("short id changed: %q", got)
	}
	joined := strings.Join([]string{
		"overlay-quattro-volti-alberto-stasi",
		"overlay-quattro-volti-giada-bocellari",
		"overlay-quattro-volti-chiara-poggi",
		"overlay-quattro-volti-andrea-sempio",
	}, "+")
	if utf8.RuneCountInString(joined) <= maxAnimationSubgroupRunes {
		t.Fatalf("fixture no longer exceeds the contract: %d runes", utf8.RuneCountInString(joined))
	}
	bound := boundAnimationSubgroupID(joined)
	if utf8.RuneCountInString(bound) > maxAnimationSubgroupRunes {
		t.Fatalf("bounded id still exceeds contract: %d runes", utf8.RuneCountInString(bound))
	}
	if !strings.HasPrefix(bound, "overlay-quattro-volti-alberto-stasi+") {
		t.Fatalf("bounded id lost its readable head: %q", bound)
	}
	other := boundAnimationSubgroupID(strings.Replace(joined, "sempio", "stasi-x", 1))
	if other == bound {
		t.Fatal("distinct groups collided after bounding")
	}
	if boundAnimationSubgroupID(joined) != bound {
		t.Fatal("bounding is not deterministic")
	}
}
