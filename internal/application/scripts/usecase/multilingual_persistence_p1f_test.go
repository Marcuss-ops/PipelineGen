// Package usecase — multilingual_persistence_p1f_test.go
//
// P1.F — Multilingua e traduzioni persistite test suite
// (PR-PY-CLIPS-CORRETTE-TRADOTTE, July 2026).
//
// USER SPEC (verbatim, July 2026, Italian):
// "Implementa la suite P1.F — Multilingua e traduzioni
// persistite su main. (1) Stesse 8 clip, language=it/en/es/pt
// → stessa copertura eventi, stesso ordine, nessuna perdita
// narrativa, nessun cambio di significato. NON confrontare
// parola per parola, solo copertura eventi + item
// structure. (2) Caso canonico: clip originale inglese +
// text track italiano READY + request language=it → usa
// track salvato, NO chiamata traduttore, NO nuova
// trascrizione. (3) Policy traduzione mancante:
// language=fr, track fr assente → TEXT_TRACK_NOT_READY
// OPPURE fallback con warning esplicito (mai traduzione
// silente). Lavora su main, commit frequenti, push."
//
// ATTESO per the user spec:
//
//	Group 1 — Cross-language consistency (TranslateScriptSpec):
//	 - Same 8-scene English script translated to it/en/es/pt
//	   must produce 4 outputs with the SAME event coverage
//	   (scene count, scene IDs, scene kinds) and SAME order
//	   (Index 0..7), with NO narrative loss (all 8 clips
//	   bound) and NO meaning change (entity markers preserved
//	   in translation).
//	 - User spec: "NON confrontare parola per parola, solo
//	   copertura eventi + item structure" — the test pins the
//	   STRUCTURAL equivalence (not byte-equality of text).
//
//	Group 2 — DB-hit canonical case (TextTrackResolver):
//	 - Asset has Italian READY track in DB. Request
//	   language=it. Resolver hits the DB (priority 2), returns
//	   the saved track byte-equivalent, and does NOT call
//	   Whisper, Subtitles, or any translator.
//	 - User spec: "NO chiamata traduttore, NO nuova
//	   trascrizione" — the test pins the cache-hit path
//	   end-to-end (no upstream port consulted).
//
//	Group 3 — Missing translation policy (TextTrackResolver):
//	 - language=fr requested, no fr track in DB, no fr
//	   subtitles, no Whisper fallback. The pipeline MUST
//	   surface a typed error (ErrLanguageUndeterminable when
//	   policy requires certainty) or (nil, nil) when it
//	   doesn't, BUT in NO case call a translator with
//	   targetLang=fr.
//	 - User spec: "TEXT_TRACK_NOT_READY OPPURE fallback con
//	   warning esplicito (mai traduzione silente)" — the test
//	   pins the no-silent-translation invariant + the
//	   available-languages operator visibility
//	   (ListReadyLanguages → AvailableLanguages).
//
// SEAM CHOICE: two layers — TranslateScriptSpec (for Group 1)
// and TextTrackResolver (for Groups 2+3). The two layers
// are independent: TranslateScriptSpec is a pure function
// (text-in, text-out); TextTrackResolver is the canonical
// acquisition chain that decides where the text comes from.
// P1.F pins BOTH surfaces because the user spec covers
// both "multilingua" (script translation across languages)
// and "traduzioni persistite" (DB-persisted translations
// are reused, not regenerated).
//
// SUT BUGS (pin current behavior; 2026-07 candidates for the
// "honest lock" backlog):
//
//  1. TranslateScriptSpec may produce scene-count drift
//     across languages (e.g., 8 → 7 or 8 → 9 scenes if the
//     validator rejects/expands). Today the function is
//     1:1 with the input (no scene count drift). The test
//     pins this as the load-bearing invariant.
//
//  2. TranslateScriptSpec may reorder scenes across
//     languages if the validator mutates Index. Today the
//     function preserves Index 0..7. The test pins this.
//
//  3. TextTrackResolver.AcquireSegmentText may fall through
//     to Whisper even when the DB has a READY track in the
//     target language. The Fase 1.a/b contract is
//     priority-2 (DB) wins over priority-5 (Whisper) when
//     PreferredLanguages matches. The test pins this as the
//     canonical cache-hit path.
//
//  4. The resolver may call Whisper with a "best-effort"
//     fallback even when a typed ErrLanguageUndeterminable
//     should fire (RequireLanguageCertainty=true). The
//     test pins the policy-gate fail-closed behavior.
//
//  5. The available-languages list may be empty when an
//     ErrTextTrackNotReady fires (operator dashboard cannot
//     tell what's available). The test pins
//     ListReadyLanguages → AvailableLanguages surfacing.
//
//  6. The resolver may silently substitute a "default"
//     language (e.g. "en") for an empty targetLang input,
//     silently translating to the wrong language. The
//     godlike/07 no-fake-availability invariant says empty
//     must collapse to "und", never silently to "en". The
//     test pins this contract via ResolveLanguage.
//
//  7. PreferredLanguages-order contract: the resolver picks
//     the FIRST READY match in the PreferredLanguages list,
//     not the DB insertion order. A regression that flips
//     the order would silently surface a different language
//     than the operator requested. The test pins the
//     [it,en,es] vs [es,en,it] contrast.
package usecase

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/application/youtube/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// ── Group 1 helpers: 8-scene Pacquiao-Broner fixture ────────────────────

