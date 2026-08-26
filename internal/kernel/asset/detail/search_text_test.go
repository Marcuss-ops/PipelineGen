// Package asset — SearchTextComposer TDD tests (PR-CANONICAL-SEARCHTEXT-PORT).
//
// godlike/06 SSOT: these tests pin the canonical per-source strategy
// contracts and the ComposerRegistry dispatch behavior. Each test
// function covers one strategy or one registry invariant.
package detail

import (
	"strings"
	"testing"

	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── YouTube strategy tests ──────────────────────────────────────────

func TestYouTubeSearchTextStrategy_HappyPath(t *testing.T) {
	input := SearchTextInput{
		Source:          "youtube",
		Title:           "Sfuriata contro Pacquiao",
		Summary:         "Broner insults Pacquiao on camera",
		Hook:            "Stay focused! It is personal.",
		Topics:          []string{"boxing", "confrontation"},
		SourceURL:       "https://www.youtube.com/watch?v=vdC5GXxS-qU",
		Speakers:        []string{"Broner", "Pacquiao"},
		MentionedPeople: []string{"Floyd Mayweather"},
	}
	got := youtubeSearchTextStrategy(input)
	for _, want := range []string{
		"Sfuriata contro Pacquiao",
		"Broner insults Pacquiao on camera",
		"Stay focused! It is personal.",
		"boxing confrontation",
		"https://www.youtube.com/watch?v=vdC5GXxS-qU",
		"Broner Pacquiao",
		"Floyd Mayweather",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("youtube strategy output missing %q\ngot: %q", want, got)
		}
	}
}

func TestYouTubeSearchTextStrategy_EmptyAllFields(t *testing.T) {
	got := youtubeSearchTextStrategy(SearchTextInput{Source: "youtube"})
	if got != "" {
		t.Errorf("empty input should produce empty output, got %q", got)
	}
}

func TestYouTubeSearchTextStrategy_TopicsDroppedWhenEmpty(t *testing.T) {
	input := SearchTextInput{
		Source: "youtube",
		Title:  "Test",
	}
	got := youtubeSearchTextStrategy(input)
	if strings.Contains(got, "Topics:") || strings.Contains(got, "tags:") {
		t.Errorf("empty topics should not produce label, got %q", got)
	}
} // ── Stock strategy tests ────────────────────────────────────────────

func TestStockSearchTextStrategy_HappyPath(t *testing.T) {
	input := SearchTextInput{
		Source:   "stock",
		Title:    "Pacquiao Vs Broner",
		Category: "Boxe",
		Tags:     []string{"boxing", "pacquiao"},
		Additional: map[string]string{
			"event":     "Pacquiao vs Broner",
			"round":     "7",
			"subject":   "Pacquiao",
			"action":    "lands a left hook",
			"start_sec": "32.0",
			"end_sec":   "51.0",
		},
	}
	got := stockSearchTextStrategy(input)
	for _, want := range []string{
		"Pacquiao Vs Broner",
		"Boxe",
		"Stock video from Pacquiao vs Broner round 7",
		"Pacquiao lands a left hook",
		"Segment 32.0s to 51.0s",
		"Tags: boxing pacquiao",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("stock strategy output missing %q\ngot: %q", want, got)
		}
	}
}

func TestStockSearchTextStrategy_NilAdditional(t *testing.T) {
	input := SearchTextInput{
		Source: "stock",
		Title:  "Clip",
	}
	got := stockSearchTextStrategy(input)
	if !strings.Contains(got, "Clip") {
		t.Errorf("nil Additional should not panic, got %q", got)
	}
}

func TestStockSearchTextStrategy_EmptyAllFields(t *testing.T) {
	got := stockSearchTextStrategy(SearchTextInput{Source: "stock"})
	if got != "" {
		t.Errorf("empty input should produce empty output, got %q", got)
	}
}

func TestStockSearchTextStrategy_SegmentDropOnPartialEndpoints(t *testing.T) {
	// Only start_sec set — should NOT emit "Segment Xs to s"
	input := SearchTextInput{
		Source: "stock",
		Title:  "Test",
		Additional: map[string]string{
			"start_sec": "10.0",
		},
	}
	got := stockSearchTextStrategy(input)
	if strings.Contains(got, "Segment") {
		t.Errorf("partial endpoint should drop segment, got %q", got)
	}
}

func TestStockSearchTextStrategy_RoundOnlyHeader(t *testing.T) {
	input := SearchTextInput{
		Source:     "stock",
		Title:      "Fight",
		Additional: map[string]string{"round": "5"},
	}
	got := stockSearchTextStrategy(input)
	if !strings.Contains(got, "Stock video, round 5") {
		t.Errorf("round-only header should use comma separator, got %q", got)
	}
}

