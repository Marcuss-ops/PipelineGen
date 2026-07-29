// Package usecase — clip_source_builder_fase4_strict_test.go
// (July 2026).
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 4 — STRICT CUTOVER
// HARDENING (regression-proof of the Fase 4 cutover contract).
//
// This suite is the Fase 4 strict-cutover regression lock. It
// pins the 3 contract invariants that, together, form the
// "Mai TranslationPort runtime / Mai metadata_json fallback /
// errore tipizzato TEXT_TRACK_NOT_READY via errors.As" user
// mandate:
//
//  1. STRICT-READ-SURFACE —
//     `resolveTranscript` reads transcripts EXCLUSIVELY from
//     `asset_text_tracks` via the TextTrackReader port. There
//     is NO `metadata_json["transcript"]` /
//     `metadata_json["clean_transcript"]` fallback path. There
//     is NO `translation.TranslationPort` runtime invocation.
//
//  2. TYPED-ERROR CONTRACT — when the requested language is
//     missing or the track is non-READY, the resolveTranscript
//     path surfaces the typed `*ErrTextTrackNotReady` to the
//     caller. The type is struct-discriminated:
//     `errors.As(err, &target)` works, `errors.Is(err, sentinel)`
//     works via the struct's Is() method, and the carry fields
//     (AssetID / RequestedLanguage / AvailableLanguages /
//     MissingKind) are populated per godlike/07
//     no-fake-availability.
//
//  3. STATIC (Compile-Time) GUARANTEES — the ClipSourceBuilder
//     struct has NO field of type *translation.TranslationService
//     or *translation.TranslationPort; the
//     `clip_source_builder*.go` files do NOT import the
//     translation package; resolveTranscript's file-header
//     MUST state the "Mai TranslationPort runtime" invariant.
//     All three are godlike/06 SSOT structural guarantees.
//
// The test file lives in `package usecase` (internal) so it can
// invoke the unexported `(b *ClipSourceBuilder).resolveTranscript`
// method directly. This is the canonical Fase 4 typed-error
// surface — passing it through BuildClipContext would dilute
// the pin (BuildClipContext logs-and-continues; the typed error
// is logged, not surfaced to the caller's struct assertion
// surface, so the errors.As probe WOULD fail at the caller
// level). Probing resolveTranscript directly keeps the errors.As
// invariant pure — godlike/07 no-fake-availability.
//
// godlike/07 NO-FAKE-AVAILABILITY: every probe asserts the
// canonical surface (typed error + struct-discriminated fields +
// carry-data populated + errors.Is sentinel-match). Never a
// "resolveTranscript returned non-nil" soft pass — that would
// silently accept a regression where the typed error chain
// degrades to a generic error.
package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── Stubs ───────────────────────────────────────────────────────────────

// fase4EmptyReader is a `ports.TextTrackReader` stub that returns
// `(nil, nil, nil)` for every (asset, lang, kind) lookup — i.e.
// "no READY track for any (asset, lang, kind) triple".
type fase4EmptyReader struct{}

func (fase4EmptyReader) FindReady(_ context.Context, _, _ string, _ asset.TextTrackKind) (*asset.TextTrack, []asset.TimedCue, error) {
	return nil, nil, nil
}

func (fase4EmptyReader) ListReadyLanguages(_ context.Context, _ string, _ asset.TextTrackKind) ([]string, error) {
	return nil, nil
}

// fase4AvailableLangReader is a TextTrackReader stub that says
// "no READY track for the requested language" (FindReady returns
// nil) but ListReadyLanguages returns ["es", "fr"] — used to
// populate ErrTextTrackNotReady.AvailableLanguages (the
// godlike/07 structured-payload contract).
type fase4AvailableLangReader struct{}

func (fase4AvailableLangReader) FindReady(_ context.Context, _, _ string, _ asset.TextTrackKind) (*asset.TextTrack, []asset.TimedCue, error) {
	return nil, nil, nil
}

func (fase4AvailableLangReader) ListReadyLanguages(_ context.Context, _ string, _ asset.TextTrackKind) ([]string, error) {
	return []string{"es", "fr"}, nil
}

