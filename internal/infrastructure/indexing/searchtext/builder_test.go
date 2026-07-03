package searchtext

import (
	"context"
	"strings"
	"testing"

	appsearchtext "github.com/Marcuss-ops/PipelineGen/internal/application/indexing/searchtext"
)

// ── Registry tests ──────────────────────────────────────────────────────

func TestRegistry_Build_NilAssetID(t *testing.T) {
	r := NewRegistry()
	_, err := r.Build(context.Background(), appsearchtext.SearchTextInput{})
	if err == nil {
		t.Fatal("Build with empty AssetID must return error")
	}
	if !strings.Contains(err.Error(), "AssetID") {
		t.Errorf("error must mention AssetID; got %v", err)
	}
}

func TestRegistry_Build_UnknownSource_FallsBack(t *testing.T) {
	r := NewRegistry()
	text, err := r.Build(context.Background(), appsearchtext.SearchTextInput{
		AssetID: "asset-1",
		Source:  "unknown_source_type",
		Title:   "Hello World",
		Tags:    []string{"tag1", "tag2"},
	})
	if err != nil {
		t.Fatalf("Build with unknown source must not error; got %v", err)
	}
	if !strings.Contains(text, "Hello World") {
		t.Errorf("fallback must include title; got %q", text)
	}
	if !strings.Contains(text, "tag1 tag2") {
		t.Errorf("fallback must include tags; got %q", text)
	}
}

func TestRegistry_Register_Override(t *testing.T) {
	r := NewRegistry()
	r.Register("youtube", func(input appsearchtext.SearchTextInput) string {
		return "custom:" + input.Title
	})
	text, err := r.Build(context.Background(), appsearchtext.SearchTextInput{
		AssetID: "asset-1",
		Source:  "youtube",
		Title:   "Test",
	})
	if err != nil {
		t.Fatalf("Build must not error; got %v", err)
	}
	if text != "custom:Test" {
		t.Errorf("override must be used; got %q", text)
	}
}

func TestRegistry_Register_Nil_Removes(t *testing.T) {
	r := NewRegistry()
	r.Register("youtube", nil)
	text, err := r.Build(context.Background(), appsearchtext.SearchTextInput{
		AssetID: "asset-1",
		Source:  "youtube",
		Title:   "Test",
		Tags:    []string{"a"},
	})
	if err != nil {
		t.Fatalf("Build must not error; got %v", err)
	}
	// After removal, should fall back to default (title + tags).
	if text != "Test a" {
		t.Errorf("after nil-Register, default fallback must apply; got %q", text)
	}
}

// ── Strategy tests ──────────────────────────────────────────────────────

func TestYoutubeStrategy(t *testing.T) {
	input := appsearchtext.SearchTextInput{
		AssetID:     "yt-1",
		Source:      "youtube",
		Title:       "Amazing Video Title",
		Transcript:  "This is the full transcript of the video.",
		Channel:     "MyChannel",
		Description: "Video description here.",
	}
	got := youtubeStrategy(input)
	mustContainAll(t, got,
		"Amazing Video Title",
		"This is the full transcript of the video.",
		"MyChannel",
		"Video description here.",
	)
}

func TestYoutubeStrategy_Minimal(t *testing.T) {
	input := appsearchtext.SearchTextInput{
		AssetID: "yt-2",
		Source:  "youtube",
		Title:   "Only Title",
	}
	got := youtubeStrategy(input)
	if got != "Only Title" {
		t.Errorf("minimal YouTube must be just title; got %q", got)
	}
}

func TestYoutubeStrategy_TranscriptTruncation(t *testing.T) {
	longTranscript := strings.Repeat("x", 2500)
	input := appsearchtext.SearchTextInput{
		AssetID:    "yt-3",
		Source:     "youtube",
		Title:      "T",
		Transcript: longTranscript,
	}
	got := youtubeStrategy(input)
	if len(got) > 2000+1+1 { // title (1) + space + transcript (max 2000)
		t.Errorf("transcript must be truncated to 2000 chars; got len=%d text=%q", len(got), got)
	}
}