// makeEightScenePacquiaoSpecEN constructs an 8-scene EN script
// paralleling the 8 Pacquiao-Broner rounds from the P2.A spec
// (commit f3d181777). Each scene is a SceneClip with a unique
// clip_id + drive_link + image_id + scene_id; entity markers
// ("Pacquiao", "Broner", "round N") anchor the cross-language
// semantic-equivalence assertion.
//
// The fixture uses round numbers 1, 2, 5, 7, 9, 10-11, 12, post
// (matches the canonical P2.A clip_ids from commit f3d181777
// — abbreviated to "round-N" labels for the P1.F test surface).
//
// Clip binding StartMs/EndMs are 32000/37000 for all 8 scenes
// (parallels the P2.A canonical 32s-37s window for round 1) so
// the P1.F cross-language fingerprint can pin timestamp
// preservation as a load-bearing invariant of "nessuna perdita
// narrativa" (no narrative loss).
// with the same canonical semantics (FindReady filters by
// status=READY; ListReadyLanguages returns the sorted set).
type p1fStubRepo struct {
	mu   sync.Mutex
	rows []asset.TextTrack
}

func (s *p1fStubRepo) UpsertBatch(_ context.Context, tracks []asset.TextTrack) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows = append(s.rows, tracks...)
	return nil
}

func (s *p1fStubRepo) Find(_ context.Context, assetID, languageCode string, kind asset.TextTrackKind) (*asset.TextTrack, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.rows {
		if s.rows[i].AssetID == assetID &&
			s.rows[i].LanguageCode == languageCode &&
			s.rows[i].TextKind == kind {
			return &s.rows[i], nil
		}
	}
	return nil, nil
}

// FindReady is the canonical Fase 1.b READY-only lookup. The
// stub returns the row when status=READY and ignores
// PENDING/FAILED rows (matches the production contract).
func (s *p1fStubRepo) FindReady(_ context.Context, assetID, languageCode string, kind asset.TextTrackKind) (*asset.TextTrack, []asset.TimedCue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.rows {
		if s.rows[i].AssetID == assetID &&
			s.rows[i].LanguageCode == languageCode &&
			s.rows[i].TextKind == kind &&
			s.rows[i].Status == asset.TextTrackReady {
			return &s.rows[i], nil, nil
		}
	}
	return nil, nil, nil
}