func TestStockSearchTextStrategy_SourceURLIncluded(t *testing.T) {
	input := SearchTextInput{
		Source:     "stock",
		Title:      "Clip",
		Additional: map[string]string{"source_url": "https://example.com/video.mp4"},
	}
	got := stockSearchTextStrategy(input)
	if !strings.Contains(got, "Source: https://example.com/video.mp4") {
		t.Errorf("source_url should be included, got %q", got)
	}
}

// ── Artlist strategy tests ──────────────────────────────────────────

func TestArtlistSearchTextStrategy_HappyPath(t *testing.T) {
	input := SearchTextInput{
		Source:      "artlist",
		Title:       "Sunset Beach",
		Tags:        []string{"nature", "ocean"},
		Category:    "landscape",
		Description: "A beautiful sunset over the ocean waves.",
	}
	got := artlistSearchTextStrategy(input)
	for _, want := range []string{"Sunset Beach", "nature ocean", "landscape", "sunset"} {
		if !strings.Contains(got, want) {
			t.Errorf("artlist strategy output missing %q\ngot: %q", want, got)
		}
	}
}

// ── Voiceover strategy tests ───────────────────────────────────────

func TestVoiceoverSearchTextStrategy_HappyPath(t *testing.T) {
	input := SearchTextInput{
		Source:     "voiceover",
		Title:      "Boxing History",
		Transcript: "The greatest boxers of all time include Ali and Tyson.",
		Language:   "en-US",
		Topic:      "boxing",
	}
	got := voiceoverSearchTextStrategy(input)
	for _, want := range []string{"Boxing History", "Ali and Tyson", "en-US", "boxing"} {
		if !strings.Contains(got, want) {
			t.Errorf("voiceover strategy output missing %q\ngot: %q", want, got)
		}
	}
}

// ── Image strategy tests ────────────────────────────────────────────

func TestImageSearchTextStrategy_HappyPath(t *testing.T) {
	input := SearchTextInput{
		Source:           string(asset.SourceImage),
		Prompt:           "A boxer in the ring",
		Caption:          "Fight night",
		DetectedEntities: []string{"ring", "boxer"},
		Tags:             []string{"sports"},
		OriginProvider:   "dall-e",
		Category:         "sports",
	}
	got := imageSearchTextStrategy(input)
	for _, want := range []string{"A boxer in the ring", "Fight night", "ring boxer", "sports", "dall-e"} {
		if !strings.Contains(got, want) {
			t.Errorf("image strategy output missing %q\ngot: %q", want, got)
		}
	}
}

// ── Registry tests ──────────────────────────────────────────────────

func TestComposerRegistry_AllSixSourcesRegistered(t *testing.T) {
	r := NewComposerRegistry()
	// image/generated_image use Prompt, not Title; all others use Title.
	sourcesWithTitle := []string{"youtube", "artlist", "voiceover", "stock"}
	for _, src := range sourcesWithTitle {
		got, err := r.Compose(SearchTextInput{Source: src, Title: "test"})
		if err != nil {
			t.Errorf("source %q returned error: %v", src, err)
		}
		if got == "" {
			t.Errorf("source %q returned empty text for non-empty title", src)
		}
	}
	sourcesWithPrompt := []string{"image", "generated_image"}
	for _, src := range sourcesWithPrompt {
		got, err := r.Compose(SearchTextInput{Source: src, Prompt: "a boxer"})
		if err != nil {
			t.Errorf("source %q returned error: %v", src, err)
		}
		if got == "" {
			t.Errorf("source %q returned empty text for non-empty prompt", src)
		}
	}
}

func TestComposerRegistry_UnknownSourceFallsBack(t *testing.T) {
	r := NewComposerRegistry()
	got, err := r.Compose(SearchTextInput{Source: "unknown_future_source", Title: "Test Title", Tags: []string{"tag1"}})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "Test Title") {
		t.Errorf("fallback should include title, got %q", got)
	}
	if !strings.Contains(got, "tag1") {
		t.Errorf("fallback should include tags, got %q", got)
	}
}

func TestComposerRegistry_EmptySourceReturnsError(t *testing.T) {
	r := NewComposerRegistry()
	_, err := r.Compose(SearchTextInput{Source: "", Title: "test"})
	if err == nil {
		t.Error("empty Source should return error")
	}
}

func TestComposerRegistry_RegisterCustomStrategy(t *testing.T) {
	r := NewComposerRegistry()
	r.Register("custom", func(input SearchTextInput) string {
		return "CUSTOM: " + input.Title
	})
	got, err := r.Compose(SearchTextInput{Source: "custom", Title: "hello"})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if got != "CUSTOM: hello" {
		t.Errorf("custom strategy not dispatched, got %q", got)
	}
}