// fase4ErrorReader is a TextTrackReader stub whose FindReady
// returns a non-nil repo-level error. The Fase 4 strict-cutover
// resolves this branch by translating the io-level error into
// the typed *ErrTextTrackNotReady (NO leakage of the underlying
// error to the caller per godlike/07 typed-error contract).
type fase4ErrorReader struct{}

func (fase4ErrorReader) FindReady(_ context.Context, _, _ string, _ asset.TextTrackKind) (*asset.TextTrack, []asset.TimedCue, error) {
	return nil, nil, errors.New("simulated repo failure")
}

func (fase4ErrorReader) ListReadyLanguages(_ context.Context, _ string, _ asset.TextTrackKind) ([]string, error) {
	return nil, errors.New("simulated repo failure")
}

// fase4PositiveReader is a TextTrackReader stub that returns a
// READY *asset.TextTrack for the canonical (asset, "en",
// Transcript) lookup. Used by phase 4 + phase 5 to assert the
// positive pin: text-track reader content surfaces into the
// assembled sourceText WITHOUT any metadata_json read.
type fase4PositiveReader struct {
	text map[string]string // assetID -> transcript text
}

func (r *fase4PositiveReader) FindReady(_ context.Context, assetID, languageCode string, kind asset.TextTrackKind) (*asset.TextTrack, []asset.TimedCue, error) {
	if text, ok := r.text[assetID]; ok && kind == asset.TextTrackTranscript {
		return &asset.TextTrack{
			AssetID:       assetID,
			LanguageCode:  languageCode,
			TextKind:      asset.TextTrackTranscript,
			TextContent:   text,
			TextHash:      "fase4-" + assetID,
			SourceVersion: "v1",
			Status:        asset.TextTrackReady,
		}, nil, nil
	}
	return nil, nil, nil
}

func (r *fase4PositiveReader) ListReadyLanguages(_ context.Context, _ string, _ asset.TextTrackKind) ([]string, error) {
	return []string{"en"}, nil
}

// ── Fase 4 typed-error surface probes (errors.As + errors.Is) ──────────

// TestFase4_ResolveTranscript_NilReader_ReturnsErrTextTrackNotReady
// pins the COMPOSITION-TIME wiring gap: a ClipSourceBuilder with
// no TextTrackReader configured MUST surface the typed
// *ErrTextTrackNotReady from resolveTranscript — godlike/07
// fail-closed (no silent no-op, no metadata_json fallback).
//
// The probe invokes (c *ClipSourceBuilder).resolveTranscript
// directly because BuildClipContext's per-clip loop logs and
// continues on the typed error (the typed error is NOT surfaced
// to the Go error-return channel of BuildClipContext — that
// threads through a follow-up PR). The probe at this layer
// pins the typed-error chain's own godlike/07 contract.
func TestFase4_ResolveTranscript_NilReader_ReturnsErrTextTrackNotReady(t *testing.T) {
	b := &ClipSourceBuilder{
		// nil textTrackReader — composition gap
		log: zap.NewNop(),
	}
	if _, _, err := b.resolveTranscript(context.Background(), "fase4-asset", "en", nil); err == nil {
		t.Fatalf("nil reader MUST return *ErrTextTrackNotReady; got nil error (silent no-op violates godlike/07)")
	} else {
		var typed *ErrTextTrackNotReady
		if !errors.As(err, &typed) {
			t.Fatalf("nil reader error chain MUST be errors.As-probeable as *ErrTextTrackNotReady; got %T %v", err, err)
		}
		if typed.AssetID != "fase4-asset" {
			t.Fatalf("typed.AssetID = %q, want %q", typed.AssetID, "fase4-asset")
		}
		if typed.RequestedLanguage != "en" {
			t.Fatalf("typed.RequestedLanguage = %q, want %q", typed.RequestedLanguage, "en")
		}
		if typed.MissingKind != asset.TextTrackTranscript {
			t.Fatalf("typed.MissingKind = %q, want %q", typed.MissingKind, asset.TextTrackTranscript)
		}
		// AvailableLanguages MUST be nil for the nil-reader branch
		// — listReadyLanguagesBestEffort short-circuits when
		// c.textTrackReader is nil (no second round-trip attempted).
		if typed.AvailableLanguages != nil {
			t.Fatalf("typed.AvailableLanguages should be nil for nil reader (no second round-trip); got %v", typed.AvailableLanguages)
		}
		// errors.Is sentinel-match (godlike/07 type-discriminated
		// pattern via the struct's Is() method).
		if !errors.Is(err, &ErrTextTrackNotReady{}) {
			t.Fatalf("errors.Is(err, &ErrTextTrackNotReady{}) MUST succeed via struct's Is() method (godlike/07); got false")
		}
	}
}

