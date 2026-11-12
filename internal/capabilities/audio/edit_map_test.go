package audio

import (
	"errors"
	"strings"
	"testing"
)

// remapTestWord builds one word boundary for a remap test.
func remapTestWord(index int, text string, start, end int64) SpeechWordTiming {
	return SpeechWordTiming{Index: index, Text: text, StartUS: start, EndUS: end}
}

func TestRemapTiming_NoRemovalIdentity(t *testing.T) {
	words := []SpeechWordTiming{
		remapTestWord(0, "Ciao", 1_000_000, 1_500_000),
		remapTestWord(1, "bella", 1_500_000, 2_000_000),
	}
	got, err := RemapSpeechTiming(words, nil)
	if err != nil {
		t.Fatalf("empty edit map must be identity: %v", err)
	}
	if len(got) != len(words) {
		t.Fatalf("identity must preserve word count: got %d want %d", len(got), len(words))
	}
	for i := range words {
		if got[i] != words[i] {
			t.Fatalf("word %d changed under identity: got %+v want %+v", i, got[i], words[i])
		}
	}
	// The result must be a copy, not an alias of the input slice.
	words[0].StartUS = 99
	if got[0].StartUS == 99 {
		t.Fatal("identity must return a copy, not an alias of the input slice")
	}
}

func TestRemapTiming_RemovedLeadingSilence(t *testing.T) {
	edits := []AudioEdit{{SourceStartUS: 0, SourceEndUS: 1_000_000, OutputStartUS: 0, OutputEndUS: 0}}
	words := []SpeechWordTiming{
		remapTestWord(0, "Ciao", 1_200_000, 1_800_000),
		remapTestWord(1, "bella", 1_800_000, 2_400_000),
	}
	got, err := RemapSpeechTiming(words, edits)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].StartUS != 200_000 || got[0].EndUS != 800_000 {
		t.Fatalf("leading silence must shift all words left by 1s: got [%d,%d)", got[0].StartUS, got[0].EndUS)
	}
	if got[1].StartUS != 800_000 || got[1].EndUS != 1_400_000 {
		t.Fatalf("word 1 shifted wrong: got [%d,%d)", got[1].StartUS, got[1].EndUS)
	}
}

func TestRemapTiming_RemovedMiddleSilence(t *testing.T) {
	edits := []AudioEdit{{SourceStartUS: 2_000_000, SourceEndUS: 3_000_000, OutputStartUS: 2_000_000, OutputEndUS: 2_000_000}}
	words := []SpeechWordTiming{
		remapTestWord(0, "prima", 1_000_000, 1_500_000),
		remapTestWord(1, "dopo", 3_200_000, 4_000_000),
	}
	got, err := RemapSpeechTiming(words, edits)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].StartUS != 1_000_000 || got[0].EndUS != 1_500_000 {
		t.Fatalf("words before the removed interval must be unchanged: got [%d,%d)", got[0].StartUS, got[0].EndUS)
	}
	if got[1].StartUS != 2_200_000 || got[1].EndUS != 3_000_000 {
		t.Fatalf("words after the removed interval must shift left by 1s: got [%d,%d)", got[1].StartUS, got[1].EndUS)
	}
}

func TestRemapTiming_MultipleRemovedIntervals(t *testing.T) {
	edits := []AudioEdit{
		{SourceStartUS: 1_000_000, SourceEndUS: 1_500_000, OutputStartUS: 1_000_000, OutputEndUS: 1_000_000},
		// The second cut lands at 2.5s: source start 3.0s minus the 0.5s
		// already removed, and its end collapses to the same cut position.
		{SourceStartUS: 3_000_000, SourceEndUS: 4_000_000, OutputStartUS: 2_500_000, OutputEndUS: 2_500_000},
	}
	words := []SpeechWordTiming{
		remapTestWord(0, "inizio", 0, 800_000),
		remapTestWord(1, "medio", 2_000_000, 2_500_000),
		remapTestWord(2, "fine", 4_500_000, 5_000_000),
	}
	got, err := RemapSpeechTiming(words, edits)
	if err != nil {
		t.Fatal(err)
	}
	// Before every edit: unchanged.
	if got[0].StartUS != 0 || got[0].EndUS != 800_000 {
		t.Fatalf("word before all edits must be unchanged: got [%d,%d)", got[0].StartUS, got[0].EndUS)
	}
	// Between the two edits: shifted only by the first edit (0.5s).
	if got[1].StartUS != 1_500_000 || got[1].EndUS != 2_000_000 {
		t.Fatalf("word between edits must shift by 0.5s: got [%d,%d)", got[1].StartUS, got[1].EndUS)
	}
	// After both edits: shifted by the cumulative 1.5s.
	if got[2].StartUS != 3_000_000 || got[2].EndUS != 3_500_000 {
		t.Fatalf("word after all edits must shift by 1.5s: got [%d,%d)", got[2].StartUS, got[2].EndUS)
	}
}