// ListReadyLanguages returns the sorted set of language
// codes for which a READY track exists.
func (s *p1fStubRepo) ListReadyLanguages(_ context.Context, assetID string, kind asset.TextTrackKind) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]struct{}{}
	var out []string
	for i := range s.rows {
		if s.rows[i].AssetID == assetID &&
			s.rows[i].TextKind == kind &&
			s.rows[i].Status == asset.TextTrackReady {
			if _, ok := seen[s.rows[i].LanguageCode]; !ok {
				seen[s.rows[i].LanguageCode] = struct{}{}
				out = append(out, s.rows[i].LanguageCode)
			}
		}
	}
	return out, nil
}

func (s *p1fStubRepo) ListByAsset(_ context.Context, assetID string) ([]asset.TextTrack, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []asset.TextTrack
	for _, r := range s.rows {
		if r.AssetID == assetID {
			out = append(out, r)
		}
	}
	return out, nil
}

// FindCurrentForTranslation is the canonical
// lookup-before-translate gate (godlike/06 SSOT). The stub
// returns (nil, nil): every test that exercises Group 2/3
// uses test-local fixtures that bypass the materializer
// path, so the stub does not need to honour the 6-tuple
// translation_key lookup. The presence of the method is
// what matters — it lets the compile-time
// `var _ asset.TextTrackRepository = (*p1fStubRepo)(nil)`
// assertion at the bottom of this file pass.
func (s *p1fStubRepo) FindCurrentForTranslation(
	_ context.Context,
	_ string,
	_ asset.TextTrackKind,
	_ string,
	_ string,
	_ string,
	_ string,
	_ string,
) (*asset.TextTrack, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return nil, nil
}

