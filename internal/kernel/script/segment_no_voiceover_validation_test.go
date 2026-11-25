package script

import (
	"strings"
	"testing"
)

func TestValidateSegmentNoVoiceoverRejectsTargetWords(t *testing.T) {
	details := validateSegmentNoVoiceover(ScriptSegment{
		Kind:        "clip",
		NoVoiceover: true,
		ClipIDs:     []string{"clip-1"},
		TargetWords: 1,
	}, 0, "item")
	if len(details) == 0 {
		t.Fatal("a no_voiceover cutaway with target_words > 0 must fail preflight")
	}
	if !strings.Contains(details[0], "target_words") {
		t.Fatalf("details = %v, want target_words named", details)
	}
}

func TestValidateSegmentNoVoiceoverAcceptsSilentCutaway(t *testing.T) {
	if details := validateSegmentNoVoiceover(ScriptSegment{
		Kind:        "clip",
		NoVoiceover: true,
		ClipIDs:     []string{"clip-1"},
	}, 0, "item"); len(details) != 0 {
		t.Fatalf("valid silent cutaway rejected: %v", details)
	}
}
