package localization

// planbuilder_test.go — the "script in N languages, subtitles correct per
// language" certificate at the boundary where that fact is decided.
//
// The plan builder is the ONLY place that answers "which concrete subtitle
// track does language X burn" (production wires it at
// internal/app/wiring/localization_service.go). Until now it was covered only
// incidentally by the overlay-reuse cases, so nothing pinned the per-language
// routing itself: a builder that assigned the SOURCE transcript to every target
// (the classic "Italian variant with English subtitles") would have passed the
// whole suite. These cases pin the routing hermetically — no GPU, no DB.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// dollyTargetLanguages mirrors the canonical multilingual request for the Dolly
// Parton clip batch (source "en"): the target set the runtime certificate
// asserts on, kept here so the hermetic routing gate covers the same languages.
var dollyTargetLanguages = []string{"it", "es", "de", "fr"}

// languageTrackStore is the fixture's READY text-track table: one track per
// language, each with its OWN (TrackID, SHA256). Keying the store by language
// (and not by "whatever the caller asked for") is what makes a routing bug
// observable — the reference handed back identifies one language only.
type languageTrackStore struct {
	mu    sync.Mutex
	refs  map[string]TrackRef
	calls map[string]int
	order []string
}

func newLanguageTrackStore(languages ...string) *languageTrackStore {
	store := &languageTrackStore{refs: map[string]TrackRef{}, calls: map[string]int{}}
	for i, lang := range languages {
		store.refs[lang] = TrackRef{
			TrackID: int64(100 + i*100),
			SHA256:  fmt.Sprintf("%064d", i+1),
		}
	}
	return store
}

func (s *languageTrackStore) ResolveTrack(_ context.Context, _ string, language string, kind detail.TextTrackKind) (*TrackRef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls[language]++
	s.order = append(s.order, language)
	ref, ok := s.refs[language]
	if !ok {
		return nil, fmt.Errorf("no READY %s track for language %q", kind, language)
	}
	return &TrackRef{TrackID: ref.TrackID, SHA256: ref.SHA256}, nil
}

func (s *languageTrackStore) callsFor(language string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[language]
}

func (s *languageTrackStore) ref(language string) TrackRef {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refs[language]
}

// dollySourceInput is the resolved source-clip identity the builder fans out
// from: an English clip with everything a plan must fingerprint.
func dollySourceInput() SourceInput {
	return SourceInput{
		JobID:             "job-dolly-parton",
		SceneID:           "scene-1",
		AssetID:           "source-asset-1",
		SourceLanguage:    "en",
		SourceSHA256:      strings.Repeat("a", 64),
		DurationMS:        8432,
		OutputProfileHash: "profile-sha",
		RendererVersion:   "renderer-v1",
		SubtitleStyleHash: "style-sha",
	}
}

func dollyLanguageRequests(languages []string) []LanguageRequest {
	requests := make([]LanguageRequest, 0, len(languages))
	for i, lang := range languages {
		requests = append(requests, LanguageRequest{Language: lang, Priority: i})
	}
	return requests
}

func mustDollyPlanBuilder(t *testing.T, store *languageTrackStore) PlanBuilder {
	t.Helper()
	builder, err := NewLocalizationPlanBuilder(store)
	if err != nil {
		t.Fatalf("NewLocalizationPlanBuilder: %v", err)
	}
	return builder
}

// TestPlanBuilder_RoutesEachLanguageToItsOwnSubtitleTrack is the core
// certificate: every requested language gets a plan that renders THAT language
// and burns THAT language's OWN track — never the source transcript, and never
// another target's track. The source transcript is resolved exactly once and
// shared by all N plans (it identifies the audio), which is also the difference
// between N translations and N redundant lookups.
func TestPlanBuilder_RoutesEachLanguageToItsOwnSubtitleTrack(t *testing.T) {
	languages := append([]string{"en"}, dollyTargetLanguages...)
	store := newLanguageTrackStore(languages...)
	plans, err := mustDollyPlanBuilder(t, store).Build(context.Background(), dollySourceInput(), dollyLanguageRequests(languages))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plans) != len(languages) {
		t.Fatalf("plans = %d, want %d (one per requested language, request order)", len(plans), len(languages))
	}

	subtitleRefs := make(map[int64]string, len(languages))
	for i, lang := range languages {
		plan := plans[i]
		if plan.TargetLanguage != lang {
			t.Errorf("plan[%d].TargetLanguage = %q, want %q (request order is editorial and must survive)", i, plan.TargetLanguage, lang)
		}
		if plan.Priority != i {
			t.Errorf("plan[%d].Priority = %d, want %d", i, plan.Priority, i)
		}
		want := store.ref(lang)
		if plan.SubtitleTrackID != want.TrackID || plan.SubtitleSHA256 != want.SHA256 {
			t.Errorf("language %s burns track (%d, %q), want its own (%d, %q)",
				lang, plan.SubtitleTrackID, plan.SubtitleSHA256, want.TrackID, want.SHA256)
		}
		if _, dup := subtitleRefs[plan.SubtitleTrackID]; dup {
			t.Errorf("language %s shares subtitle track %d with another language", lang, plan.SubtitleTrackID)
		}
		subtitleRefs[plan.SubtitleTrackID] = lang

		source := store.ref("en")
		if plan.TranscriptTrackID != source.TrackID || plan.TranscriptSHA256 != source.SHA256 {
			t.Errorf("language %s: transcript ref (%d, %q), want the source transcript (%d, %q)",
				lang, plan.TranscriptTrackID, plan.TranscriptSHA256, source.TrackID, source.SHA256)
		}
	}

	// One resolution per language — the source transcript is NOT re-resolved per
	// target, and no language is resolved twice.
	for _, lang := range languages {
		if calls := store.callsFor(lang); calls != 1 {
			t.Errorf("track resolutions for %s = %d, want 1", lang, calls)
		}
	}
	if len(store.order) != len(languages) {
		t.Errorf("total track resolutions = %d, want %d", len(store.order), len(languages))
	}
}

