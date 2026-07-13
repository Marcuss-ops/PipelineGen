package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"go.uber.org/zap"
)

type recordingSearchPort struct {
	lastQuery    string
	lastLimit    int
	lastLanguage string
	results      []SemanticSearchResult
	err          error
}

func (f *recordingSearchPort) SearchByText(_ context.Context, query string, limit int, language string) ([]SemanticSearchResult, error) {
	f.lastQuery = query
	f.lastLimit = limit
	f.lastLanguage = language
	return f.results, f.err
}

type stubTextTrackReader struct {
	tracks map[string]*asset.TextTrack
}

func (s *stubTextTrackReader) FindReady(_ context.Context, assetID, languageCode string, kind asset.TextTrackKind) (*asset.TextTrack, []asset.TimedCue, error) {
	if kind != asset.TextTrackTranscript {
		return nil, nil, nil
	}
	key := assetID + ":" + languageCode
	if track, ok := s.tracks[key]; ok {
		return track, nil, nil
	}
	return nil, nil, nil
}

func (s *stubTextTrackReader) ListReadyLanguages(_ context.Context, assetID string, kind asset.TextTrackKind) ([]string, error) {
	if kind != asset.TextTrackTranscript {
		return nil, nil
	}
	var langs []string
	for key := range s.tracks {
		if strings.HasPrefix(key, assetID+":") {
			parts := strings.SplitN(key, ":", 2)
			if len(parts) == 2 {
				langs = append(langs, parts[1])
			}
		}
	}
	return langs, nil
}

func makeTrack(assetID, language, text string) *asset.TextTrack {
	hash := fmt.Sprintf("%x", len(text))
	return &asset.TextTrack{
		AssetID:       assetID,
		LanguageCode:  language,
		TextKind:      asset.TextTrackTranscript,
		TextContent:   text,
		SourceType:    asset.TextSourceProvided,
		ModelVersion:  "test",
		TextHash:      hash,
		SourceVersion: "test",
		Status:        asset.TextTrackReady,
	}
}