func TestRemapTiming_DurationMatchesFinalAudio(t *testing.T) {
	// Original audio 10s; a 1s silence at [3s,4s) removed → final 9s.
	edits := []AudioEdit{{SourceStartUS: 3_000_000, SourceEndUS: 4_000_000, OutputStartUS: 3_000_000, OutputEndUS: 3_000_000}}
	words := []SpeechWordTiming{
		remapTestWord(0, "ultima", 8_500_000, 9_500_000),
	}
	remapped, err := RemapSpeechTiming(words, edits)
	if err != nil {
		t.Fatal(err)
	}
	// The raw word ends at 9.5s — past the 9s final duration. Only the
	// remapped word (ending 8.5s) validates against the final audio.
	if _, rawErr := BuildSpeechTimingArtifact("edge_tts", "it", "voice",
		strings.Repeat("a", 64), strings.Repeat("b", 64), 9_000_000, words); rawErr == nil {
		t.Fatal("raw (un-remapped) timing must NOT validate against the final audio duration")
	}
	finalArtifact, err := BuildSpeechTimingArtifact("edge_tts", "it", "voice",
		strings.Repeat("a", 64), strings.Repeat("b", 64), 9_000_000, remapped)
	if err != nil {
		t.Fatalf("remapped timing must validate against the final audio duration: %v", err)
	}
	if finalArtifact.Words[0].StartUS != 7_500_000 || finalArtifact.Words[0].EndUS != 8_500_000 {
		t.Fatalf("remapped word = [%d,%d), want [7500000,8500000)", finalArtifact.Words[0].StartUS, finalArtifact.Words[0].EndUS)
	}
}

func TestRemapTiming_WordStraddlingACutLandsOnSurvivingTimeline(t *testing.T) {
	edits := []AudioEdit{{SourceStartUS: 2_000_000, SourceEndUS: 3_000_000, OutputStartUS: 2_000_000, OutputEndUS: 2_000_000}}
	// Start inside the removed interval (clamped to the cut), end after it.
	words := []SpeechWordTiming{remapTestWord(0, "a cavallo", 2_500_000, 3_500_000)}
	got, err := RemapSpeechTiming(words, edits)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].StartUS != 2_000_000 || got[0].EndUS != 2_500_000 {
		t.Fatalf("straddling word = [%d,%d), want [2000000,2500000)", got[0].StartUS, got[0].EndUS)
	}
}

func TestRemapTiming_WordInsideRemovedSilenceCollapses(t *testing.T) {
	edits := []AudioEdit{{SourceStartUS: 2_000_000, SourceEndUS: 3_000_000, OutputStartUS: 2_000_000, OutputEndUS: 2_000_000}}
	words := []SpeechWordTiming{remapTestWord(0, "silenzio", 2_200_000, 2_800_000)}
	got, err := RemapSpeechTiming(words, edits)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].StartUS != 2_000_000 || got[0].EndUS != 2_000_000 {
		t.Fatalf("word inside removed silence must collapse to the cut: got [%d,%d)", got[0].StartUS, got[0].EndUS)
	}
}

