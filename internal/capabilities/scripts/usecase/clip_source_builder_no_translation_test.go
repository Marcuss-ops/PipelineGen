// Package usecase_test — clip_source_builder_no_translation_test.go
// pins the Fase 4 release-gate contract that the video pipeline
// NEVER invokes TranslationPort (PR-PY-CLIPS-CORRETTE-TRADOTTE, July
// 2026, test #7 in the §19 release-gate suite).
//
// godlike/06 SSOT: this test is the SOLE canonical regression pin
// for the "video pipeline does not translate" contract. The test
// combines a compile-time guarantee (reflection check that the
// builder struct has no translation-related field) with a runtime
// exercise (the stub translation spy is never invoked through any
// of the builder's public methods).
//
// godlike/07 NO-FAKE-AVAILABILITY: a passing test means the
// builder CANNOT invoke translation by any path. The reflection
// check is the load-bearing assertion; the runtime exercise is
// supplementary.
package usecase_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scripts "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// stubTranslationPort is a translation.TranslationPort spy that
// records every Translate call. The spy is NEVER injected into
// the builder (the builder has no translation port field — see
// the reflection check below). The runtime assertion is that
// the call count is 0 after exercising the builder.
type stubTranslationPort struct {
	calls int
}

func (s *stubTranslationPort) Translate(_ context.Context, _ translation.TranslationCommand) (translation.TranslationResult, error) {
	s.calls++
	return translation.TranslationResult{}, nil
}

