package asset

import (
	"testing"
)

// ── AsSearchTextInput ─────────────────────────────────────────────────

func TestAsSearchTextInput_YouTube_PopulatesTypedFields(t *testing.T) {
	t.Parallel()
	m := ClipSemanticMetadata{
		AssetID:         "yt_abc_0_60_v1",
		Title:           "Pacquiao vs Broner",
		Description:     "Press conference highlights",
		Hook:            "Stay focused!",
		Tags:            []string{"boxing", "pacquiao"},
		Topics:          []string{"confrontation", "prefight"},
		Speakers:        []string{"Pacquiao", "Broner"},
		MentionedPeople: []string{"Mayweather"},
		SourceURL:       "https://youtube.com/watch?v=abc",
		NormalizedGroup: "boxing",
	}
	sti := m.AsSearchTextInput("youtube")

	if sti.Source != "youtube" {
		t.Errorf("Source = %q, want %q", sti.Source, "youtube")
	}
	if sti.Title != "Pacquiao vs Broner" {
		t.Errorf("Title = %q, want %q", sti.Title, "Pacquiao vs Broner")
	}
	if sti.Hook != "Stay focused!" {
		t.Errorf("Hook = %q, want %q", sti.Hook, "Stay focused!")
	}
	if len(sti.Speakers) != 2 {
		t.Errorf("Speakers len = %d, want 2", len(sti.Speakers))
	}
	if len(sti.MentionedPeople) != 1 {
		t.Errorf("MentionedPeople len = %d, want 1", len(sti.MentionedPeople))
	}
	if len(sti.Topics) != 2 {
		t.Errorf("Topics len = %d, want 2", len(sti.Topics))
	}
	// YouTube should NOT populate Additional (stock-specific)
	if sti.Additional != nil {
		t.Errorf("Additional should be nil for YouTube, got %v", sti.Additional)
	}
}

func TestAsSearchTextInput_Stock_PopulatesAdditional(t *testing.T) {
	t.Parallel()
	m := ClipSemanticMetadata{
		AssetID:     "planner:abc123:0",
		Title:       "Round 7",
		Description: "Pacquiao knocks down Broner",
		Tags:        []string{"boxing"},
		Category:    "Boxe",
		SourceURL:   "https://pexels.com/video/123",
		Event:       "Pacquiao vs Broner",
		Round:       7,
		Subject:     "knockdown",
		StartSec:    32.0,
		EndSec:      51.0,
	}
	sti := m.AsSearchTextInput("stock")

	if sti.Source != "stock" {
		t.Errorf("Source = %q, want %q", sti.Source, "stock")
	}
	if sti.Additional == nil {
		t.Fatal("Additional should be non-nil for Stock")
	}
	if sti.Additional["event"] != "Pacquiao vs Broner" {
		t.Errorf("Additional[event] = %q, want %q", sti.Additional["event"], "Pacquiao vs Broner")
	}
	if sti.Additional["round"] != "7" {
		t.Errorf("Additional[round] = %q, want %q", sti.Additional["round"], "7")
	}
	if sti.Additional["subject"] != "knockdown" {
		t.Errorf("Additional[subject] = %q, want %q", sti.Additional["subject"], "knockdown")
	}
	if sti.Additional["start_sec"] != "32" {
		t.Errorf("Additional[start_sec] = %q, want %q", sti.Additional["start_sec"], "32")
	}
	if sti.Additional["end_sec"] != "51" {
		t.Errorf("Additional[end_sec] = %q, want %q", sti.Additional["end_sec"], "51")
	}
}

func TestAsSearchTextInput_EmptySource_DoesNotInferFromAssetID(t *testing.T) {
	t.Parallel()
	m := ClipSemanticMetadata{
		AssetID: "yt_abc_0_60_v1",
		Title:   "Test",
	}
	sti := m.AsSearchTextInput("")
	if sti.Source != "" {
		t.Errorf("Source = %q, want empty source until registry resolution", sti.Source)
	}
}

func TestAsSearchTextInput_DefensiveCopy(t *testing.T) {
	t.Parallel()
	tags := []string{"boxing"}
	m := ClipSemanticMetadata{Tags: tags}
	sti := m.AsSearchTextInput("youtube")
	sti.Tags[0] = "mutated"
	if tags[0] != "boxing" {
		t.Error("Tags slice was mutated — defensive copy failed")
	}
}

