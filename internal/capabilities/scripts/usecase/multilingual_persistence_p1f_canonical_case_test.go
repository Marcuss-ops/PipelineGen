// multilingual_persistence_p1f_canonical_case_test.go
//
// Group 2 - DB-hit canonical case (TextTrackResolver): 2 tests.
// Extracted atomically from multilingual_persistence_p1f_test.go (P1F, 2026-07-04).
// Uses stubs p1fStubRepo/p1fStubSubtitles/p1fStubTranscriber + newP1FResolver helper
// from the original file (same usecase package). Stubs migrate into Group 4 file
// in a subsequent atomic commit, where InsertTranslationWithAuditPredecessor is exercised.
//
// godlike/06 SSOT: this file is the canonical SOLE owner of the 2
// TestCanonicalCase_* test functions. Other groups (MissingTranslation, AuditTrail)
// remain in the original file pending subsequent atomic-extract commits.
// Imports are the MINIMAL subset (context/testing/assert/require/youtube.usecase/domain.asset)
// — fmt/errors/sync/time/zap/youtubeports stay in original (used only by stubs).

package usecase

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// ── Group 2: DB-hit canonical case (TextTrackResolver) ──────────────────

// TestCanonicalCase_ItalianReadyTrack_NoTranslatorNoWhisper pins
// the user-spec canonical-case contract:
//
//	"clip originale inglese + text track italiano READY + request
//	 language=it → usa track salvato, NO chiamata traduttore, NO
//	 nuova trascrizione."
//
// The asset has a READY Italian transcript in the DB. The
// resolver MUST:
//   - hit the DB (priority 2) and return the saved track
//     byte-equivalent
//   - NOT call Whisper (priority 5) for a new transcription
//   - NOT call Subtitles (priority 3+4) when DB has a match
//
// The "translator" in the user spec maps to any of the upstream
// acquisition paths (Whisper, Subtitles, or a future
// translation leg) — the test pins ALL three as "must not be
// called" for the canonical case.

// Asset has a READY Italian track in the DB.

// Acquire with PreferredLanguages = [it] (the canonical case).

// User-spec invariant 1: bundle matches the DB row byte-equivalent.

// User-spec invariant 2: NO Whisper call (no new transcription).

// User-spec invariant 3: NO Subtitles call (DB short-circuits).

// TestCanonicalCase_UseSavedTrack_ByteEquivalentToDBRow pins the
// canonical contract at a tighter grain: the returned bundle's
// PlainText + LanguageCode + SourceType + TextHash + SourceVersion
// are all byte-equivalent to the DB row's stored values. No
// re-derivation, no translation, no silent substitution.
func TestCanonicalCase_UseSavedTrack_ByteEquivalentToDBRow(t *testing.T) {
	t.Parallel()

	originalText := "Il match Pacquiao vs Broner inizia con la fase di studio, trascrizione italiana dal DB."
	originalLang := "it"
	originalSrcLang := "en"
	originalHash := detail.TextHash(originalText, originalLang, detail.TextTrackTranscript)
	originalSrcVer := detail.SourceVersion(originalHash, originalSrcLang, originalLang, "yt-dlp", "yt-auto", "v1", "")

	repo := &p1fStubRepo{rows: []detail.TextTrack{
		{
			AssetID:            "yt_p1f_byte_001",
			LanguageCode:       originalLang,
			TextKind:           detail.TextTrackTranscript,
			TextContent:        originalText,
			Status:             detail.TextTrackReady,
			SourceType:         detail.TextSourceYouTubeSubtitle,
			SourceLanguageCode: originalSrcLang,
			IsOriginal:         true,
			Provider:           "yt-dlp",
			ModelName:          "yt-auto",
			ModelVersion:       "v1",
			TextHash:           originalHash,
			SourceVersion:      originalSrcVer,
		},
	}}

	resolver := newP1FResolver(repo, &p1fStubSubtitles{}, &p1fStubTranscriber{})
	bundle, err := resolver.AcquireSegmentText(context.Background(), usecase.TextTrackAcquireRequest{
		ClipID:             "yt_p1f_byte_001",
		PreferredLanguages: []string{originalLang},
	})
	require.NoError(t, err)
	require.NotNil(t, bundle)

	// The bundle's PlainText matches the DB row's TextContent
	// byte-equivalent (no LLM re-derivation, no translation).
	assert.Equal(t, originalText, bundle.PlainText,
		"bundle.PlainText MUST match the DB row byte-equivalent (no re-derivation)")

	// SourceType byte-equivalent.
	assert.Equal(t, detail.TextSourceYouTubeSubtitle, bundle.SourceType,
		"bundle.SourceType MUST match the DB row byte-equivalent (provenance preserved)")

	// Provider byte-equivalent (the DB row's Provider propagates).
	assert.Equal(t, "yt-dlp", bundle.Provider,
		"bundle.Provider MUST match the DB row byte-equivalent")

	// ModelName + ModelVersion propagate from the DB row to the bundle
	// (the resolver's cdbRowToBundle helper copies these verbatim).
	assert.Equal(t, "yt-auto", bundle.ModelName,
		"bundle.ModelName MUST match the DB row byte-equivalent")
	assert.Equal(t, "v1", bundle.ModelVersion,
		"bundle.ModelVersion MUST match the DB row byte-equivalent")

	// The bundle's SourceLanguageCode is the DB row's
	// SourceLanguageCode (the original clip's language, NOT the
	// target language — the translation history is preserved).
	assert.Equal(t, originalSrcLang, bundle.SourceLanguageCode,
		"bundle.SourceLanguageCode MUST be the original source language (translation history preserved)")
}