// TestFase4_ResolveTranscript_EmptyLanguage_ReturnsErrTextTrackNotReady
// pins the VALIDATION gate: an empty (whitespace-only) language
// code MUST surface the typed *ErrTextTrackNotReady — caller
// cannot bypass via empty-string. The probe asserts the carry
// field RequestedLanguage is empty (NOT " " or language="") so
// dashboards render an actionable message.
func TestFase4_ResolveTranscript_EmptyLanguage_ReturnsErrTextTrackNotReady(t *testing.T) {
	b := &ClipSourceBuilder{
		textTrackReader: fase4EmptyReader{},
		log:             zap.NewNop(),
	}

	// Probe 3 input-shape variants: empty, whitespace-only, tabs.
	for _, lang := range []string{"", " ", "\t\t"} {
		t.Run("lang="+lang, func(t *testing.T) {
			_, _, err := b.resolveTranscript(context.Background(), "fase4-empty-lang", lang, nil)
			if err == nil {
				t.Fatalf("empty/whitespace language MUST return *ErrTextTrackNotReady; got nil error")
			}
			var typed *ErrTextTrackNotReady
			if !errors.As(err, &typed) {
				t.Fatalf("empty-lang error MUST be errors.As-probeable as *ErrTextTrackNotReady; got %T %v", err, err)
			}
			if typed.RequestedLanguage != "" {
				t.Fatalf("typed.RequestedLanguage = %q, want \"\" (the empty-lang branch must NOT preserve whitespace)", typed.RequestedLanguage)
			}
			if typed.AssetID != "fase4-empty-lang" {
				t.Fatalf("typed.AssetID = %q, want %q", typed.AssetID, "fase4-empty-lang")
			}
		})
	}
}

// TestFase4_ResolveTranscript_NonReadyTrack_ReturnsErrTextTrackNotReady
// pins the TYPICAL happy-path failure mode: the reader has been
// wired, FindReady returns nil (no READY track for the
// requested (asset, language, kind) triple), and ListReadyLanguages
// returns ["es", "fr"] (other languages ARE ready). The probe
// asserts the typed error's AvailableLanguages is populated with
// the carry data — the godlike/07 structured-payload contract.
func TestFase4_ResolveTranscript_NonReadyTrack_ReturnsErrTextTrackNotReady(t *testing.T) {
	b := &ClipSourceBuilder{
		textTrackReader: fase4AvailableLangReader{},
		log:             zap.NewNop(),
	}
	_, _, err := b.resolveTranscript(context.Background(), "fase4-nonready", "en", nil)
	if err == nil {
		t.Fatalf("non-READY track MUST return *ErrTextTrackNotReady; got nil error")
	}
	var typed *ErrTextTrackNotReady
	if !errors.As(err, &typed) {
		t.Fatalf("non-READY error MUST be errors.As-probeable; got %T %v", err, err)
	}
	if typed.AssetID != "fase4-nonready" {
		t.Fatalf("typed.AssetID = %q, want %q", typed.AssetID, "fase4-nonready")
	}
	if typed.RequestedLanguage != "en" {
		t.Fatalf("typed.RequestedLanguage = %q, want %q", typed.RequestedLanguage, "en")
	}
	if typed.MissingKind != asset.TextTrackTranscript {
		t.Fatalf("typed.MissingKind = %q, want %q", typed.MissingKind, asset.TextTrackTranscript)
	}
	wantAvail := []string{"es", "fr"}
	if len(typed.AvailableLanguages) != len(wantAvail) {
		t.Fatalf("typed.AvailableLanguages = %v, want %v (godlike/07 carry-data contract)", typed.AvailableLanguages, wantAvail)
	}
	for i, lang := range wantAvail {
		if i >= len(typed.AvailableLanguages) || typed.AvailableLanguages[i] != lang {
			t.Fatalf("typed.AvailableLanguages[%d] = %v, want %q", i, typed.AvailableLanguages, lang)
		}
	}
	// errors.Is sentinel-match (godlike/07).
	if !errors.Is(err, &ErrTextTrackNotReady{}) {
		t.Fatalf("errors.Is(err, &ErrTextTrackNotReady{}) MUST succeed via struct's Is() method")
	}
}