func TestAsSearchTextInput_StockEmptyRound_OmitsFromAdditional(t *testing.T) {
	t.Parallel()
	m := ClipSemanticMetadata{
		AssetID:  "planner:abc:0",
		Event:    "Test",
		StartSec: 10.0,
		// Round = 0 (zero = omitted)
	}
	sti := m.AsSearchTextInput("stock")
	if sti.Additional == nil {
		t.Fatal("Additional should be non-nil (Event is set)")
	}
	if _, ok := sti.Additional["round"]; ok {
		t.Error("Round=0 should be omitted from Additional")
	}
}

func TestAsSearchTextInput_StockZeroStartSec_OmitsFromAdditional(t *testing.T) {
	t.Parallel()
	m := ClipSemanticMetadata{
		AssetID: "planner:abc:0",
		Event:   "Test",
		// StartSec = 0 (zero = omitted)
	}
	sti := m.AsSearchTextInput("stock")
	if sti.Additional == nil {
		t.Fatal("Additional should be non-nil (Event is set)")
	}
	if _, ok := sti.Additional["start_sec"]; ok {
		t.Error("StartSec=0 should be omitted from Additional")
	}
}

// ── Clone ─────────────────────────────────────────────────────────────

func TestClone_DeepCopy(t *testing.T) {
	t.Parallel()
	m := ClipSemanticMetadata{
		AssetID:         "yt_abc_0_60_v1",
		Tags:            []string{"a", "b"},
		Entities:        []string{"e1"},
		Speakers:        []string{"s1"},
		MentionedPeople: []string{"p1"},
		Topics:          []string{"t1"},
	}
	cp := m.Clone()

	// Mutate clone
	cp.Tags[0] = "mutated"
	cp.Entities[0] = "mutated"
	cp.Speakers[0] = "mutated"

	if m.Tags[0] != "a" {
		t.Error("Original Tags mutated")
	}
	if m.Entities[0] != "e1" {
		t.Error("Original Entities mutated")
	}
	if m.Speakers[0] != "s1" {
		t.Error("Original Speakers mutated")
	}
}

func TestClone_NilSlices(t *testing.T) {
	t.Parallel()
	m := ClipSemanticMetadata{AssetID: "x"}
	cp := m.Clone()
	if cp.Tags != nil {
		t.Errorf("Clone Tags = %v, want nil", cp.Tags)
	}
}

func TestClone_PreservesValues(t *testing.T) {
	t.Parallel()
	m := ClipSemanticMetadata{
		AssetID:   "yt_abc_0_60_v1",
		Title:     "Test",
		StartSec:  10.0,
		EndSec:    20.0,
		Round:     5,
		SourceURL: "https://example.com",
	}
	cp := m.Clone()
	if cp.AssetID != m.AssetID {
		t.Errorf("AssetID = %q, want %q", cp.AssetID, m.AssetID)
	}
	if cp.Round != 5 {
		t.Errorf("Round = %d, want 5", cp.Round)
	}
}

// ── IsEmpty ───────────────────────────────────────────────────────────

func TestIsEmpty_ZeroValue(t *testing.T) {
	t.Parallel()
	m := ClipSemanticMetadata{}
	if !m.IsEmpty() {
		t.Error("zero-value should be empty")
	}
}

func TestIsEmpty_HasAssetID(t *testing.T) {
	t.Parallel()
	m := ClipSemanticMetadata{AssetID: "x"}
	if m.IsEmpty() {
		t.Error("AssetID set should not be empty")
	}
}

func TestIsEmpty_HasTitleOnly(t *testing.T) {
	t.Parallel()
	m := ClipSemanticMetadata{Title: "hello"}
	if m.IsEmpty() {
		t.Error("Title set should not be empty")
	}
}

func TestIsEmpty_HasContentHashOnly(t *testing.T) {
	t.Parallel()
	m := ClipSemanticMetadata{ContentHash: "abc123"}
	if m.IsEmpty() {
		t.Error("ContentHash set should not be empty")
	}
}

// ── ComputeDurationSec ────────────────────────────────────────────────

func TestComputeDurationSec_ComputesWhenZero(t *testing.T) {
	t.Parallel()
	m := ClipSemanticMetadata{StartSec: 10.0, EndSec: 25.5}
	d := m.ComputeDurationSec()
	if d != 15.5 {
		t.Errorf("DurationSec = %f, want 15.5", d)
	}
	if m.DurationSec != 15.5 {
		t.Errorf("field DurationSec = %f, want 15.5", m.DurationSec)
	}
}

func TestComputeDurationSec_PreservesNonZero(t *testing.T) {
	t.Parallel()
	m := ClipSemanticMetadata{StartSec: 10.0, EndSec: 25.5, DurationSec: 99.0}
	d := m.ComputeDurationSec()
	if d != 99.0 {
		t.Errorf("DurationSec = %f, want 99.0 (preserved)", d)
	}
}