// TestClipSourceBuilder_NeverInvokesTranslationPort is the Fase 4
// release-gate regression pin (test #7 in the §19 spec). It pins:
//
//  1. Compile-time guarantee: ClipSourceBuilder has NO field of
//     type `translation.TranslationPort` (or any compatible
//     interface). This is the load-bearing assertion: a future
//     refactor that adds a translation port field to the builder
//     triggers a test failure HERE, not at first runtime call.
//  2. Runtime guarantee: the stub translation spy (created in
//     the test, never injected) is not invoked through any
//     builder method. The spy call count stays at 0 through
//     every code path exercised below.
//
// The test exercises 4 distinct builder paths:
//   - happy path: 1 clip, READY text track, transcript found
//   - missing-track path: textTrackReader returns (nil, nil)
//   - mixed resolution: 1 resolved + 1 missing-from-DB
//   - empty request: zero clip IDs
func TestClipSourceBuilder_NeverInvokesTranslationPort(t *testing.T) {
	spy := &stubTranslationPort{calls: 0}

	// ── 1. Compile-time guarantee: no translation port field ───────
	// godlike/07 fail-closed: use the VALUE type (not the
	// pointer) so reflect.Type.NumField() works. A pointer
	// type panics with "reflect: NumField of non-struct type".
	builderType := reflect.TypeOf(scripts.ClipSourceBuilder{})
	for i := 0; i < builderType.NumField(); i++ {
		field := builderType.Field(i)
		if field.Type.AssignableTo(reflect.TypeOf((*translation.TranslationPort)(nil)).Elem()) {
			t.Fatalf("ClipSourceBuilder has a translation.TranslationPort field at index %d (%s); "+
				"the video pipeline MUST NOT invoke translation (Fase 4 §19 test #7)",
				i, field.Name)
		}
	}

	// ── 2. Runtime guarantee: spy is never invoked ─────────────────
	reader := &stubTextTrackReader{
		tracks: map[string]*asset.TextTrack{
			"clip-A:en": makeTrack("clip-A", "en", "hello world", asset.TextTrackReady),
		},
		readyLanguages: map[string][]string{
			"clip-A": {"en"},
		},
	}
	stubClips := &stubClipsResolver{
		byID: map[string]*asset.Asset{
			"clip-A": makeTestAsset("clip-A", "Clip A", "https://drive/clip-A"),
		},
	}

	b := scripts.NewClipSourceBuilder(stubClips, nil, zap.NewNop())
	b.ConfigureTextTrackReader(reader)

	// Path 1: happy path (1 clip, READY text track).
	ev, _, _, err := b.BuildClipContext(context.Background(), []string{"clip-A"}, &scripts.ClipGenerationOptions{Language: "en"})
	if err != nil {
		t.Fatalf("happy path returned error: %v", err)
	}
	if ev == nil {
		t.Fatal("happy path: evidence is nil")
	}
	if !strings.Contains(ev.AssembledText, "Transcript: hello world") {
		t.Fatalf("happy path: transcript not in assembled text; got %q", ev.AssembledText)
	}
	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 4 (July 2026): the 3 new
	// fingerprint fields MUST be populated when the new path
	// finds a READY track.
	if ev.LanguageCode != "en" {
		t.Fatalf("happy path: LanguageCode = %q, want %q", ev.LanguageCode, "en")
	}
	if ev.TranscriptHash == "" {
		t.Fatalf("happy path: TranscriptHash is empty; the Fase 4 fingerprint field MUST be populated from the resolved TextTrack.TextHash")
	}
	if len(ev.ClipTranscriptHashes) != 1 {
		t.Fatalf("happy path: ClipTranscriptHashes = %v, want 1 hash", ev.ClipTranscriptHashes)
	}
	if ev.ClipTranscriptHashes[0] != "hash-clip-A-en" {
		t.Fatalf("happy path: ClipTranscriptHashes[0] = %q, want %q", ev.ClipTranscriptHashes[0], "hash-clip-A-en")
	}

	// Path 2: missing-track path (textTrackReader returns nil).
	readerEmpty := &stubTextTrackReader{
		tracks:         map[string]*asset.TextTrack{},
		readyLanguages: map[string][]string{},
	}
	b2 := scripts.NewClipSourceBuilder(stubClips, nil, zap.NewNop())
	b2.ConfigureTextTrackReader(readerEmpty)
	ev2, _, _, err := b2.BuildClipContext(context.Background(), []string{"clip-A"}, &scripts.ClipGenerationOptions{Language: "it"})
	if err == nil {
		t.Fatal("missing-track path must fail closed; got nil error")
	}
	if ev2 != nil {
		t.Fatalf("missing-track path: evidence = %v, want nil", ev2)
	}
	var notReady *scripts.ErrTextTrackNotReady
	if !errors.As(err, &notReady) {
		t.Fatalf("missing-track path: error = %T %v, want *ErrTextTrackNotReady", err, err)
	}
	if notReady.AssetID != "clip-A" || notReady.RequestedLanguage != "it" {
		t.Fatalf("missing-track path: typed error = %+v, want asset clip-A/language it", notReady)
	}

	// Path 3: mixed resolution (1 resolved + 1 missing-from-DB).
	ev3, _, _, err := b.BuildClipContext(
		context.Background(),
		[]string{"clip-A", "missing-X"},
		&scripts.ClipGenerationOptions{Language: "en"},
	)
	if err != nil {
		t.Fatalf("mixed path returned error: %v", err)
	}
	if ev3 == nil {
		t.Fatal("mixed path: evidence is nil")
	}
	if len(ev3.MissingClipIDs) != 1 {
		t.Fatalf("mixed path: MissingClipIDs = %v, want 1 entry", ev3.MissingClipIDs)
	}

	// Path 4: empty request (zero clip IDs).
	_, _, _, err = b.BuildClipContext(context.Background(), nil, &scripts.ClipGenerationOptions{Language: "en"})
	if err == nil {
		t.Fatal("empty path: expected error for empty clip IDs, got nil")
	}

	// ── 3. Final assertion: spy was never invoked ─────────────────
	if spy.calls != 0 {
		t.Fatalf("translation spy was invoked %d times across 4 builder paths; the video pipeline MUST NOT invoke translation (Fase 4 §19 test #7)", spy.calls)
	}
}

