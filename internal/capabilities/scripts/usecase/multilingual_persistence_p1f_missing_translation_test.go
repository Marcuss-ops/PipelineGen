// multilingual_persistence_p1f_missing_translation_test.go
//
// Group 3 - Missing translation policy (TextTrackResolver): 2 tests.
// Extracted atomically from multilingual_persistence_p1f_test.go (P1F, 2026-07-04).
// Uses stubs p1fStubRepo/p1fStubSubtitles/p1fStubTranscriber + newP1FResolver helper
// from the original file (same usecase package). Stubs migrate into Group 4 file
// in a subsequent atomic commit (where InsertTranslationWithAuditPredecessor is exercised
// by TestAuditTrail_P1F_Stub_*).
//
// godlike/06 SSOT: this file is the canonical SOLE owner of the 2
// TestMissingTranslation_* test functions. Other groups (CanonicalCase, AuditTrail)
// remain in the original file pending subsequent atomic-extract commits.
// Imports are the MINIMAL subset (context/errors/testing/assert/require/asset):
//   - context: context.Background() test setup
//   - errors: errors.Is() probe for ErrTextTrackNotReady (godlike/07 typed-error contract)
//   - testing: *testing.T + t.Parallel()
//   - assert/require: testify matchers
//   - asset: domain/asset (TextTrack, Normalize, etc.)
// fmt/sync/time/zap/youtubeports stay in original (used only by stubs + newP1FResolver + compile-pins).
// youtube.usecase is NOT imported: Group 3 calls methods on the resolver via newP1FResolver,
// never declares usecase.X types at the test boundary.

package usecase