// TestPlanBuilder_SourceLanguageBurnsItsTranscript pins the source-language
// rule: for the language the clip is already in, the subtitle track IS the
// transcript — no translated track is invented, and none is required to exist.
func TestPlanBuilder_SourceLanguageBurnsItsTranscript(t *testing.T) {
	// The store deliberately has NO translated tracks at all: a source-only
	// request must still build, which it cannot if the builder asks the store to
	// translate the source into itself.
	store := newLanguageTrackStore("en")
	plans, err := mustDollyPlanBuilder(t, store).Build(context.Background(), dollySourceInput(), []LanguageRequest{{Language: "en", Priority: 0}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("plans = %d, want 1", len(plans))
	}
	if plans[0].SubtitleTrackID != plans[0].TranscriptTrackID || plans[0].SubtitleSHA256 != plans[0].TranscriptSHA256 {
		t.Fatalf("source language must burn its transcript: subtitle (%d, %q) != transcript (%d, %q)",
			plans[0].SubtitleTrackID, plans[0].SubtitleSHA256, plans[0].TranscriptTrackID, plans[0].TranscriptSHA256)
	}
	if refs := store.callsFor("en"); refs != 1 {
		t.Fatalf("source transcript resolutions = %d, want 1", refs)
	}
}

// TestPlanBuilder_CanonicalSourceLanguageStillBurnsItsTranscript verifies the
// source-language rule is decided by the canonical BCP-47 language, not by raw
// string equality: a request written "EN" is the SAME language as the source
// "en" and must reuse the transcript. Comparing the raw strings instead made the
// builder demand a translated track for the source language and abort the whole
// fan-out when the store (correctly) had none.
func TestPlanBuilder_CanonicalSourceLanguageStillBurnsItsTranscript(t *testing.T) {
	for _, variant := range []string{"EN", " en ", "en"} {
		store := newLanguageTrackStore("en")
		plans, err := mustDollyPlanBuilder(t, store).Build(context.Background(), dollySourceInput(), []LanguageRequest{{Language: variant, Priority: 0}})
		if err != nil {
			t.Fatalf("Build(%q): %v", variant, err)
		}
		if len(plans) != 1 {
			t.Fatalf("Build(%q): plans = %d, want 1", variant, len(plans))
		}
		if plans[0].SubtitleTrackID != plans[0].TranscriptTrackID {
			t.Fatalf("Build(%q): subtitle track %d must be the transcript %d",
				variant, plans[0].SubtitleTrackID, plans[0].TranscriptTrackID)
		}
	}
}

// TestPlanBuilder_MissingTargetTrackAbortsTheWholeFanOut pins the fail-closed
// contract: a target with no READY track produces NO plans at all. A partially
// built fan-out would render some languages and silently drop the rest — the
// failure mode where a job reports success and a language is simply missing.
func TestPlanBuilder_MissingTargetTrackAbortsTheWholeFanOut(t *testing.T) {
	store := newLanguageTrackStore("en", "it", "es") // "de" missing
	plans, err := mustDollyPlanBuilder(t, store).Build(context.Background(), dollySourceInput(), dollyLanguageRequests([]string{"it", "es", "de"}))
	if err == nil {
		t.Fatal("Build must fail closed when a requested language has no READY track")
	}
	if plans != nil {
		t.Fatalf("a failed build must return no plans, got %d", len(plans))
	}
	if !strings.Contains(err.Error(), "de") {
		t.Fatalf("error must name the unresolved language, got %v", err)
	}
}

// TestPlanBuilder_MissingSourceTranscriptAbortsBuild verifies the source
// transcript is a hard prerequisite: without it no language can be localized.
func TestPlanBuilder_MissingSourceTranscriptAbortsBuild(t *testing.T) {
	store := newLanguageTrackStore("it", "es") // no "en" transcript
	plans, err := mustDollyPlanBuilder(t, store).Build(context.Background(), dollySourceInput(), dollyLanguageRequests([]string{"it", "es"}))
	if err == nil {
		t.Fatal("Build must fail closed when the source transcript is unresolved")
	}
	if plans != nil {
		t.Fatalf("a failed build must return no plans, got %d", len(plans))
	}
}

// unresolvedTrackResolver fails every resolution with a transport-style error,
// distinct from the "no READY track" case above.
type unresolvedTrackResolver struct{ err error }

func (r unresolvedTrackResolver) ResolveTrack(context.Context, string, string, detail.TextTrackKind) (*TrackRef, error) {
	return nil, r.err
}

// TestPlanBuilder_WrapsResolverFailure verifies a track-store failure surfaces
// as an error naming the language, instead of a zero-value reference that would
// later fingerprint as a (0, "") subtitle track.
func TestPlanBuilder_WrapsResolverFailure(t *testing.T) {
	boom := errors.New("text-track store unavailable")
	builder, err := NewLocalizationPlanBuilder(unresolvedTrackResolver{err: boom})
	if err != nil {
		t.Fatalf("NewLocalizationPlanBuilder: %v", err)
	}
	plans, err := builder.Build(context.Background(), dollySourceInput(), dollyLanguageRequests([]string{"it"}))
	if !errors.Is(err, boom) {
		t.Fatalf("Build error = %v, want it to wrap %v", err, boom)
	}
	if plans != nil {
		t.Fatalf("a failed build must return no plans, got %d", len(plans))
	}
}

// TestPlanBuilder_RequiresTrackResolver pins construction-time fail-closed: a
// builder with no resolver can never answer "which subtitles", so it is refused
// up front rather than at the first render.
func TestPlanBuilder_RequiresTrackResolver(t *testing.T) {
	if _, err := NewLocalizationPlanBuilder(nil); err == nil {
		t.Fatal("NewLocalizationPlanBuilder(nil) must be rejected")
	}
}

// builtPlanSubtitleResolver resolves a plan's subtitle track back through the
// same per-language store, so a plan that referenced another language's track
// surfaces at the wire as a language/track contradiction.
type builtPlanSubtitleResolver struct {
	languages map[int64]string
	hashes    map[int64]string
}

func newBuiltPlanSubtitleResolver(store *languageTrackStore, languages []string) *builtPlanSubtitleResolver {
	resolver := &builtPlanSubtitleResolver{languages: map[int64]string{}, hashes: map[int64]string{}}
	for _, lang := range languages {
		ref := store.ref(lang)
		resolver.languages[ref.TrackID] = lang
		resolver.hashes[ref.TrackID] = ref.SHA256
	}
	return resolver
}

func (r *builtPlanSubtitleResolver) ResolveSubtitleTrack(_ context.Context, trackID int64, expectedSHA256 string) (*ResolvedSubtitleTrack, error) {
	lang, ok := r.languages[trackID]
	if !ok {
		return nil, fmt.Errorf("text track %d not found", trackID)
	}
	if r.hashes[trackID] != expectedSHA256 {
		return nil, fmt.Errorf("text track %d hash mismatch", trackID)
	}
	return &ResolvedSubtitleTrack{
		TrackID:      trackID,
		LanguageCode: lang,
		Cues:         []detail.TimedCue{{StartMs: 0, EndMs: 900, Text: "sub " + lang}},
		TextHash:     expectedSHA256,
	}, nil
}

// TestPlanBuilder_BuiltPlansBurnTheirOwnLanguage is the end-to-end hermetic
// certificate the runtime test cannot give without a GPU: the plans the builder
// PRODUCES are fed to the REAL subtitle wire, and each language compiles its OWN
// .ass artifact whose identity carries that language. Distinct paths prove N
// languages do not overwrite one another's subtitles; an artifact path carrying
// another language's tag would mean the burned subtitles belong to the wrong
// script.
func TestPlanBuilder_BuiltPlansBurnTheirOwnLanguage(t *testing.T) {
	languages := append([]string{"en"}, dollyTargetLanguages...)
	store := newLanguageTrackStore(languages...)
	plans, err := mustDollyPlanBuilder(t, store).Build(context.Background(), dollySourceInput(), dollyLanguageRequests(languages))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	compiler := &languageDerivedSubtitleCompiler{}
	wire, err := NewSubtitleWire(newBuiltPlanSubtitleResolver(store, languages), compiler, "/tmp/subtitles")
	if err != nil {
		t.Fatalf("NewSubtitleWire: %v", err)
	}

	paths := make(map[string]string, len(plans))
	for _, plan := range plans {
		asset, err := wire.Wire(context.Background(), plan)
		if err != nil {
			t.Fatalf("language %s: Wire: %v", plan.TargetLanguage, err)
		}
		if !strings.HasSuffix(asset.LocalPath, "."+plan.TargetLanguage+".ass") {
			t.Fatalf("language %s: burned artifact %q must be that language's ASS", plan.TargetLanguage, asset.LocalPath)
		}
		if owner, dup := paths[asset.LocalPath]; dup {
			t.Fatalf("languages %s and %s share the subtitle artifact %q", owner, plan.TargetLanguage, asset.LocalPath)
		}
		paths[asset.LocalPath] = plan.TargetLanguage
	}
	if len(compiler.inputs) != len(plans) {
		t.Fatalf("ASS compilations = %d, want %d (one per language)", len(compiler.inputs), len(plans))
	}
}
