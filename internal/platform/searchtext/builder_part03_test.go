package searchtext

import (
	"context"
	appsearchtext "github.com/Marcuss-ops/PipelineGen/internal/capabilities/indexing/searchtext"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"strings"
	"testing"
)

// TestStockChunkStrategy_RegistryDispatch pins that the registry
// resolves "stock" to stockChunkStrategy (regression guard for the
// NewRegistry mapping).
func TestStockChunkStrategy_RegistryDispatch(t *testing.T) {
	r := NewRegistry()
	got, err := r.Build(context.Background(), appsearchtext.SearchTextInput{
		AssetID:  "stock-reg-1",
		Source:   "stock",
		Title:    "Dispatched title",
		Category: "Boxe",
		Additional: map[string]string{
			"event":   "Fight",
			"round":   "1",
			"subject": "Fighter",
		},
	})
	if err != nil {
		t.Fatalf("Registry.Build(stock) unexpected error: %v", err)
	}
	mustContainAll(t, got,
		"Dispatched title",
		"Boxe",
		"Stock video from Fight round 1",
		"Fighter",
	)
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
				Tags: []string{"boxing"},
				Additional: map[string]string{
					"hook":     "hook text",
					"speakers": "Speaker A", "mentioned_people": "Person B",
				},
			},
			want: []string{"YT Title", "transcript", "ch", "desc", "boxing", "hook text", "Speaker A", "Person B"},
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
				AssetID: "a-4", Source: string(asset.SourceImage),
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
		{
			name:   "stock",
			source: "stock",
			input: appsearchtext.SearchTextInput{
				AssetID: "a-6", Source: "stock",
				Title: "Stock Title", Category: "Boxe",
				Tags: []string{"boxing"},
				Additional: map[string]string{
					"event": "Event", "round": "1",
					"subject": "Fighter", "action": "moves",
				},
			},
			want: []string{"Stock Title", "Boxe", "Stock video from Event round 1", "Fighter moves", "boxing"},
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
	for _, s := range []Strategy{youtubeStrategy, artlistStrategy, voiceoverStrategy, imageStrategy, generatedImageStrategy, stockChunkStrategy} {
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
		Source:           string(asset.SourceImage),
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

// ── Enhanced YouTube strategy tests (PR-YT-DOD-10) ────────────────────

// TestYoutubeStrategy_NilAdditional_NoPanic pins the no-panic contract
// when Additional is nil (the strategy reads add["x"] which returns ""
// for nil maps in Go — but the test locks this for future drift).
func TestYoutubeStrategy_NilAdditional_NoPanic(t *testing.T) {
	input := appsearchtext.SearchTextInput{
		AssetID:    "yt-nil-1",
		Source:     "youtube",
		Title:      "Title only",
		Additional: nil,
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil Additional must not panic; got %v", r)
		}
	}()
	got := youtubeStrategy(input)
	if got != "Title only" {
		t.Errorf("nil Additional with title only: got %q", got)
	}
}

// TestYoutubeStrategy_FullBronerPacquiaoClip pins the canonical
// PR-YT-DOD-10 contract: the search_text for a YouTube clip MUST
// contain title, summary (=Description), hook, topics (=Tags),
// transcript, speakers, and mentioned_people — NOT
// just the filename.
//
// This mirrors the real Broner-Pacquiao clip (vdC5GXxS-qU [146-155])
// that the 12-DoD E2E test exercises.
func TestYoutubeStrategy_FullBronerPacquiaoClip(t *testing.T) {
	input := appsearchtext.SearchTextInput{
		AssetID:     "yt_vdC5GXxS-qU_146_155_v1",
		Source:      "youtube",
		Title:       "Sfuriata di Broner contro Pacquiao",
		Description: "Broner urla a Pacquiao: Pensa a me, non a Floyd!",
		Transcript:  "I'm gonna whoop your ass! Don't worry about Floyd, worry about me!",
		Channel:     "SHOWTIME Sports",
		Tags:        []string{"boxing", "trash talk", "press conference"},
		Additional: map[string]string{
			"hook":             "Ti sto per spaccare il culo, non preoccuparti di Floyd! Pensa a me!",
			"speakers":         "Adrien Broner Manny Pacquiao",
			"mentioned_people": "Floyd Mayweather",
		},
	}
	got := youtubeStrategy(input)

	mustContainAll(t, got,
		"Sfuriata di Broner contro Pacquiao",
		"Broner urla a Pacquiao: Pensa a me, non a Floyd!",
		"Ti sto per spaccare il culo, non preoccuparti di Floyd! Pensa a me!",
		"boxing trash talk press conference",
		"I'm gonna whoop your ass! Don't worry about Floyd, worry about me!",
		"Adrien Broner Manny Pacquiao",
		"Floyd Mayweather",
		"SHOWTIME Sports",
	)

	// Must NOT be just the filename.
	if strings.HasPrefix(got, "yt_vdC5GXxS-qU_146_155_v1") {
		t.Errorf("search_text must NOT be just the filename (DoD 10 contract); got prefix match on clip ID: %q", got)
	}
}

// TestYoutubeStrategy_AdditionalFieldsOnly tests the case where only
// Additional fields are populated — the strategy must compose them
// without top-level fields.
func TestYoutubeStrategy_AdditionalFieldsOnly(t *testing.T) {
	input := appsearchtext.SearchTextInput{
		AssetID: "yt-addonly-1",
		Source:  "youtube",
		Additional: map[string]string{
			"hook": "Check this out!",
		},
	}
	got := youtubeStrategy(input)
	mustContainAll(t, got,
		"Check this out!",
	)
}