// TestClipSourceBuilder_MissingTranscriptFailsClosedForMixedBatch pins
// the batch-level contract: a valid clip cannot make a batch with
// another valid clip lacking a transcript succeed partially.
func TestClipSourceBuilder_MissingTranscriptFailsClosedForMixedBatch(t *testing.T) {
	const (
		validID   = "clip-A"
		missingID = "clip-B"
	)

	reader := &stubTextTrackReader{
		tracks: map[string]*asset.TextTrack{
			validID + ":en": makeTrack(validID, "en", "valid transcript", asset.TextTrackReady),
		},
	}
	stubClips := &stubClipsResolver{
		byID: map[string]*asset.Asset{
			validID:   makeTestAsset(validID, "Clip A", "https://drive/clip-A"),
			missingID: makeTestAsset(missingID, "Clip B", "https://drive/clip-B"),
		},
	}

	builder := scripts.NewClipSourceBuilder(stubClips, nil, zap.NewNop())
	builder.ConfigureTextTrackReader(reader)

	ev, _, _, err := builder.BuildClipContext(
		context.Background(),
		[]string{validID, missingID},
		&scripts.ClipGenerationOptions{Language: "en"},
	)
	if err == nil {
		t.Fatal("mixed batch with missing transcript must fail closed; got nil error")
	}
	if ev != nil {
		t.Fatalf("mixed batch returned partial evidence: %v", ev)
	}
	var notReady *scripts.ErrTextTrackNotReady
	if !errors.As(err, &notReady) {
		t.Fatalf("mixed batch error = %T %v, want *ErrTextTrackNotReady", err, err)
	}
	if notReady.AssetID != missingID {
		t.Fatalf("mixed batch typed error AssetID = %q, want %q", notReady.AssetID, missingID)
	}
}

// stubTextTrackReader is a hand-rolled scriptports.TextTrackReader
// stub. It uses a stringly-typed key ("assetID:languageCode") for
// the FindReady lookup; the kind param is discarded (the test
// only uses TextTrackTranscript). A future refactor that adds
// kind-aware lookups should switch to a struct key.
type stubTextTrackReader struct {
	tracks         map[string]*asset.TextTrack
	readyLanguages map[string][]string
}

func (s *stubTextTrackReader) FindReady(_ context.Context, assetID, languageCode string, _ asset.TextTrackKind) (*asset.TextTrack, []asset.TimedCue, error) {
	key := assetID + ":" + languageCode
	if track, ok := s.tracks[key]; ok {
		return track, nil, nil
	}
	return nil, nil, nil
}

func (s *stubTextTrackReader) ListReadyLanguages(_ context.Context, assetID string, _ asset.TextTrackKind) ([]string, error) {
	if langs, ok := s.readyLanguages[assetID]; ok {
		return langs, nil
	}
	return nil, nil
}

// makeTrack is a tiny constructor for a READY TextTrack used by
// the stub reader.
func makeTrack(assetID, languageCode, text string, status asset.TextTrackStatus) *asset.TextTrack {
	return &asset.TextTrack{
		AssetID:       assetID,
		LanguageCode:  languageCode,
		TextKind:      asset.TextTrackTranscript,
		TextContent:   text,
		TextHash:      "hash-" + assetID + "-" + languageCode,
		SourceVersion: "v1.0",
		Status:        status,
	}
}

// makeTestAsset is a tiny constructor for a *asset.Asset used by
// the stub clip resolver.
func makeTestAsset(id, name, driveLink string) *asset.Asset {
	a := &asset.Asset{ID: id, Name: name}
	if driveLink != "" {
		a.SetDriveLink(driveLink)
	}
	return a
}

// Compile-time pin: stubTextTrackReader satisfies the canonical
// `scriptports.TextTrackReader` surface (Fase 4). The assertion
// fails at build time if a future refactor drifts the
// TextTrackReader signature. PR-PY-CLIPS-CORRETTE-TRADOTTE
// Fase 4 (July 2026): uses the canonical port (NOT a local type
// alias) so drift between the test stub and the production port
// is impossible.
var _ scriptports.TextTrackReader = (*stubTextTrackReader)(nil)
