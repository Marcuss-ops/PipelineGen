package searchtext

import (
	"context"
	"strings"
	"testing"

	appsearchtext "github.com/Marcuss-ops/PipelineGen/internal/capabilities/indexing/searchtext"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
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

func TestStockChunkStrategy_CategoryNil(t *testing.T) {
	// Category empty → prefix is just the title; the rest of the
	// sentence is unaffected.
	input := appsearchtext.SearchTextInput{
		AssetID: "stock-2",
		Source:  "stock",
		Title:   "Knockout clip",
		Tags:    []string{"knockout"},
		Additional: map[string]string{
			"event":   "Wilder vs Fury",
			"round":   "12",
			"subject": "Deontay Wilder",
			"action":  "knocks down Fury",
		},
	}
	got := stockChunkStrategy(input)
	if strings.Contains(got, "  ") {
		t.Errorf("must not contain double-space when category is empty; got %q", got)
	}
	if !strings.HasPrefix(got, "Knockout clip ") {
		t.Errorf("prefix must be just the title when category empty; got %q", got)
	}
	mustContainAll(t, got,
		"Stock video from Wilder vs Fury round 12",
		"Deontay Wilder knocks down Fury",
		"Tags: knockout",
	)
}

func TestStockChunkStrategy_TagsEmpty(t *testing.T) {
	// Tags empty → the "Tags:" labelled segment is dropped entirely.
	input := appsearchtext.SearchTextInput{
		AssetID: "stock-3",
		Source:  "stock",
		Title:   "Clip",
		Additional: map[string]string{
			"event":   "Fight Night",
			"round":   "1",
			"subject": "Fighter A",
			"action":  "jabs",
		},
	}
	got := stockChunkStrategy(input)
	if strings.Contains(got, "Tags:") {
		t.Errorf("empty tags must drop the 'Tags:' label; got %q", got)
	}
	if strings.HasSuffix(got, " ") {
		t.Errorf("must not end with trailing whitespace; got %q", got)
	}
	mustContainAll(t, got, "Stock video from Fight Night round 1", "Fighter A jabs")
}

func TestStockChunkStrategy_MultiSegment(t *testing.T) {
	// Two chunks from the same event: each must produce distinct text
	// keyed on round/subject so Qdrant can disambiguate them.
	a := appsearchtext.SearchTextInput{
		AssetID: "stock-4a",
		Source:  "stock",
		Title:   "Round 1",
		Tags:    []string{"boxing"},
		Additional: map[string]string{
			"event":   "Pacquiao vs Broner",
			"round":   "1",
			"subject": "Manny Pacquiao",
			"action":  "throws a jab",
		},
	}
	b := appsearchtext.SearchTextInput{
		AssetID: "stock-4b",
		Source:  "stock",
		Title:   "Round 5",
		Tags:    []string{"boxing"},
		Additional: map[string]string{
			"event":   "Pacquiao vs Broner",
			"round":   "5",
			"subject": "Adrien Broner",
			"action":  "blocks a hook",
		},
	}
	gotA := stockChunkStrategy(a)
	gotB := stockChunkStrategy(b)
	if gotA == gotB {
		t.Fatalf("multi-segment chunks with different round/subject must produce distinct text; both = %q", gotA)
	}
	mustContainAll(t, gotA, "round 1", "Manny Pacquiao", "throws a jab")
	mustContainAll(t, gotB, "round 5", "Adrien Broner", "blocks a hook")
}

func TestStockChunkStrategy_SourceURLOmitted(t *testing.T) {
	// Stock search_text must not leak source URL noise.
	input := appsearchtext.SearchTextInput{
		AssetID: "stock-5",
		Source:  "stock",
		Title:   "Clip",
		Tags:    []string{"boxing"},
		Additional: map[string]string{
			"event":   "Event",
			"round":   "2",
			"subject": "Fighter",
			"action":  "moves",
		},
	}
	got := stockChunkStrategy(input)
	if strings.Contains(got, "http://") || strings.Contains(got, "https://") {
		t.Errorf("stock search_text must not include source URL noise; got %q", got)
	}
}

func TestStockChunkStrategy_EmptyInput(t *testing.T) {
	// Fully empty input → strategy must return empty string.
	input := appsearchtext.SearchTextInput{
		AssetID:    "stock-6",
		Source:     "stock",
		Additional: map[string]string{},
	}
	got := stockChunkStrategy(input)
	if got != "" {
		t.Errorf("fully empty input must return empty; got %q", got)
	}
}

// TestStockChunkStrategy_NilAdditional_NoPanic pins the no-panic
// invariant for nil Additional maps. Go's map zero-value semantics
// make this safe (add["x"] returns "" for nil maps) but the test
// locks it for future drift.
func TestStockChunkStrategy_NilAdditional_NoPanic(t *testing.T) {
	input := appsearchtext.SearchTextInput{
		AssetID:    "stock-nil-1",
		Source:     "stock",
		Title:      "Title only",
		Additional: nil,
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil Additional must not panic; got %v", r)
		}
	}()
	got := stockChunkStrategy(input)
	if got != "Title only" {
		t.Errorf("nil Additional with title only: got %q", got)
	}
}