func TestSearchResolver_RealBuilder_DedupsAndHydratesInSearchOrder(t *testing.T) {
	t.Parallel()

	search := &recordingSearchPort{
		results: []SemanticSearchResult{
			{ClipID: "  clip-b  ", Name: "Round 2", Score: 0.92},
			{ClipID: "", Name: "blank", Score: 0.50},
			{ClipID: "clip-a", Name: "Round 1", Score: 0.91},
			{ClipID: "clip-b", Name: "duplicate", Score: 0.40},
			{ClipID: "clip-c", Name: "Round 3", Score: 0.89},
		},
	}

	clipResolver := newFakeClipResolver()
	clipResolver.AddClip(makeTestClip("clip-a", "Alpha Round 1", 1))
	clipResolver.AddClip(makeTestClip("clip-b", "Alpha Round 2", 2))
	clipResolver.AddClip(makeTestClip("clip-c", "Alpha Round 3", 3))

	builder := NewClipSourceBuilder(clipResolver, nil, zap.NewNop())
	builder.ConfigureTextTrackReader(&stubTextTrackReader{
		tracks: map[string]*asset.TextTrack{
			"clip-a:en": makeTrack("clip-a", "en", "transcript clip-a"),
			"clip-b:en": makeTrack("clip-b", "en", "transcript clip-b"),
			"clip-c:en": makeTrack("clip-c", "en", "transcript clip-c"),
		},
	})

	resolver := NewSearchSourceResolver(search, builder, NewClipSamplerRegistry(), zap.NewNop())

	resolved, err := resolver.Resolve(context.Background(), scriptpkg.SourceSpec{
		Type:               scriptpkg.SourceSearch,
		Query:              "   Manny Pacquiao Adrien Broner full fight   ",
		MaxClips:           5,
		GroundingPolicy:    scriptpkg.GroundingPolicyClipsPrimary,
		OrderingStrategy:   "chronological",
		TranscriptPolicy:   "canonical",
		MinCoverage:        0,
		MinQualityScore:    ptrFloat64(0.1),
		MinTranscriptWords: ptrInt(5),
	}, scriptpkg.SourceResolutionContext{
		ItemID:      "item-search",
		Title:       "Search title",
		Language:    "en",
		Tone:        "documentary",
		Model:       "gemma4:e4b",
		Style:       "Follow the evidence chronologically.",
		TargetWords: 700,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got, want := search.lastQuery, "Manny Pacquiao Adrien Broner full fight"; got != want {
		t.Fatalf("search query: got %q, want %q", got, want)
	}
	if got, want := search.lastLimit, 5; got != want {
		t.Fatalf("search limit: got %d, want %d", got, want)
	}
	if got, want := search.lastLanguage, "en"; got != want {
		t.Fatalf("search language: got %q, want %q", got, want)
	}

	wantIDs := []string{"clip-b", "clip-a", "clip-c"}
	if got := clipResolver.mediaCalls; !slicesEqual(got, wantIDs) {
		t.Fatalf("hydration lookup order: got %v, want %v", got, wantIDs)
	}

	if resolved.Type != scriptpkg.SourceSearch {
		t.Fatalf("resolved type: got %q, want %q", resolved.Type, scriptpkg.SourceSearch)
	}
	if resolved.Title != "Search title" || resolved.Topic != "Search title" {
		t.Fatalf("resolved title/topic: got title=%q topic=%q", resolved.Title, resolved.Topic)
	}
	if resolved.GroundingPolicy != scriptpkg.GroundingPolicyClipsPrimary {
		t.Fatalf("grounding policy: got %q, want %q", resolved.GroundingPolicy, scriptpkg.GroundingPolicyClipsPrimary)
	}

	if len(resolved.SearchResults) != 3 {
		t.Fatalf("search results: got %d, want 3", len(resolved.SearchResults))
	}
	for i, want := range wantIDs {
		if resolved.SearchResults[i].ClipID != want {
			t.Fatalf("search results[%d].ClipID: got %q, want %q", i, resolved.SearchResults[i].ClipID, want)
		}
		if resolved.SearchResults[i].Source != "semantic" {
			t.Fatalf("search results[%d].Source: got %q, want %q", i, resolved.SearchResults[i].Source, "semantic")
		}
	}

	ev := resolved.ClipEvidence
	if ev == nil {
		t.Fatal("expected clip evidence")
	}
	if !slicesEqual(ev.AcceptedClipIDs, wantIDs) {
		t.Fatalf("accepted clip IDs: got %v, want %v", ev.AcceptedClipIDs, wantIDs)
	}
	if ev.ClipCount != len(wantIDs) {
		t.Fatalf("clip count: got %d, want %d", ev.ClipCount, len(wantIDs))
	}
	if ev.LanguageCode != "en" {
		t.Fatalf("language code: got %q, want %q", ev.LanguageCode, "en")
	}
	if ev.TranscriptHash == "" {
		t.Fatal("expected transcript hash to be populated")
	}

	if idx := strings.Index(ev.AssembledText, "CLIP clip-b:"); idx < 0 {
		t.Fatalf("assembled text missing clip-b block: %q", ev.AssembledText)
	} else {
		idxA := strings.Index(ev.AssembledText, "CLIP clip-a:")
		idxC := strings.Index(ev.AssembledText, "CLIP clip-c:")
		if !(idx < idxA && idxA < idxC) {
			t.Fatalf("assembled text order incorrect: clip-b=%d clip-a=%d clip-c=%d", idx, idxA, idxC)
		}
	}
}

func TestSearchResolver_MinCoverageFailsClosed(t *testing.T) {
	t.Parallel()

	search := &recordingSearchPort{
		results: []SemanticSearchResult{
			{ClipID: "clip-a", Name: "Round 1", Score: 0.9},
			{ClipID: "clip-b", Name: "Round 2", Score: 0.8},
		},
	}

	clipResolver := newFakeClipResolver()
	clipResolver.AddClip(makeTestClip("clip-a", "Alpha Round 1", 1))
	clipResolver.AddClip(makeTestClip("clip-b", "Alpha Round 2", 2))

	builder := NewClipSourceBuilder(clipResolver, nil, zap.NewNop())
	builder.ConfigureTextTrackReader(&stubTextTrackReader{
		tracks: map[string]*asset.TextTrack{
			"clip-a:en": makeTrack("clip-a", "en", "transcript clip-a"),
			"clip-b:en": makeTrack("clip-b", "en", "transcript clip-b"),
		},
	})

	resolver := NewSearchSourceResolver(search, builder, NewClipSamplerRegistry(), zap.NewNop())

	_, err := resolver.Resolve(context.Background(), scriptpkg.SourceSpec{
		Type:        scriptpkg.SourceSearch,
		Query:       "e2e_fight_alpha complete fight from opening round to verdict",
		MaxClips:    10,
		MinCoverage: 0.5,
	}, scriptpkg.SourceResolutionContext{
		ItemID:   "item-search-low-coverage",
		Language: "en",
	})
	if err == nil {
		t.Fatal("expected coverage error")
	}
	var srcErr *scriptpkg.SourceResolutionError
	if !errors.As(err, &srcErr) {
		t.Fatalf("expected SourceResolutionError, got %T: %v", err, err)
	}
	if srcErr.ResultCount != 2 {
		t.Fatalf("coverage error result count: got %d, want 2", srcErr.ResultCount)
	}
	if len(clipResolver.mediaCalls) != 0 {
		t.Fatalf("builder should not have been called on coverage failure, got resolver calls %v", clipResolver.mediaCalls)
	}
	if !strings.Contains(srcErr.Inner.Error(), "coverage") {
		t.Fatalf("coverage error inner message missing coverage detail: %v", srcErr.Inner)
	}
}

func ptrFloat64(v float64) *float64 { return &v }

func ptrInt(v int) *int { return &v }

func slicesEqual[T comparable](a, b []T) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