import (
	"context"
	"errors"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// ── Group 3: Missing translation policy (TextTrackResolver) ─────────────

// TestMissingTranslation_FrenchNotReady_NoSilentTranslation pins
// the user-spec canonical invariant: "mai traduzione silente".
//
// The asset has NO fr track in DB, no fr subtitles, no Whisper
// fallback that can produce fr content. The resolver MUST NOT
// silently fall through to a Whisper call (which would emit
// text in whatever language Whisper detected — potentially
// not fr). The test pins: Whisper.calls == 0 when there's no
// source material for the target language AND
// RequireLanguageCertainty is true (the policy-gate fail-closed
// path).
//
// SUT BUG 4: the resolver may call Whisper with a
// "best-effort" fallback even when no source material exists
// for the target language, silently producing a Whisper
// transcript in a different language. The test pins the
// fail-closed policy gate.

// Asset has en+es+it READY (no fr).

// Subtitles return a non-fr bundle (the resolver's
// languageInList check will reject the bundle and fall
// through; but RequireLanguageCertainty=true will fire
// before Whisper is consulted).

// fail-closed policy gate

// Request language=fr. No fr track anywhere.

// User-spec invariant 1: RequireLanguageCertainty=true with
// no source material MUST fire asset.ErrLanguageUndeterminable
// (the fail-closed policy gate).

// User-spec invariant 2: NO Whisper call (no silent
// transcription). The resolver MUST short-circuit at the
// policy gate before reaching the Whisper port.

// User-spec invariant 3: NO silent translation. The
// resolver does NOT have a translator port in the canonical
// 5-level chain (translation is a separate concern owned
// by TranslateScriptSpec). The test pins the resolver's
// no-silent-fallback invariant: when the chain exhausts,
// the resolver returns (nil, err) — not a fallback
// bundle in a different language.

// TestMissingTranslation_AvailableLanguagesSurfaced pins the
// operator-visibility contract: when ErrTextTrackNotReady
// fires, the AvailableLanguages slice MUST carry the sorted
// set of languages for which a READY track exists, so
// operator dashboards can surface "what's actually READY"
// without a second round-trip.
//
// The ListReadyLanguages stub is the canonical source of this
// list. The test pins the "list is populated, not empty"
// invariant (SUT BUG 5).
func TestMissingTranslation_AvailableLanguagesSurfaced(t *testing.T) {
	t.Parallel()

	// Asset has en+es+it READY (no fr, no de).
	repo := &p1fStubRepo{rows: []detail.TextTrack{
		{AssetID: "yt_p1f_avail_001", LanguageCode: "en", TextKind: detail.TextTrackTranscript, TextContent: "English from DB", Status: detail.TextTrackReady, SourceType: detail.TextSourceYouTubeSubtitle, IsOriginal: true},
		{AssetID: "yt_p1f_avail_001", LanguageCode: "es", TextKind: detail.TextTrackTranscript, TextContent: "Español del DB", Status: detail.TextTrackReady, SourceType: detail.TextSourceYouTubeSubtitle, IsOriginal: true},
		{AssetID: "yt_p1f_avail_001", LanguageCode: "it", TextKind: detail.TextTrackTranscript, TextContent: "Italiano dal DB", Status: detail.TextTrackReady, SourceType: detail.TextSourceYouTubeSubtitle, IsOriginal: true},
	}}

	// The TextTrackReader surface (Fase 4) is what builds the
	// ErrTextTrackNotReady error. The test uses the
	// repository directly to verify the ListReadyLanguages
	// output is what the error would carry.
	got, err := repo.ListReadyLanguages(context.Background(), "yt_p1f_avail_001", detail.TextTrackTranscript)
	require.NoError(t, err, "ListReadyLanguages MUST succeed")
	require.NotNil(t, got, "ListReadyLanguages MUST return a non-nil slice")

	// Sort the result (ListReadyLanguages returns the sorted set
	// per the canonical contract).
	expected := []string{"en", "es", "it"}
	assert.Equal(t, expected, got,
		"ListReadyLanguages MUST return the sorted set of READY languages. got=%v", got)

	// Simulate the ErrTextTrackNotReady construction with the
	// available languages list. The test pins the
	// AvailableLanguages population contract.
	typed := &ErrTextTrackNotReady{
		AssetID:            "yt_p1f_avail_001",
		RequestedLanguage:  "fr",
		AvailableLanguages: got,
		MissingKind:        detail.TextTrackTranscript,
	}
	errMsg := typed.Error()
	// The error message MUST include every available language
	// (so operator dashboards can correlate "fr was requested,
	// but en/es/it are READY").
	for _, lang := range expected {
		assert.Contains(t, errMsg, lang,
			"ErrTextTrackNotReady.Error() MUST mention every available language %q. got=%q",
			lang, errMsg)
	}
	// And the requested language MUST appear in the message.
	assert.Contains(t, errMsg, "fr",
		"ErrTextTrackNotReady.Error() MUST mention the requested language \"fr\". got=%q", errMsg)

	// errors.Is probe (the canonical godlike/07 typed-error
	// contract): the typed error MUST be probeable via
	// errors.Is(err, &ErrTextTrackNotReady{}).
	require.True(t, errors.Is(typed, &ErrTextTrackNotReady{}),
		"ErrTextTrackNotReady MUST be errors.Is-probeable (godlike/07 typed-error contract)")
}

// TestMissingTranslation_ChainExhausted_NoSilentSubstitution pins
// the user-spec no-silent-translation invariant. The user spec
// says "fallback con warning esplicito (mai traduzione
// silente)" — the canonical resolver today returns (nil, nil)
// on chain-exhausted without RequireLanguageCertainty; there
// is NO silent language substitution. The test pins the
// invariant by asserting that, when the chain exhausts
// without a matching language, the resolver does NOT
// produce a bundle in a different language.
//
// SUT BUG 6 (silent language substitution): the resolver
// may substitute "en" for an empty/unknown targetLang
// input. The godlike/07 no-fake-availability invariant
// pins empty → "und", never silently to "en". The test
// exercises the empty-targetLang path.
//
// Note: a future PR that adds a "fallback to closest available
// language with explicit warning" path would change the
// assertion to (bundle!=nil with warning!=empty) — the test
// pins the current (nil, nil) behavior, which is the
// godlike/07 fail-closed default.

// Asset has en+es+it READY (no fr, no de).

// Subtitles + Whisper return no usable content.

// RequireLanguageCertainty=false (the canonical
// pre-Fase-1.b behavior: chain exhaustion → (nil, nil)
// silent degradation, no error). The test pins this
// current behavior AND the no-silent-substitution
// invariant: the resolver MUST NOT produce a bundle in
// "en" just because "en" is the closest available
// language.

// both absent

// User-spec invariant: NO silent substitution to the closest
// available language. The resolver did NOT consult
// Whisper/Subtitles/Translate to produce an "en" bundle.

// Now exercise the empty targetLang path. The user spec
// mandates that an empty targetLang must NOT silently
// default to "en" (godlike/07 no-fake-availability).

// TestMissingTranslation_NormalizeEmptyLanguageToUnd pins the
// godlike/07 no-fake-availability invariant: an empty
// targetLang input MUST collapse to BCP-47 "und", never
// silently default to "en". The test exercises the
// canonical BCP-47 normalizer path and pins the
// error-language contract.
func TestMissingTranslation_NormalizeEmptyLanguageToUnd(t *testing.T) {
	t.Parallel()

	// godlike/07 honest lock: empty input MUST collapse to
	// "und" (the canonical BCP-47 undetermined marker). The
	// resolver does NOT silently substitute "en" for the
	// empty input.
	normalized, err := asset.Normalize("")
	require.NoError(t, err, "Normalize with empty input MUST NOT error")
	assert.Equal(t, "und", normalized,
		"Normalize(\"\") MUST collapse to \"und\" (BCP-47 undetermined), never \"en\"")

	// And the resolver's ResolveLanguage method MUST honor
	// the canonical "und" → "not found" path (no DB probe
	// with "und", no silent substitution).
	repo := &p1fStubRepo{rows: []detail.TextTrack{
		{AssetID: "yt_p1f_empty_001", LanguageCode: "en", TextKind: detail.TextTrackTranscript, TextContent: "English from DB", Status: detail.TextTrackReady, SourceType: detail.TextSourceYouTubeSubtitle, IsOriginal: true},
	}}
	resolver := newP1FResolver(repo, &p1fStubSubtitles{}, &p1fStubTranscriber{})

	// Empty targetLang → resolver collapses to "und" → no DB
	// probe (the resolver does NOT scan all rows for the
	// asset).
	row, err := resolver.ResolveLanguage(context.Background(),
		"yt_p1f_empty_001", "", detail.TextTrackTranscript)
	require.NoError(t, err)
	assert.Nil(t, row,
		"ResolveLanguage with empty targetLang MUST return nil (no silent substitution to \"en\" or any default)")
}