// Full-pipeline regression for the audit's 73.3s -> 64.7s cleanup: three
// cuts totalling 8.6 seconds, with words before, between and after the cuts.
// A flat 8.6s shift is wrong; every word must follow its actual cut position.
func TestRemapTiming_Garlasco73_3To64_7(t *testing.T) {
	const originalUS int64 = 73_300_000
	const finalUS int64 = 64_700_000
	edits := []AudioEdit{
		{SourceStartUS: 5_000_000, SourceEndUS: 7_300_000, OutputStartUS: 5_000_000, OutputEndUS: 5_000_000},
		{SourceStartUS: 20_000_000, SourceEndUS: 22_100_000, OutputStartUS: 17_700_000, OutputEndUS: 17_700_000},
		{SourceStartUS: 50_000_000, SourceEndUS: 54_200_000, OutputStartUS: 45_600_000, OutputEndUS: 45_600_000},
	}
	var removed int64
	for _, edit := range edits {
		removed += edit.SourceEndUS - edit.SourceStartUS
	}
	if removed != originalUS-finalUS {
		t.Fatalf("removed %d us, want %d us", removed, originalUS-finalUS)
	}
	words := []SpeechWordTiming{
		remapTestWord(0, "prima", 10_000_000, 10_500_000),
		remapTestWord(1, "durante", 30_000_000, 30_500_000),
		remapTestWord(2, "fine", 73_000_000, 73_300_000),
	}
	got, err := RemapSpeechTiming(words, edits)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].StartUS != 7_700_000 || got[0].EndUS != 8_200_000 {
		t.Fatalf("word before the second/third cut = [%d,%d), want [7700000,8200000)", got[0].StartUS, got[0].EndUS)
	}
	if got[1].StartUS != 25_600_000 || got[1].EndUS != 26_100_000 {
		t.Fatalf("word after the third cut = [%d,%d), want [25600000,26100000)", got[1].StartUS, got[1].EndUS)
	}
	if got[2].StartUS != 64_400_000 || got[2].EndUS != 64_700_000 {
		t.Fatalf("final word = [%d,%d), want [64400000,64700000) (flat 8.6s shift would be wrong)", got[2].StartUS, got[2].EndUS)
	}
	if _, rawErr := BuildSpeechTimingArtifact("edge_tts", "it", "voice",
		strings.Repeat("a", 64), strings.Repeat("b", 64), finalUS, words); rawErr == nil {
		t.Fatal("raw un-remapped timing must not validate against the 64.7s final audio")
	}
	artifact, err := BuildSpeechTimingArtifact("edge_tts", "it", "voice",
		strings.Repeat("a", 64), strings.Repeat("b", 64), finalUS, got)
	if err != nil {
		t.Fatalf("remapped timing must validate against the 64.7s final audio: %v", err)
	}
	if artifact.Words[2].EndUS != finalUS {
		t.Fatalf("final artifact word end = %d, want %d", artifact.Words[2].EndUS, finalUS)
	}
}

func TestRemapTiming_RejectsInvertedSourceInterval(t *testing.T) {
	edits := []AudioEdit{{SourceStartUS: 2_000_000, SourceEndUS: 1_000_000, OutputStartUS: 2_000_000, OutputEndUS: 2_000_000}}
	_, err := RemapSpeechTiming(nil, edits)
	if !errors.Is(err, ErrInvalidEditRange) {
		t.Fatalf("inverted source interval must fail closed with ErrInvalidEditRange, got %v", err)
	}
}

func TestRemapTiming_RejectsOverlappingEdits(t *testing.T) {
	edits := []AudioEdit{
		{SourceStartUS: 1_000_000, SourceEndUS: 2_000_000, OutputStartUS: 1_000_000, OutputEndUS: 1_000_000},
		{SourceStartUS: 1_500_000, SourceEndUS: 2_500_000, OutputStartUS: 1_500_000, OutputEndUS: 2_000_000},
	}
	_, err := RemapSpeechTiming(nil, edits)
	if !errors.Is(err, ErrOverlappingEdits) {
		t.Fatalf("overlapping edits must fail closed with ErrOverlappingEdits, got %v", err)
	}
}

func TestRemapTiming_RejectsInconsistentEditMap(t *testing.T) {
	// Output anchor (1.5s) disagrees with the source-derived position (1s).
	edits := []AudioEdit{{SourceStartUS: 1_000_000, SourceEndUS: 2_000_000, OutputStartUS: 1_500_000, OutputEndUS: 1_500_000}}
	_, err := RemapSpeechTiming(nil, edits)
	if !errors.Is(err, ErrInconsistentEditMap) {
		t.Fatalf("self-inconsistent edit map must fail closed with ErrInconsistentEditMap, got %v", err)
	}
}