func TestArtlistStrategy(t *testing.T) {
	input := appsearchtext.SearchTextInput{
		AssetID:     "art-1",
		Source:      "artlist",
		Title:       "Cinematic Drone Shot",
		Tags:        []string{"drone", "cinematic", "aerial"},
		Category:    "nature",
		Description: "Beautiful drone footage over mountains.",
	}
	got := artlistStrategy(input)
	mustContainAll(t, got,
		"Cinematic Drone Shot",
		"drone cinematic aerial",
		"nature",
		"Beautiful drone footage over mountains.",
	)
}

func TestArtlistStrategy_NoTags(t *testing.T) {
	input := appsearchtext.SearchTextInput{
		AssetID:  "art-2",
		Source:   "artlist",
		Title:    "Simple Clip",
		Category: "music",
	}
	got := artlistStrategy(input)
	if got != "Simple Clip music" {
		t.Errorf("expected title + category only; got %q", got)
	}
}

func TestVoiceoverStrategy(t *testing.T) {
	input := appsearchtext.SearchTextInput{
		AssetID:    "vo-1",
		Source:     "voiceover",
		Title:      "Introduction Scene",
		Transcript: "Welcome to this amazing video about AI.",
		Language:   "en-US",
		Topic:      "Introduction",
	}
	got := voiceoverStrategy(input)
	mustContainAll(t, got,
		"Introduction Scene",
		"Welcome to this amazing video about AI.",
		"en-US",
		"Introduction",
	)
}

func TestVoiceoverStrategy_Minimal(t *testing.T) {
	input := appsearchtext.SearchTextInput{
		AssetID: "vo-2",
		Source:  "voiceover",
		Title:   "Scene 5",
	}
	got := voiceoverStrategy(input)
	if got != "Scene 5" {
		t.Errorf("minimal voiceover must be just title; got %q", got)
	}
}

func TestImageStrategy(t *testing.T) {
	input := appsearchtext.SearchTextInput{
		AssetID:          "img-1",
		Source:           "image",
		Prompt:           "sunset over mountains with birds flying",
		Caption:          "A beautiful sunset panorama",
		DetectedEntities: []string{"mountain", "sun", "bird", "sky"},
		Tags:             []string{"landscape", "nature"},
		OriginProvider:   "unsplash",
		Category:         "photography",
	}
	got := imageStrategy(input)
	mustContainAll(t, got,
		"sunset over mountains with birds flying",
		"A beautiful sunset panorama",
		"mountain sun bird sky",
		"landscape nature",
		"unsplash",
		"photography",
	)
}

func TestImageStrategy_PromptOnly(t *testing.T) {
	input := appsearchtext.SearchTextInput{
		AssetID: "img-2",
		Source:  "image",
		Prompt:  "a cat wearing a hat",
	}
	got := imageStrategy(input)
	if got != "a cat wearing a hat" {
		t.Errorf("prompt-only image must be the prompt itself; got %q", got)
	}
}

func TestGeneratedImageStrategy_SameAsImage(t *testing.T) {
	input := appsearchtext.SearchTextInput{
		AssetID:          "gen-1",
		Source:           "generated_image",
		Prompt:           "futuristic cityscape",
		Caption:          "AI-generated city",
		DetectedEntities: []string{"building", "sky"},
		Tags:             []string{"ai-art", "scifi"},
		OriginProvider:   "dall-e",
	}
	gotImage := imageStrategy(input)
	gotGen := generatedImageStrategy(input)
	if gotImage != gotGen {
		t.Errorf("generated_image must produce same text as image; image=%q generated=%q", gotImage, gotGen)
	}
}

func TestImageStrategy_Empty(t *testing.T) {
	input := appsearchtext.SearchTextInput{
		AssetID: "img-3",
		Source:  "image",
	}
	got := imageStrategy(input)
	if got != "" {
		t.Errorf("fully empty image input must return empty; got %q", got)
	}
}

// ── Strategy dispatch via registry ──────────────────────────────────────