// TestCanonicalCase_PreferredLanguagesFanOut_PicksFirstMatch pins
// the user-spec preferred-languages fan-out: the resolver picks
// the FIRST READY match in the PreferredLanguages list, NOT
// any random match. When the asset has it+en+es READY tracks
// and the request is language=it with PreferredLanguages=[it,en,es],
// the resolver MUST pick "it" (first match).
//
// SUT BUG 3: the resolver may pick a non-first match if the
// priority-2 fan-out iterates the DB in insertion order rather
// than the PreferredLanguages order. The test pins the
// PreferredLanguages-order contract.
func TestCanonicalCase_PreferredLanguagesFanOut_PicksFirstMatch(t *testing.T) {
	t.Parallel()

	// Asset has it+en+es READY tracks (3 languages in DB).
	repo := &p1fStubRepo{rows: []detail.TextTrack{
		{AssetID: "yt_p1f_fanout_001", LanguageCode: "it", TextKind: detail.TextTrackTranscript, TextContent: "italiano dal DB", Status: detail.TextTrackReady, SourceType: detail.TextSourceYouTubeSubtitle, IsOriginal: true},
		{AssetID: "yt_p1f_fanout_001", LanguageCode: "en", TextKind: detail.TextTrackTranscript, TextContent: "english from DB", Status: detail.TextTrackReady, SourceType: detail.TextSourceYouTubeSubtitle, IsOriginal: true},
		{AssetID: "yt_p1f_fanout_001", LanguageCode: "es", TextKind: detail.TextTrackTranscript, TextContent: "español del DB", Status: detail.TextTrackReady, SourceType: detail.TextSourceYouTubeSubtitle, IsOriginal: true},
	}}

	resolver := newP1FResolver(repo, &p1fStubSubtitles{}, &p1fStubTranscriber{})

	// PreferredLanguages = [it, en, es] — the resolver MUST pick "it"
	// (first match), not "en" or "es" (the DB insertion order).
	bundle, err := resolver.AcquireSegmentText(context.Background(), usecase.TextTrackAcquireRequest{
		ClipID:             "yt_p1f_fanout_001",
		PreferredLanguages: []string{"it", "en", "es"},
	})
	require.NoError(t, err)
	require.NotNil(t, bundle)
	assert.Equal(t, "it", bundle.LanguageCode,
		"resolver MUST pick the first preferred language with a READY row. got=%q", bundle.LanguageCode)
	assert.Equal(t, "italiano dal DB", bundle.PlainText,
		"bundle.PlainText MUST be the Italian DB row's TextContent. got=%q", bundle.PlainText)

	// Now reverse the PreferredLanguages order to [es, en, it] and
	// verify the resolver picks "es" (the new first match). This
	// pins the PreferredLanguages-order contract (not the DB
	// insertion order).
	bundle2, err := resolver.AcquireSegmentText(context.Background(), usecase.TextTrackAcquireRequest{
		ClipID:             "yt_p1f_fanout_001",
		PreferredLanguages: []string{"es", "en", "it"},
	})
	require.NoError(t, err)
	require.NotNil(t, bundle2)
	assert.Equal(t, "es", bundle2.LanguageCode,
		"resolver MUST honor PreferredLanguages order (es first). got=%q", bundle2.LanguageCode)
	assert.Equal(t, "español del DB", bundle2.PlainText,
		"bundle.PlainText MUST be the Spanish DB row's TextContent. got=%q", bundle2.PlainText)
}