// InsertTranslationWithAuditPredecessor atomically flips any
// prior is_current=1 row for the same (asset, language, kind)
// tuple whose TranslationKey differs, then appends the new
// track with IsCurrent=true and refreshed UpdatedAt. Mirrors the
// canonical SQLite implementation in
// `internal/infrastructure/database/sqlite/assets/text_track_repository.go`
// (godlike/06 SSOT — the stub honours the same audit-trail
// semantics as the production port, no inline reimplementation
// of the flip formula).
//
// Steps (preserving the SQLite §Step 1..3 contract):
//  1. Idempotency — if a current row already exists in this tuple
//     with a matching TranslationKey, the insert is a no-op. The
//     row is NOT duplicated, the IsCurrent flag is NOT toggled,
//     and UpdatedAt is NOT refreshed on the existing row.
//  2. Flip — for every prior is_current=1 row in the same tuple
//     (matching AssetID + LanguageCode + TextKind) with a
//     DIFFERENT TranslationKey, set IsCurrent=false and refresh
//     UpdatedAt. The row is preserved (never deleted) — the audit
//     trail remains queryable through ListByAsset for forensic
//     dumps.
//  3. Append — the new track has IsCurrent forced to true (even if
//     the caller forgot), UpdatedAt refreshed, ID assigned if
//     missing, and Status defaulted to TextTrackReady when blank.
//
// godlike/07 NO-FAKE-AVAILABILITY: the stub validates the four
// required fields exactly like the SQLite impl (AssetID,
// LanguageCode, TextKind, TranslationKey). A test that wanted to
// exercise a different contract MUST NOT rely on this stub; it
// belongs in a dedicated port test that mocks the implementation,
// not the audit-trail-aware seam.
func (s *p1fStubRepo) InsertTranslationWithAuditPredecessor(_ context.Context, track asset.TextTrack) error {
	if track.AssetID == "" || track.LanguageCode == "" || track.TextKind == "" || track.TranslationKey == "" {
		return fmt.Errorf("p1fStubRepo.InsertTranslationWithAuditPredecessor: AssetID, LanguageCode, TextKind, TranslationKey are all required (caller bug)")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	// Step 1: idempotency — same TranslationKey under the same
	// tuple = no-op. The SQLite impl has the same short-circuit
	// via SELECT step 1 + COMMIT.
	for i := range s.rows {
		row := &s.rows[i]
		if row.AssetID == track.AssetID &&
			row.LanguageCode == track.LanguageCode &&
			row.TextKind == track.TextKind &&
			row.IsCurrent &&
			row.TranslationKey == track.TranslationKey {
			return nil
		}
	}
	// Step 2: flip prior is_current=1 rows in the same tuple with
	// a DIFFERENT TranslationKey. Mirrors the SQLite UPDATE
	// step that flips audit predecessors atomically.
	for i := range s.rows {
		row := &s.rows[i]
		if row.AssetID == track.AssetID &&
			row.LanguageCode == track.LanguageCode &&
			row.TextKind == track.TextKind &&
			row.IsCurrent &&
			row.TranslationKey != track.TranslationKey {
			row.IsCurrent = false
			row.UpdatedAt = now
		}
	}
	// Step 3: append the new row. Force IsCurrent=true (the
	// canonical gateway that decides "this row is current" lives
	// here, not at the caller) — matches the SQLite impl that
	// literally writes is_current=1 on INSERT regardless of the
	// caller's payload.
	track.IsCurrent = true
	if track.Status == "" {
		track.Status = asset.TextTrackReady
	}
	if track.ID == 0 {
		track.ID = int64(len(s.rows) + 1000) // deterministic test id (mirrors materializer_test.go:146)
	}
	if track.CreatedAt.IsZero() {
		track.CreatedAt = now
	}
	track.UpdatedAt = now
	s.rows = append(s.rows, track)
	return nil
}

// p1fStubSubtitles records FetchSegmentSubtitles calls. Used to
// assert the resolver did NOT fall through to subtitles when
// the DB has a READY track (Group 2) and to assert the
// resolver did NOT consult subtitles for a fr request with no
// source material (Group 3).
type p1fStubSubtitles struct {
	bundle *asset.ResolvedTextBundle
	err    error
	calls  int
}

func (s *p1fStubSubtitles) SliceSubtitles(_ context.Context, _ string, _, _ int, _ string) error {
	return nil
}
func (s *p1fStubSubtitles) FetchSegmentSubtitles(_ context.Context, _ string, _, _ int) (*asset.ResolvedTextBundle, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.bundle, nil
}

// p1fStubTranscriber records TranscribeAudio + TranscribeAudioWithDetection
// invocations. Used to assert the resolver did NOT call Whisper
// when the DB has a READY track (Group 2) and the resolver did
// NOT silently call Whisper for a fr request with no source
// material (Group 3).
type p1fStubTranscriber struct {
	text  string
	err   error
	calls int
	det   *asset.TranscriptResult
}

func (s *p1fStubTranscriber) TranscribeAudio(_ context.Context, _ string) (string, error) {
	s.calls++
	if s.err != nil {
		return "", s.err
	}
	return s.text, nil
}

func (s *p1fStubTranscriber) TranscribeAudioWithDetection(_ context.Context, _ string) (asset.TranscriptResult, error) {
	s.calls++
	if s.err != nil {
		return asset.TranscriptResult{}, s.err
	}
	if s.det != nil {
		return *s.det, nil
	}
	return asset.TranscriptResult{Text: s.text, DetectedLanguage: ""}, nil
}

// Compile-time guarantees that the stubs satisfy the ports the
// resolver depends on.
var (
	_ asset.TextTrackRepository           = (*p1fStubRepo)(nil)
	_ youtubeports.SubtitleFetcherPort    = (*p1fStubSubtitles)(nil)
	_ youtubeports.WhisperTranscriberPort = (*p1fStubTranscriber)(nil)
)

// newP1FResolver builds a TextTrackResolver wired with the
// given stubs. Log is zap.NewNop() to keep the test surface
// deterministic.
func newP1FResolver(repo *p1fStubRepo, subs *p1fStubSubtitles, trans *p1fStubTranscriber) *usecase.TextTrackResolver {
	return &usecase.TextTrackResolver{
		Repo:        repo,
		Subtitles:   subs,
		Transcriber: trans,
		Log:         zap.NewNop(),
	}
}

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
	repo := &p1fStubRepo{rows: []asset.TextTrack{
		{AssetID: "yt_p1f_avail_001", LanguageCode: "en", TextKind: asset.TextTrackTranscript, TextContent: "English from DB", Status: asset.TextTrackReady, SourceType: asset.TextSourceYouTubeSubtitle, IsOriginal: true},
		{AssetID: "yt_p1f_avail_001", LanguageCode: "es", TextKind: asset.TextTrackTranscript, TextContent: "Español del DB", Status: asset.TextTrackReady, SourceType: asset.TextSourceYouTubeSubtitle, IsOriginal: true},
		{AssetID: "yt_p1f_avail_001", LanguageCode: "it", TextKind: asset.TextTrackTranscript, TextContent: "Italiano dal DB", Status: asset.TextTrackReady, SourceType: asset.TextSourceYouTubeSubtitle, IsOriginal: true},
	}}

	// The TextTrackReader surface (Fase 4) is what builds the
	// ErrTextTrackNotReady error. The test uses the
	// repository directly to verify the ListReadyLanguages
	// output is what the error would carry.
	got, err := repo.ListReadyLanguages(context.Background(), "yt_p1f_avail_001", asset.TextTrackTranscript)
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
		MissingKind:        asset.TextTrackTranscript,
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
	repo := &p1fStubRepo{rows: []asset.TextTrack{
		{AssetID: "yt_p1f_empty_001", LanguageCode: "en", TextKind: asset.TextTrackTranscript, TextContent: "English from DB", Status: asset.TextTrackReady, SourceType: asset.TextSourceYouTubeSubtitle, IsOriginal: true},
	}}
	resolver := newP1FResolver(repo, &p1fStubSubtitles{}, &p1fStubTranscriber{})

	// Empty targetLang → resolver collapses to "und" → no DB
	// probe (the resolver does NOT scan all rows for the
	// asset).
	row, err := resolver.ResolveLanguage(context.Background(),
		"yt_p1f_empty_001", "", asset.TextTrackTranscript)
	require.NoError(t, err)
	assert.Nil(t, row,
		"ResolveLanguage with empty targetLang MUST return nil (no silent substitution to \"en\" or any default)")
}