func TestRegistryDispatch_AllSources(t *testing.T) {
	r := NewRegistry()
	tests := []struct {
		name   string
		source string
		input  appsearchtext.SearchTextInput
		want   []string // substrings that must appear
	}{
		{
			name:   "youtube",
			source: "youtube",
			input: appsearchtext.SearchTextInput{
				AssetID: "a-1", Source: "youtube",
				Title: "YT Title", Transcript: "transcript", Channel: "ch", Description: "desc",
			},
			want: []string{"YT Title", "transcript", "ch", "desc"},
		},
		{
			name:   "artlist",
			source: "artlist",
			input: appsearchtext.SearchTextInput{
				AssetID: "a-2", Source: "artlist",
				Title: "Art Title", Tags: []string{"t1", "t2"}, Category: "cat", Description: "desc",
			},
			want: []string{"Art Title", "t1 t2", "cat", "desc"},
		},
		{
			name:   "voiceover",
			source: "voiceover",
			input: appsearchtext.SearchTextInput{
				AssetID: "a-3", Source: "voiceover",
				Title: "VO Title", Transcript: "vo text", Language: "it-IT", Topic: "topic",
			},
			want: []string{"VO Title", "vo text", "it-IT", "topic"},
		},
		{
			name:   "image",
			source: "image",
			input: appsearchtext.SearchTextInput{
				AssetID: "a-4", Source: "image",
				Prompt: "prompt", Caption: "caption", DetectedEntities: []string{"e1"},
				Tags: []string{"tag"}, OriginProvider: "unsplash", Category: "photo",
			},
			want: []string{"prompt", "caption", "e1", "tag", "unsplash", "photo"},
		},
		{
			name:   "generated_image",
			source: "generated_image",
			input: appsearchtext.SearchTextInput{
				AssetID: "a-5", Source: "generated_image",
				Prompt: "gen prompt", Caption: "gen caption",
			},
			want: []string{"gen prompt", "gen caption"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := r.Build(context.Background(), tc.input)
			if err != nil {
				t.Fatalf("Build(%s) unexpected error: %v", tc.source, err)
			}
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("Build(%s) = %q; must contain %q", tc.source, got, w)
				}
			}
		})
	}
}

// ── Edge cases ──────────────────────────────────────────────────────────

func TestAllStrategies_EmptyInput_ReturnsEmpty(t *testing.T) {
	empty := appsearchtext.SearchTextInput{AssetID: "e", Source: "youtube"}
	for _, s := range []Strategy{youtubeStrategy, artlistStrategy, voiceoverStrategy, imageStrategy, generatedImageStrategy} {
		got := s(empty)
		if got != "" {
			t.Errorf("strategy with no data must return empty; got %q", got)
		}
	}
}

func TestAllStrategies_Idempotent(t *testing.T) {
	// Running the same input twice must produce the same output.
	input := appsearchtext.SearchTextInput{
		AssetID:          "idem-1",
		Source:           "image",
		Title:            "T",
		Prompt:           "P",
		Tags:             []string{"a", "b"},
		DetectedEntities: []string{"c", "d"},
	}
	first := imageStrategy(input)
	second := imageStrategy(input)
	if first != second {
		t.Errorf("strategy must be idempotent; first=%q second=%q", first, second)
	}
}

func TestJoinNonEmpty_AllEmpty(t *testing.T) {
	got := joinNonEmpty(" ", "", "  ", "")
	if got != "" {
		t.Errorf("all-empty must produce empty; got %q", got)
	}
}

func TestJoinNonEmpty_NoSep(t *testing.T) {
	got := joinNonEmpty(" ", "hello")
	if got != "hello" {
		t.Errorf("single part: %q", got)
	}
}

func TestJoinTags_EmptySlice(t *testing.T) {
	if got := joinTags(nil); got != "" {
		t.Errorf("nil tags: %q", got)
	}
	if got := joinTags([]string{}); got != "" {
		t.Errorf("empty tags: %q", got)
	}
}

func TestJoinTags_FiltersEmpty(t *testing.T) {
	got := joinTags([]string{"a", "", "  ", "b"})
	if got != "a b" {
		t.Errorf("must skip empty tags; got %q", got)
	}
}

func TestTruncate_Noop(t *testing.T) {
	if got := truncate("hello", 100); got != "hello" {
		t.Errorf("short string: %q", got)
	}
	if got := truncate("hello", 5); got != "hello" {
		t.Errorf("exact-length string: %q", got)
	}
	if got := truncate("hello", 0); got != "hello" {
		t.Errorf("maxLen=0 means no truncation: %q", got)
	}
}

func TestTruncate_Truncates(t *testing.T) {
	got := truncate("hello world", 5)
	if got != "hello" {
		t.Errorf("must truncate to 5 chars; got %q", got)
	}
}

// ── Helpers ─────────────────────────────────────────────────────────────

func mustContainAll(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("output must contain %q; got %q", w, got)
		}
	}
}