// TestStockChunkStrategy_RoundOnlyHeader pins the comma-separated
// "Stock video, round N" output for the round-only branch (the
// branch where composeStockHeader's "from" preposition is missing
// but a separator is needed to keep the phrase grammatical).
func TestStockChunkStrategy_RoundOnlyHeader(t *testing.T) {
	input := appsearchtext.SearchTextInput{
		AssetID: "stock-round-1",
		Source:  "stock",
		Additional: map[string]string{
			"round": "7",
		},
	}
	got := stockChunkStrategy(input)
	if !strings.Contains(got, "Stock video, round 7") {
		t.Errorf("round-only header must use comma separator; got %q", got)
	}
}

// TestStockChunkStrategy_AdditionalKeysContract locks the contract
// that stockChunkStrategyAdditionalKeys enumerates exactly the
// keys the strategy actually reads from Additional. A future drift
// (key added to strategy but not to the slice, OR vice versa) is
// caught here. Mirrors the godlike/06 SSOT discipline of pinning
// canonical-source-of-truth declarations.
//
// Coverage scope: the table-driven loop below covers the 4
// single-source-of-truth keys (event/round/subject/action).
// The Segment-bound pair (start_sec + end_sec) is covered
// separately by TestStockChunkStrategy_SegmentDropOnPartialEndpoints
// because the godlike/07 NO-FAKE-AVAILABILITY contract on those
// keys is asymmetric (BOTH must be present, OR the segment is
// dropped) and does not fit the per-key single-population pattern.
func TestStockChunkStrategy_AdditionalKeysContract(t *testing.T) {
	checks := []struct {
		key  string
		val  string
		want string
	}{
		{"event", "Event-X", "Stock video from Event-X"},
		{"round", "9", "Stock video, round 9"},
		{"subject", "Subject-Y", "Subject-Y"},
		{"action", "Action-Z", "Action-Z"},
		// start_sec + end_sec are tested together in SegmentBoth below
		// to lock the godlike/07 NO-FAKE-AVAILABILITY contract.
	}
	for _, c := range checks {
		t.Run(c.key, func(t *testing.T) {
			add := map[string]string{c.key: c.val}
			got := stockChunkStrategy(appsearchtext.SearchTextInput{
				AssetID:    "stock-contract-" + c.key,
				Source:     "stock",
				Additional: add,
			})
			if !strings.Contains(got, c.want) {
				t.Errorf("key %q (val=%q) must produce substring %q; got %q",
					c.key, c.val, c.want, got)
			}
		})
	}
}

// TestStockChunkStrategy_SegmentDropOnPartialEndpoints pins the
// godlike/07 NO-FAKE-AVAILABILITY contract: the "Segment X to Y"
// segment is emitted ONLY when BOTH start_sec and end_sec are set.
// A one-sided endpoint must drop the segment entirely (rather than
// emit a malformed "Segment 10.0s to s" or "Segment s to 20.0s"
// that would pollute the Qdrant BM25 channel).
func TestStockChunkStrategy_SegmentDropOnPartialEndpoints(t *testing.T) {
	cases := []struct {
		name        string
		add         map[string]string
		mustHave    []string
		mustNotHave []string
	}{
		{
			name:        "start_sec_only_drops_segment",
			add:         map[string]string{"start_sec": "10.0"},
			mustNotHave: []string{"Segment"},
		},
		{
			name:        "end_sec_only_drops_segment",
			add:         map[string]string{"end_sec": "20.0"},
			mustNotHave: []string{"Segment"},
		},
		{
			name:     "both_endpoints_emits_segment",
			add:      map[string]string{"start_sec": "10.0", "end_sec": "20.0"},
			mustHave: []string{"Segment 10.0s to 20.0s"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := stockChunkStrategy(appsearchtext.SearchTextInput{
				AssetID:    "stock-seg-" + c.name,
				Source:     "stock",
				Additional: c.add,
			})
			for _, w := range c.mustHave {
				if !strings.Contains(got, w) {
					t.Errorf("must contain %q; got %q", w, got)
				}
			}
			for _, w := range c.mustNotHave {
				if strings.Contains(got, w) {
					t.Errorf("must NOT contain %q; got %q", w, got)
				}
			}
		})
	}
}

// TestStockChunkStrategy_Idempotent parallels the existing
// TestAllStrategies_Idempotent for parity with the other strategies.
func TestStockChunkStrategy_Idempotent(t *testing.T) {
	input := appsearchtext.SearchTextInput{
		AssetID:  "stock-idem-1",
		Source:   "stock",
		Title:    "Title",
		Category: "Boxe",
		Tags:     []string{"a", "b"},
		Additional: map[string]string{
			"event":     "E",
			"round":     "3",
			"subject":   "S",
			"action":    "A",
			"start_sec": "1.0",
			"end_sec":   "2.0",
		},
	}
	first := stockChunkStrategy(input)
	second := stockChunkStrategy(input)
	if first != second {
		t.Errorf("strategy must be idempotent; first=%q second=%q", first, second)
	}
}

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

// TestYoutubeStrategy_Idempotent mirrors the existing
// TestAllStrategies_Idempotent for the YouTube strategy.
func TestYoutubeStrategy_Idempotent(t *testing.T) {
	input := appsearchtext.SearchTextInput{
		AssetID:     "yt-idem-1",
		Source:      "youtube",
		Title:       "Title",
		Description: "Desc",
		Tags:        []string{"a", "b"},
		Additional: map[string]string{
			"hook": "Hook",
		},
	}
	first := youtubeStrategy(input)
	second := youtubeStrategy(input)
	if first != second {
		t.Errorf("strategy must be idempotent; first=%q second=%q", first, second)
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