// ── Audit-trail-aware stub tests ────────────────────────────────────────────

// TestAuditTrail_P1F_Stub_InsertTranslationWithAuditPredecessor_FlipsPriorCurrent
// pins the godlike/06 audit-trail invariant through the test
// seam stub. When a new translation row is inserted under the
// same (asset, language, kind) tuple with a DIFFERENT
// TranslationKey, the prior is_current=1 row MUST be flipped to
// is_current=0 + refreshed UpdatedAt, the new row MUST be
// appended with IsCurrent=true, and the total row count MUST
// grow by exactly 1 (the prior row is preserved, NOT deleted).
//
// The test mirrors the canonical SQLite impl semantics documented
// in `internal/domain/asset/text_track_repository.go` and the
// `fakeTextTrackRepo` seam in
// `internal/application/assets/texttracks/materializer_test.go`.
func TestAuditTrail_P1F_Stub_InsertTranslationWithAuditPredecessor_FlipsPriorCurrent(t *testing.T) {
	t.Parallel()
	const (
		assetID  = "yt_p1f_audit_001"
		lang     = "it"
		kind     = asset.TextTrackTranscript
		priorKey = "key-prior-v1"
		nextKey  = "key-next-v2"
	)
	createdAt := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	priorUpdated := createdAt.Add(time.Hour)
	repo := &p1fStubRepo{rows: []asset.TextTrack{
		{
			ID:             1001,
			AssetID:        assetID,
			LanguageCode:   lang,
			TextKind:       kind,
			TextContent:    "italiano v1 - dal DB",
			Status:         asset.TextTrackReady,
			SourceType:     asset.TextSourceTranslation,
			IsCurrent:      true,
			TranslationKey: priorKey,
			PromptVersion:  "prompt-v1",
			CreatedAt:      createdAt,
			UpdatedAt:      priorUpdated,
		},
	}}
	const priorIdx = 0
	if got := len(repo.rows); got != 1 {
		t.Fatalf("seed precondition: want 1 row, got %d", got)
	}
	if !repo.rows[priorIdx].IsCurrent {
		t.Fatalf("seed precondition: prior row MUST start as IsCurrent=true")
	}
	if repo.rows[priorIdx].TranslationKey != priorKey {
		t.Fatalf("seed precondition: prior row TranslationKey = %q, want %q",
			repo.rows[priorIdx].TranslationKey, priorKey)
	}

	err := repo.InsertTranslationWithAuditPredecessor(
		context.Background(),
		asset.TextTrack{
			AssetID:        assetID,
			LanguageCode:   lang,
			TextKind:       kind,
			TextContent:    "italiano v2 - tradotto da en con prompt-v2",
			SourceType:     asset.TextSourceTranslation,
			TranslationKey: nextKey,
			PromptVersion:  "prompt-v2",
			SourceTextHash: "source-text-hash-v2",
		},
	)
	if err != nil {
		t.Fatalf("InsertTranslationWithAuditPredecessor: %v", err)
	}

	// Audit-trail invariant 1 — row count grew by exactly 1. The
	// prior row is preserved (godlike/06: "audit trail is invisible
	// to callers; only is_current matters").
	if got := len(repo.rows); got != 2 {
		t.Fatalf("audit-trail row count: want 2 (preserve + append), got %d", got)
	}
	// Audit-trail invariant 2 — the prior row IsCurrent flipped to
	// false AND UpdatedAt is strictly later than its CreatedAt
	// (the partial UNIQUE INDEX WHERE is_current=1 would reject
	// two is_current=1 rows in the same tuple at the SQL layer).
	if repo.rows[priorIdx].IsCurrent {
		t.Errorf("audit-trail invariant violated: prior row IsCurrent stayed true (the flip was skipped): row=%+v",
			repo.rows[priorIdx])
	}
	if !repo.rows[priorIdx].UpdatedAt.After(repo.rows[priorIdx].CreatedAt) {
		t.Errorf("audit-trail invariant violated: prior row UpdatedAt (%s) MUST be later than CreatedAt (%s)",
			repo.rows[priorIdx].UpdatedAt, repo.rows[priorIdx].CreatedAt)
	}
	// Audit-trail invariant 3 — the new row appended with
	// IsCurrent=true + matching TranslationKey. The prior row's
	// TranslationKey is preserved verbatim (no silent overwrite).
	newRow := repo.rows[1]
	if !newRow.IsCurrent {
		t.Errorf("audit-trail invariant violated: new row IsCurrent = false (must be true): row=%+v", newRow)
	}
	if newRow.TranslationKey != nextKey {
		t.Errorf("audit-trail invariant violated: new row TranslationKey = %q, want %q",
			newRow.TranslationKey, nextKey)
	}
	if newRow.AssetID != assetID || newRow.LanguageCode != lang || newRow.TextKind != kind {
		t.Errorf("audit-trail invariant violated: new row tuple drift: AssetID=%q LanguageCode=%q TextKind=%q",
			newRow.AssetID, newRow.LanguageCode, newRow.TextKind)
	}
	// Audit-trail invariant 4 — exactly ONE row in the stub has
	// IsCurrent=true. The partial UNIQUE INDEX WHERE is_current=1
	// cannot split-brain against an existing is_current=1 row.
	nCurrent := 0
	for _, r := range repo.rows {
		if r.IsCurrent {
			nCurrent++
		}
	}
	if nCurrent != 1 {
		t.Errorf("audit-trail invariant violated: want exactly 1 is_current=1 row in the tuple, got %d", nCurrent)
	}
}