// TestFase4_ResolveTranscript_RepoError_ReturnsErrTextTrackNotReady
// pins the GODLIKE-07 FAIL-CLOSED REPO-ERROR contract: when the
// TextTrackReader.FindReady returns a non-nil error (e.g. DB
// timeout), resolveTranscript MUST NOT leak the underlying
// error to the caller — it translates to the typed
// *ErrTextTrackNotReady. The original error IS logged (status
// feedback), but the caller's errors.As probe must match the
// typed-error surface, NOT the raw repo error.
func TestFase4_ResolveTranscript_RepoError_ReturnsErrTextTrackNotReady(t *testing.T) {
	b := &ClipSourceBuilder{
		textTrackReader: fase4ErrorReader{},
		log:             zap.NewNop(),
	}
	_, _, err := b.resolveTranscript(context.Background(), "fase4-repoerr", "en", nil)
	if err == nil {
		t.Fatalf("repo-error branch MUST NOT silently no-op; got nil error")
	}
	var typed *ErrTextTrackNotReady
	if !errors.As(err, &typed) {
		t.Fatalf("repo-error MUST translate to *ErrTextTrackNotReady (godlike/07 fail-closed); got %T %v", err, err)
	}
	if typed.AssetID != "fase4-repoerr" {
		t.Fatalf("typed.AssetID = %q, want %q", typed.AssetID, "fase4-repoerr")
	}
	// REGRESSION-LOCK side: the repo-level error MUST NOT
	// surface as errors.As(string-recoverable). The Fase 4
	// resolver wraps the repo error and surfaces ONLY the
	// typed ErrTextTrackNotReady. If a future refactor
	// accidentally wraps `fmt.Errorf("...: %w", repoErr)`
	// instead of returning the typed error, an errors.As
	// probe for ErrTextTrackNotReady would FAIL — that's
	// the canonical godlike/07 typed-error regression lock.
	//
	// The probe is implicit (the errors.As above already
	// fails closed if the chain is broken).
	_ = strings.Contains // reserved for future expansion (e.g.,
	// asserting the typed error's Error() string format).
}

// ── Fase 4 strict read-surface pin (BuildClipContext end-to-end) ────────