func TestComposerRegistry_NilStrategyRemovesSource(t *testing.T) {
	r := NewComposerRegistry()
	r.Register("youtube", nil)
	// After removing, youtube should fall back to default
	got, err := r.Compose(SearchTextInput{Source: "youtube", Title: "Test", Tags: []string{"x"}})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "Test") {
		t.Errorf("removed strategy should fall back to default, got %q", got)
	}
}

// ── Truncation tests ────────────────────────────────────────────────

func TestTruncateSearchText_LongTranscript(t *testing.T) {
	long := strings.Repeat("a", 3000)
	got := truncateSearchText(long, MaxTranscriptChars)
	if len([]rune(got)) != MaxTranscriptChars {
		t.Errorf("expected %d runes, got %d", MaxTranscriptChars, len([]rune(got)))
	}
}

func TestTruncateSearchText_ShortTranscript(t *testing.T) {
	short := "hello"
	got := truncateSearchText(short, MaxTranscriptChars)
	if got != short {
		t.Errorf("short text should not be truncated, got %q", got)
	}
}

func TestTruncateSearchText_ZeroMaxLen(t *testing.T) {
	got := truncateSearchText("hello", 0)
	if got != "hello" {
		t.Errorf("zero maxLen should return unchanged, got %q", got)
	}
}

func TestTruncateSearchText_UnicodeSafe(t *testing.T) {
	// 10 emoji (each 1 rune in Go's rune count)
	emoji := strings.Repeat("🥊", 10)
	got := truncateSearchText(emoji, 5)
	if len([]rune(got)) != 5 {
		t.Errorf("expected 5 runes, got %d", len([]rune(got)))
	}
}

// ── Helper tests ────────────────────────────────────────────────────

func TestJoinSearchTextNonEmpty_SkipsEmpty(t *testing.T) {
	got := joinSearchTextNonEmpty(" ", "a", "", "b", " ", "c")
	if got != "a b c" {
		t.Errorf("expected 'a b c', got %q", got)
	}
}

func TestJoinSearchTextNonEmpty_AllEmpty(t *testing.T) {
	got := joinSearchTextNonEmpty(" ", "", "", "")
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestJoinSearchTextTags_NilSlice(t *testing.T) {
	got := joinSearchTextTags(nil)
	if got != "" {
		t.Errorf("nil slice should return empty, got %q", got)
	}
}

func TestJoinSearchTextTags_WithEmptyEntries(t *testing.T) {
	got := joinSearchTextTags([]string{"a", "", " ", "b"})
	if got != "a b" {
		t.Errorf("expected 'a b', got %q", got)
	}
}

func TestComposeStockSearchHeader_BothPresent(t *testing.T) {
	got := composeStockSearchHeader("Fight Night", "3")
	if got != "Stock video from Fight Night round 3" {
		t.Errorf("unexpected: %q", got)
	}
}

func TestComposeStockSearchHeader_EventOnly(t *testing.T) {
	got := composeStockSearchHeader("Fight Night", "")
	if got != "Stock video from Fight Night" {
		t.Errorf("unexpected: %q", got)
	}
}

func TestComposeStockSearchHeader_RoundOnly(t *testing.T) {
	got := composeStockSearchHeader("", "3")
	if got != "Stock video, round 3" {
		t.Errorf("unexpected: %q", got)
	}
}

func TestComposeStockSearchHeader_BothEmpty(t *testing.T) {
	got := composeStockSearchHeader("", "")
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

// ── Compile-time pin ────────────────────────────────────────────────

func TestComposerRegistry_SatisfiesSearchTextComposer(t *testing.T) {
	// This is a compile-time assertion enforced by the var _ declaration
	// in search_text.go. If ComposerRegistry doesn't satisfy
	// SearchTextComposer, this package won't compile.
	var _ SearchTextComposer = (*ComposerRegistry)(nil)
}

func TestGeneratedImageSearchTextStrategy_DelegatesToImage(t *testing.T) {
	input := SearchTextInput{
		Source:         "generated_image",
		Prompt:         "a boxer in the ring",
		Caption:        "fight night",
		OriginProvider: "dall-e",
		Tags:           []string{"sports"},
	}
	gen := generatedImageSearchTextStrategy(input)
	img := imageSearchTextStrategy(input)
	if gen != img {
		t.Errorf("generated_image should delegate to image:\ngen: %q\nimg: %q", gen, img)
	}
}

func TestComposerRegistry_Deterministic(t *testing.T) {
	r := NewComposerRegistry()
	input := SearchTextInput{
		Source:   "stock",
		Title:    "Fight",
		Category: "Boxe",
		Additional: map[string]string{
			"event":   "Pacquiao vs Broner",
			"round":   "7",
			"subject": "Pacquiao",
		},
	}
	got1, _ := r.Compose(input)
	got2, _ := r.Compose(input)
	if got1 != got2 {
		t.Errorf("non-deterministic: %q != %q", got1, got2)
	}
}
