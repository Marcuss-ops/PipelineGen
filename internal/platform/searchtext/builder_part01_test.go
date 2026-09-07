package searchtext

import (
	"context"
	appsearchtext "github.com/Marcuss-ops/PipelineGen/internal/capabilities/indexing/searchtext"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"strings"
	"testing"
)

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
		Tags:        []string{"boxing", "press conference"},
		Additional: map[string]string{
			"hook":             "Ti spacco il culo!",
			"speakers":         "Adrien Broner Manny Pacquiao",
			"mentioned_people": "Floyd Mayweather",
		},
	}
	got := youtubeStrategy(input)
	mustContainAll(t, got,
		"Amazing Video Title",
		"This is the full transcript of the video.",
		"MyChannel",
		"Video description here.",
		"boxing press conference",
		"Ti spacco il culo!",
		"Adrien Broner Manny Pacquiao",
		"Floyd Mayweather",
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
		Source:           string(asset.SourceImage),
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
		Source:  string(asset.SourceImage),
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
		Source:  string(asset.SourceImage),
	}
	got := imageStrategy(input)
	if got != "" {
		t.Errorf("fully empty image input must return empty; got %q", got)
	}
}

// ── Stock chunk strategy tests ─────────────────────────────────────────

func TestStockChunkStrategy_HappyPath(t *testing.T) {
	input := appsearchtext.SearchTextInput{
		AssetID:  "stock-1",
		Source:   "stock",
		Title:    "Stock clip 001",
		Category: "Boxe",
		Tags:     []string{"boxing", "training", "match"},
		Additional: map[string]string{
			"event":     "Pacquiao vs Broner",
			"round":     "3",
			"subject":   "Mike Tyson",
			"action":    "lands a left hook",
			"start_sec": "12.5",
			"end_sec":   "35.0",
		},
	}
	got := stockChunkStrategy(input)
	mustContainAll(t, got,
		"Stock clip 001", // title prefix
		"Boxe",           // category prefix
		"Stock video from Pacquiao vs Broner round 3",
		"Mike Tyson",
		"lands a left hook",
		"Segment 12.5s to 35.0s",
		"Tags: boxing training match",
	)
}