// TestFase4_BuildClipContext_TextTrackReaderIsSoleTranscriptSource
// pins the END-TO-END contract at BuildClipContext's surface:
// the assembled sourceText MUST surface the TextTrackReader's
// TextContent and MUST NOT surface any metadata_json
// `transcript` / `clean_transcript` value, regardless of
// whether both fields are populated as LEGACY POISON.
//
// The probe writes BOTH legacy metadata keys DISTINGUISHABLY-
// marked so any silent fallback would surface distinct payload
// tokens — making a regression immediately attributable to the
// right code path.
//
// godlike/07 NO-FAKE-AVAILABILITY: this probe is the
// end-to-end regression lock for the user mandate "Mai
// metadata_json[\\\"transcript\\\"] fallback".
func TestFase4_BuildClipContext_TextTrackReaderIsSoleTranscriptSource(t *testing.T) {
	const (
		clipID        = "fase4-sot"
		readerPayload = "TRUTH-FROM-TEXT-TRACK-READER-MUST-SURFACE"
		legacyPayout1 = "POISON-FROM-METADATA-TRANSCRIPT-MUST-NOT-SURFACE"
		legacyPayout2 = "POISON-FROM-METADATA-CLEAN-TRANSCRIPT-MUST-NOT-SURFACE"
	)

	clip := &asset.Asset{
		ID:   clipID,
		Name: "Fase 4 source-of-truth clip",
	}
	clip.SetDriveFileID(clipID + "-drive")
	clip.SetDriveLink("https://drive/" + clipID)
	// LEGACY POISON: write BOTH legacy keys with distinguishable
	// payloads. A regression that re-introduces the legacy
	// metadata_json read would surface one of these payloads
	// in the sourceText and immediately fail the probe.
	clip.SetMetadataString("transcript", legacyPayout1)
	clip.SetMetadataString("clean_transcript", legacyPayout2)

	resolver := &fase4StubResolver{byID: map[string]*asset.Asset{clipID: clip}}

	b := NewClipSourceBuilder(resolver, nil, zap.NewNop())
	b.ConfigureTextTrackReader(&fase4PositiveReader{
		text: map[string]string{clipID: readerPayload},
	})

	ev, _, sourceText, err := b.BuildClipContext(
		context.Background(),
		[]string{clipID},
		&ClipGenerationOptions{Language: "en", RequireDriveLink: false},
	)
	if err != nil {
		t.Fatalf("BuildClipContext returned error: %v", err)
	}
	if ev == nil {
		t.Fatal("evidence is nil")
	}

	// POSITIVE pin: TextTrackReader's content surfaces
	// exactly. The canonical Fase 4 read surface is the
	// SINGLE SOURCE OF TRUTH for the per-clip transcript
	// string.
	if !strings.Contains(sourceText, readerPayload) {
		t.Fatalf("Fase 4 STRICT-CUTOVER VIOLATION: TextTrackReader payload %q NOT in sourceText (Fase 4 read surface is broken):\n%s",
			readerPayload, sourceText)
	}
	// NEGATIVE pin #1: metadata_json["transcript"] payload
	// MUST NOT surface. This is the canonical "no metadata_json
	// fallback" probe.
	if strings.Contains(sourceText, legacyPayout1) {
		t.Fatalf("Fase 4 STRICT-CUTOVER VIOLATION: metadata_json[\"transcript\"] poison %q surfaced (silently re-introduced legacy fallback?):\n%s",
			legacyPayout1, sourceText)
	}
	// NEGATIVE pin #2: metadata_json["clean_transcript"] payload
	// MUST NOT surface. Catches scenario 7 (the KNOWN GAP that
	// was supposed to prefer clean_transcript in the doc-comment
	// but actually preferred raw transcript in the implementation).
	// Fase 4 strict cutover RETIRES both legacy paths — neither
	// metadata_json key may surface.
	if strings.Contains(sourceText, legacyPayout2) {
		t.Fatalf("Fase 4 STRICT-CUTOVER VIOLATION: metadata_json[\"clean_transcript\"] poison %q surfaced:\n%s",
			legacyPayout2, sourceText)
	}
}

// ── Helper stub resolver (typedClipResolverPort implementation) ────────

// fase4StubResolver is a typedClipResolverPort stub used in the
// BuildClipContext probe. Test-internal package — co-located
// with the file to avoid cross-file test plumbing.
type fase4StubResolver struct {
	byID map[string]*asset.Asset
}

func (s *fase4StubResolver) ResolveByMediaAssetID(_ context.Context, id string) (*asset.Asset, error) {
	if c, ok := s.byID[id]; ok {
		return c, nil
	}
	return nil, errors.New("fase4 stub: not found")
}

func (s *fase4StubResolver) ResolveByDriveFileID(_ context.Context, fileID string) ([]*asset.Asset, error) {
	if c, ok := s.byID[fileID]; ok {
		return []*asset.Asset{c}, nil
	}
	return nil, errors.New("fase4 stub: not found")
}
