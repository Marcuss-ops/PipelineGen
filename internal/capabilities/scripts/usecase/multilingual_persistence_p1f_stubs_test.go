// Package usecase — multilingual_persistence_p1f_stubs_test.go
//
// Shared godlike/06 SSOT helpers for the P1.F multilingual persistence
// test surface (PR-PY-CLIPS-CORRETTE-TRADOTTE, July 2026):
//   - p1fStubRepo / p1fStubSubtitles / p1fStubTranscriber (TextTrackRepository
//   - SubtitleFetcherPort + WhisperTranscriberPort typed stubs)
//   - newP1FResolver typed constructor wiring the 3 stubs into a
//     *usecase.TextTrackResolver for Groups 2/3/4 tests
//   - 3 interface-satisfaction compile-time pins (godlike/07 SSOT)
//   - 6 typed-error sentinel compile-time pins (godlike/07 typed-error contract)
//
// godlike/06 SSOT one-owner-per-fact: this file is the canonical SOLE
// definition site for the 3 shared stubs + newP1FResolver + 9 compile-pins.
// All 4 P1F group files (multilingual_8scenes / canonical_case /
// missing_translation / audit_trail_test) consume these symbols.
package usecase

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// ── Shared P1F test stubs ──────────────────────────────────────────────────────

type p1fStubRepo struct {
	mu   sync.Mutex
	rows []detail.TextTrack
}

func (s *p1fStubRepo) UpsertBatch(_ context.Context, tracks []detail.TextTrack) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows = append(s.rows, tracks...)
	return nil
}

func (s *p1fStubRepo) Find(_ context.Context, assetID, languageCode string, kind detail.TextTrackKind) (*detail.TextTrack, error) {
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
func (s *p1fStubRepo) FindReady(_ context.Context, assetID, languageCode string, kind detail.TextTrackKind) (*detail.TextTrack, []detail.TimedCue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.rows {
		if s.rows[i].AssetID == assetID &&
			s.rows[i].LanguageCode == languageCode &&
			s.rows[i].TextKind == kind &&
			s.rows[i].Status == detail.TextTrackReady {
			return &s.rows[i], nil, nil
		}
	}
	return nil, nil, nil
}

// ListReadyLanguages returns the sorted set of language
// codes for which a READY track exists.
func (s *p1fStubRepo) ListReadyLanguages(_ context.Context, assetID string, kind detail.TextTrackKind) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]struct{}{}
	var out []string
	for i := range s.rows {
		if s.rows[i].AssetID == assetID &&
			s.rows[i].TextKind == kind &&
			s.rows[i].Status == detail.TextTrackReady {
			if _, ok := seen[s.rows[i].LanguageCode]; !ok {
				seen[s.rows[i].LanguageCode] = struct{}{}
				out = append(out, s.rows[i].LanguageCode)
			}
		}
	}
	return out, nil
}

func (s *p1fStubRepo) ListByAsset(_ context.Context, assetID string) ([]detail.TextTrack, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []detail.TextTrack
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
// `var _ detail.TextTrackRepository = (*p1fStubRepo)(nil)`
// assertion at the bottom of this file pass.
func (s *p1fStubRepo) FindCurrentForTranslation(
	_ context.Context,
	_ string,
	_ detail.TextTrackKind,
	_ string,
	_ string,
	_ string,
	_ string,
	_ string,
) (*detail.TextTrack, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return nil, nil
}

// InsertTranslationWithAuditPredecessor atomically flips any
// prior is_current=1 row for the same (asset, language, kind)
// tuple whose TranslationKey differs, then appends the new
// track with IsCurrent=true and refreshed UpdatedAt. Mirrors the
// canonical SQLite implementation in
// `internal/platform/sqlite/assets/text_track_repository.go`
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
func (s *p1fStubRepo) InsertTranslationWithAuditPredecessor(_ context.Context, track detail.TextTrack) error {
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
		track.Status = detail.TextTrackReady
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
	bundle *detail.ResolvedTextBundle
	err    error
	calls  int
}

func (s *p1fStubSubtitles) SliceSubtitles(_ context.Context, _ string, _, _ int, _ string) error {
	return nil
}
func (s *p1fStubSubtitles) FetchSegmentSubtitles(_ context.Context, _ string, _, _ int) (*detail.ResolvedTextBundle, error) {
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
	det   *detail.TranscriptResult
}

func (s *p1fStubTranscriber) TranscribeAudio(_ context.Context, _ string) (string, error) {
	s.calls++
	if s.err != nil {
		return "", s.err
	}
	return s.text, nil
}

func (s *p1fStubTranscriber) TranscribeAudioWithDetection(_ context.Context, _ string) (detail.TranscriptResult, error) {
	s.calls++
	if s.err != nil {
		return detail.TranscriptResult{}, s.err
	}
	if s.det != nil {
		return *s.det, nil
	}
	return detail.TranscriptResult{Text: s.text, DetectedLanguage: ""}, nil
}

// Compile-time guarantees that the stubs satisfy the ports the
// resolver depends on.
var (
	_ detail.TextTrackRepository           = (*p1fStubRepo)(nil)
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