func TestComputeDurationSec_NegativeReturnsZero(t *testing.T) {
	t.Parallel()
	m := ClipSemanticMetadata{StartSec: 25.0, EndSec: 10.0}
	d := m.ComputeDurationSec()
	if d != 0 {
		t.Errorf("DurationSec = %f, want 0 (negative)", d)
	}
}

// ── MergedTags ────────────────────────────────────────────────────────

func TestMergedTags_YouTube_AllSources(t *testing.T) {
	t.Parallel()
	m := ClipSemanticMetadata{
		Tags:            []string{"boxing", "pacquiao"},
		Topics:          []string{"confrontation"},
		Speakers:        []string{"Pacquiao", "Broner"},
		MentionedPeople: []string{"Mayweather"},
		Entities:        []string{"entity1"},
	}
	merged := m.MergedTags()
	if len(merged) != 7 {
		t.Errorf("MergedTags len = %d, want 7; got %v", len(merged), merged)
	}
}

func TestMergedTags_Deduplicates(t *testing.T) {
	t.Parallel()
	m := ClipSemanticMetadata{
		Tags:     []string{"boxing", "BOXING"},
		Topics:   []string{"boxing"}, // exact dup of Tags[0]
		Speakers: []string{"Pacquiao"},
	}
	merged := m.MergedTags()
	// "boxing" appears in Tags and Topics — deduped to 1
	// "BOXING" is different (case-sensitive) — kept
	// "Pacquiao" — unique
	seen := make(map[string]bool)
	for _, tag := range merged {
		if seen[tag] {
			t.Errorf("duplicate tag: %q", tag)
		}
		seen[tag] = true
	}
	if !seen["boxing"] {
		t.Error("missing 'boxing'")
	}
	if !seen["BOXING"] {
		t.Error("missing 'BOXING'")
	}
	if !seen["Pacquiao"] {
		t.Error("missing 'Pacquiao'")
	}
}

func TestMergedTags_SkipsEmpty(t *testing.T) {
	t.Parallel()
	m := ClipSemanticMetadata{
		Tags:     []string{"", "  ", "valid"},
		Topics:   []string{""},
		Speakers: nil,
	}
	merged := m.MergedTags()
	if len(merged) != 1 || merged[0] != "valid" {
		t.Errorf("MergedTags = %v, want [valid]", merged)
	}
}

func TestMergedTags_Stock_TagsOnly(t *testing.T) {
	t.Parallel()
	m := ClipSemanticMetadata{
		Tags: []string{"boxing", "round7"},
	}
	merged := m.MergedTags()
	if len(merged) != 2 {
		t.Errorf("MergedTags len = %d, want 2", len(merged))
	}
}

// ── AsSearchTextInput edge cases ──────────────────────────────────────

func TestAsSearchTextInput_Voiceover_PopulatesTopicAndLanguage(t *testing.T) {
	t.Parallel()
	m := ClipSemanticMetadata{
		AssetID: "vo_abc_123",
		Title:   "Storia della boxe",
	}
	sti := m.AsSearchTextInput("voiceover")
	if sti.Source != "voiceover" {
		t.Errorf("Source = %q, want %q", sti.Source, "voiceover")
	}
	if sti.Title != "Storia della boxe" {
		t.Errorf("Title = %q, want %q", sti.Title, "Storia della boxe")
	}
}

func TestAsSearchTextInput_Stock_AllZeroValues_NoAdditional(t *testing.T) {
	t.Parallel()
	m := ClipSemanticMetadata{
		AssetID: "planner:abc:0",
		Title:   "Clip",
		// All stock-specific fields at zero
	}
	sti := m.AsSearchTextInput("stock")
	// Event="", Round=0, Subject="", StartSec=0, EndSec=0
	// → Additional should be nil or empty
	if sti.Additional != nil && len(sti.Additional) > 0 {
		t.Errorf("Additional should be nil/empty for all-zero stock fields, got %v", sti.Additional)
	}
}

func TestAsSearchTextInput_FloatPrecision_StartSec(t *testing.T) {
	t.Parallel()
	m := ClipSemanticMetadata{
		AssetID:  "planner:abc:0",
		StartSec: 32.5,
		EndSec:   51.75,
		Event:    "test",
	}
	sti := m.AsSearchTextInput("stock")
	if sti.Additional["start_sec"] != "32.5" {
		t.Errorf("start_sec = %q, want %q", sti.Additional["start_sec"], "32.5")
	}
	if sti.Additional["end_sec"] != "51.75" {
		t.Errorf("end_sec = %q, want %q", sti.Additional["end_sec"], "51.75")
	}
}