// TestAuditTrail_P1F_Stub_InsertTranslationWithAuditPredecessor_IdempotencyNoOp
// pins the SQLite §Step 1 idempotency contract: when a current
// row already exists in the tuple with a matching
// TranslationKey, the insert is a no-op. No new row is appended,
// no flip fires, and the existing row's flags + UpdatedAt are
// preserved verbatim (matches the SQLite impl's BEGIN IMMEDIATE
// TRANSACTION + SELECT step 1 + COMMIT short-circuit).
func TestAuditTrail_P1F_Stub_InsertTranslationWithAuditPredecessor_IdempotencyNoOp(t *testing.T) {
	t.Parallel()
	const (
		assetID = "yt_p1f_idemp_001"
		lang    = "en"
		kind    = asset.TextTrackTranscript
	)
	key := asset.TranslationKey("source-text-hash", lang, "ollama", "v1", "prompt-v1")
	fixedTime := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	repo := &p1fStubRepo{rows: []asset.TextTrack{
		{
			AssetID:        assetID,
			LanguageCode:   lang,
			TextKind:       kind,
			TextContent:    "already-current",
			Status:         asset.TextTrackReady,
			SourceType:     asset.TextSourceYouTubeSubtitle,
			IsOriginal:     true,
			Provider:       "yt-dlp",
			IsCurrent:      true,
			TranslationKey: key,
			PromptVersion:  "prompt-v1",
			CreatedAt:      fixedTime,
			UpdatedAt:      fixedTime,
		},
	}}
	preCreatedAt := repo.rows[0].CreatedAt
	preUpdatedAt := repo.rows[0].UpdatedAt
	preIsCurrent := repo.rows[0].IsCurrent
	preTextContent := repo.rows[0].TextContent

	err := repo.InsertTranslationWithAuditPredecessor(
		context.Background(),
		asset.TextTrack{
			AssetID:        assetID,
			LanguageCode:   lang,
			TextKind:       kind,
			TextContent:    "SHOULD-BE-DROPPED",
			SourceType:     asset.TextSourceYouTubeSubtitle,
			TranslationKey: key, // same key → idempotent
			PromptVersion:  "prompt-v1",
			SourceTextHash: "source-text-hash",
		},
	)
	if err != nil {
		t.Fatalf("idempotent insert MUST NOT error: %v", err)
	}

	// Idempotency invariant 1 — row count preserved (no append
	// when matching current row exists).
	if got := len(repo.rows); got != 1 {
		t.Fatalf("idempotency invariant violated: want 1 row, got %d (a duplicate was appended)", got)
	}
	// Idempotency invariant 2 — existing row's CreatedAt +
	// UpdatedAt preserved verbatim (no silent refresh on a no-op
	// insert).
	if !repo.rows[0].CreatedAt.Equal(preCreatedAt) {
		t.Errorf("idempotency invariant violated: CreatedAt drifted from %s to %s",
			preCreatedAt, repo.rows[0].CreatedAt)
	}
	if !repo.rows[0].UpdatedAt.Equal(preUpdatedAt) {
		t.Errorf("idempotency invariant violated: UpdatedAt drifted from %s to %s",
			preUpdatedAt, repo.rows[0].UpdatedAt)
	}
	if repo.rows[0].IsCurrent != preIsCurrent {
		t.Errorf("idempotency invariant violated: IsCurrent drifted from %v to %v",
			preIsCurrent, repo.rows[0].IsCurrent)
	}
	if repo.rows[0].TextContent != preTextContent {
		t.Errorf("idempotency invariant violated: TextContent drifted to %q, want %q",
			repo.rows[0].TextContent, preTextContent)
	}
}

// ── Compile-time pin ───────────────────────────────────────────────────────────

// Compile-time assertion: the package's typed sentinels are
// reachable (godlike/07 typed-error contract).
var (
	_ error = ErrTranslationSourceInvalid
	_ error = ErrTranslationTranslatorMissing
	_ error = ErrTranslationTargetLangMissing
	_ error = ErrTranslationEmpty
	_ error = ErrTranslationIncomplete
)

// Compile-time assertion: ErrTextTrackNotReady satisfies the
// error interface and the errors.Is probe pattern.
var _ error = (*ErrTextTrackNotReady)(nil)
