package usecase_test

// text_track_resolver_test.go pins the Fase 1.a + Fase 1.b contract
// for TextTrackResolver (PR-PY-CLIPS-CORRETTE-TRADOTTE, July 2026):
//
// Fase 1.a:
//  1. The priority chain (AcquireSegmentText) covers all 5 levels and
//     short-circuits correctly.
//  2. Whisper is NEVER called when payload/DB/subtitle win.
//  3. Empty payload language defaults to BCP-47 "und" (NEVER "en").
//  4. Failed acquisition does NOT silently empty-text the bundle —
//     the resolver returns (nil, nil) when no level produces usable
//     content.
//  5. MaterializePayloadTexts emits all 4 kinds (transcript+title+
//     summary+description) per non-empty input field, each with the
//     canonical TextHash + SourceVersion populated.
//  6. Save rejects an empty Source (no silent classify-as-provided).
//
// Fase 1.b (BCP-47 + language detection, this commit):
//  7. ResolveOriginal/ResolveLanguage/ResolveBestAvailable are the
//     SOLE canonical typed lookups (no `Find(ctx, "en", ...)` residue).
//  8. Whisper priority 5 uses TranscribeAudioWithDetection — the
//     resolver consumes DetectedLanguage + Confidence.
//  9. RequireLanguageCertainty policy gate fires
//     asset.ErrLanguageUndeterminable pre-Step-9 when the chain
//     exhausts without surfacing a real BCP-47 language.
//  10. Subtitle language is filtered against PreferredLanguages
//     before the bundle is accepted (Fase 1.b: the port surfaces
//     any language it found; the resolver's policy is to discard
//     the bundle when its LanguageCode is NOT in
//     PreferredLanguages and fall through to Whisper).
//  11. The resolver NEVER substitutes "en" for an empty/unknown
//     input. All language codes flow through asset.Normalize
//     (BCP-47 canonical; empty → "und").
//
// Drift from any of the above MUST FAIL the test, not regress silently.
// All assertions use the detail.TextTrack field shape (TextHash,
// SourceVersion) defined in internal/kernel/asset/text_track.go and
// the canonical hash factory in internal/kernel/asset/text_track_hashes.go.

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// ── Stubs (in-memory mocks) ──────────────────────────────────────────────

// stubRepo is the minimal TextTrackRepository used by the chain tests.
// Stores rows in-memory; ListByAsset filters by assetID.
type stubRepo struct {
	rows []detail.TextTrack
}

func (s *stubRepo) UpsertBatch(_ context.Context, tracks []detail.TextTrack) error {
	s.rows = append(s.rows, tracks...)
	return nil
}

func (s *stubRepo) Find(_ context.Context, assetID, languageCode string, kind detail.TextTrackKind) (*detail.TextTrack, error) {
	for i := range s.rows {
		if s.rows[i].AssetID == assetID &&
			s.rows[i].LanguageCode == languageCode &&
			s.rows[i].TextKind == kind {
			return &s.rows[i], nil
		}
	}
	return nil, nil
}

// FindReady is the canonical Fase 1.b READY-only lookup (PR-PY-CLIPS-CORRETTE-TRADOTTE
// Fase 1.b). The stub returns the row when status=READY and ignores
// PENDING/FAILED rows (matches the production contract: a non-READY
// row is not authoritative, the resolver surfaces nil).
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 4 (July 2026): the signature
// changed from `(*TextTrack, error)` to `(*TextTrack, []TimedCue, error)`
// to match the canonical port surface. The timed cues are returned
// alongside the track (nil here, since the stub stores no cues).
func (s *stubRepo) FindReady(_ context.Context, assetID, languageCode string, kind detail.TextTrackKind) (*detail.TextTrack, []detail.TimedCue, error) {
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

// ListReadyLanguages returns the sorted set of language codes
// for which a READY track exists. PR-PY-CLIPS-CORRETTE-TRADOTTE
// Fase 4 (July 2026): added to satisfy the canonical port surface.
func (s *stubRepo) ListReadyLanguages(_ context.Context, assetID string, kind detail.TextTrackKind) ([]string, error) {
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

func (s *stubRepo) ListByAsset(_ context.Context, assetID string) ([]detail.TextTrack, error) {
	var out []detail.TextTrack
	for _, r := range s.rows {
		if r.AssetID == assetID {
			out = append(out, r)
		}
	}
	return out, nil
}

// FindCurrentForTranslation + InsertTranslationWithAuditPredecessor
// are the PR-CATALOG-MULTILINGUA step-4 surface added to the
// canonical TextTrackRepository port. stubRepo backs the
// TextTrackResolver priority-chain tests, which exercise
// FindReady + ListReadyLanguages + Save (no LookupBeforeTranslate
// gate). Both new methods mirror the null-write contract of
// p1fStubRepo / fakeTextTrackRepo (see materializer_test.go).
func (s *stubRepo) FindCurrentForTranslation(_ context.Context, _ string, _ detail.TextTrackKind, _, _, _, _, _ string) (*detail.TextTrack, error) {
	return nil, nil
}
func (s *stubRepo) InsertTranslationWithAuditPredecessor(_ context.Context, _ detail.TextTrack) error {
	return nil
}

// stubSubtitles records FetchSegmentSubtitles invocations and returns
// a deterministic bundle.
type stubSubtitles struct {
	bundle *detail.ResolvedTextBundle
	err    error
	calls  int
}

func (s *stubSubtitles) SliceSubtitles(_ context.Context, _ string, _, _ int, _ string) error {
	return nil
}
func (s *stubSubtitles) FetchSegmentSubtitles(_ context.Context, _ string, _, _ int) (*detail.ResolvedTextBundle, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.bundle, nil
}

// stubTranscriber records TranscribeAudio + TranscribeAudioWithDetection
// invocations and returns deterministic output.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b: the new typed method
// (TranscribeAudioWithDetection) is the canonical chain surface.
// The legacy plain-string method is RETAINED on the port for
// back-compat with the Step 10 metadata path.
type stubTranscriber struct {
	text  string
	err   error
	calls int
	// det overrides the legacy `text` field for the typed
	// TranscribeAudioWithDetection method (Fase 1.b). nil means
	// the stub returns TranscriptResult{Text: text, DetectedLanguage: ""}.
	det *detail.TranscriptResult
}

func (s *stubTranscriber) TranscribeAudio(_ context.Context, _ string) (string, error) {
	s.calls++
	if s.err != nil {
		return "", s.err
	}
	return s.text, nil
}

func (s *stubTranscriber) TranscribeAudioWithDetection(_ context.Context, _ string) (detail.TranscriptResult, error) {
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
	_ detail.TextTrackRepository           = (*stubRepo)(nil)
	_ youtubeports.SubtitleFetcherPort    = (*stubSubtitles)(nil)
	_ youtubeports.WhisperTranscriberPort = (*stubTranscriber)(nil)
)

func newTestResolver(repo *stubRepo, subs *stubSubtitles, trans *stubTranscriber) *usecase.TextTrackResolver {
	return &usecase.TextTrackResolver{
		Repo:        repo,
		Subtitles:   subs,
		Transcriber: trans,
		Log:         zap.NewNop(),
	}
}

// ── AcquireSegmentText: priority chain (Fase 1.a) ───────────────────────

func TestAcquireSegmentText_PayloadWins(t *testing.T) {
	resolver := newTestResolver(&stubRepo{}, nil, nil)
	bundle, err := resolver.AcquireSegmentText(context.Background(), usecase.TextTrackAcquireRequest{
		ClipID:  "yt_phase1a_001",
		VideoID: "v001",
		PayloadTexts: []youtubetypes.LocalizedClipText{
			{LanguageCode: "en", Transcript: "Hello world"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if bundle == nil {
		t.Fatal("bundle should not be nil")
	}
	if bundle.PlainText != "Hello world" {
		t.Fatalf("PlainText = %q, want %q", bundle.PlainText, "Hello world")
	}
	if bundle.LanguageCode != "en" {
		t.Fatalf("LanguageCode = %q, want %q", bundle.LanguageCode, "en")
	}
	if bundle.SourceType != detail.TextSourceProvided {
		t.Fatalf("SourceType = %q, want %q", bundle.SourceType, detail.TextSourceProvided)
	}
	if !bundle.IsOriginal {
		t.Fatal("IsOriginal should be true for a payload-provided transcript")
	}
}

func TestAcquireSegmentText_PayloadEmptyLanguageCoercedToUnd(t *testing.T) {
	// godlike/07 honest lock: the legacy `if lang == "" { lang = "en" }`
	// was the root cause of the "Italian original stored as English"
	// audit finding. Empty languageCode MUST collapse to BCP-47 "und".
	resolver := newTestResolver(&stubRepo{}, nil, nil)
	bundle, err := resolver.AcquireSegmentText(context.Background(), usecase.TextTrackAcquireRequest{
		ClipID:  "yt_phase1a_002",
		VideoID: "v002",
		PayloadTexts: []youtubetypes.LocalizedClipText{
			{LanguageCode: "", Transcript: "ciao mondo"},
		},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if bundle == nil || bundle.LanguageCode != "und" {
		t.Fatalf("LanguageCode should be %q, got %+v", "und", bundle)
	}
}

func TestAcquireSegmentText_DBWinsWhenPayloadEmpty(t *testing.T) {
	repo := &stubRepo{rows: []detail.TextTrack{
		{
			AssetID:      "yt_phase1a_003",
			LanguageCode: "it",
			TextKind:     detail.TextTrackTranscript,
			TextContent:  "Ciao dal DB",
			Status:       detail.TextTrackReady,
			SourceType:   detail.TextSourceYouTubeSubtitle,
			IsOriginal:   true,
		},
	}}
	resolver := newTestResolver(repo, nil, nil)
	bundle, err := resolver.AcquireSegmentText(context.Background(), usecase.TextTrackAcquireRequest{
		ClipID:             "yt_phase1a_003",
		PreferredLanguages: []string{"it", "en"},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if bundle == nil || bundle.PlainText != "Ciao dal DB" {
		t.Fatalf("DB priority should fire; got %+v", bundle)
	}
	if bundle.LanguageCode != "it" {
		t.Fatalf("LanguageCode = %q, want %q", bundle.LanguageCode, "it")
	}
}

func TestAcquireSegmentText_SubtitlesWinAndWhisperNotCalled(t *testing.T) {
	subs := &stubSubtitles{bundle: &detail.ResolvedTextBundle{
		LanguageCode: "es",
		PlainText:    "Hola mundo",
		Cues: []detail.TimedCue{
			{StartMs: 0, EndMs: 2000, Text: "Hola"},
		},
		SourceType: detail.TextSourceYouTubeSubtitle,
		IsOriginal: true,
		Provider:   "yt-dlp",
	}}
	trans := &stubTranscriber{text: "should not be called"}
	resolver := newTestResolver(&stubRepo{}, subs, trans)

	bundle, err := resolver.AcquireSegmentText(context.Background(), usecase.TextTrackAcquireRequest{
		ClipID:    "yt_phase1a_004",
		VideoID:   "v004",
		LocalPath: "/tmp/x.mp4",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if bundle == nil || bundle.LanguageCode != "es" {
		t.Fatalf("expected es, got %+v", bundle)
	}
	if len(bundle.Cues) != 1 {
		t.Fatalf("Cues should carry VTT timings, got len=%d", len(bundle.Cues))
	}
	if trans.calls != 0 {
		t.Fatalf("Whisper MUST NOT be called when subtitles win; got %d calls", trans.calls)
	}
}

func TestAcquireSegmentText_WhisperFallbackWhenOthersEmpty(t *testing.T) {
	// Fase 1.b: stubTranscriber uses TranscribeAudioWithDetection
	// internally (the resolver calls the typed method on priority
	// 5). The stub returns TranscriptResult{Text: "Whisper text",
	// DetectedLanguage: ""} which the resolver normalizes to
	// "und" (BCP-47 undetermined).
	subs := &stubSubtitles{bundle: nil} // valid "not found"
	trans := &stubTranscriber{text: "Whisper text"}
	resolver := newTestResolver(&stubRepo{}, subs, trans)

	bundle, err := resolver.AcquireSegmentText(context.Background(), usecase.TextTrackAcquireRequest{
		ClipID:    "yt_phase1a_005",
		VideoID:   "v005",
		LocalPath: "/tmp/x.mp4",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if bundle == nil || bundle.PlainText != "Whisper text" {
		t.Fatalf("Whisper fallback should fire; got %+v", bundle)
	}
	if bundle.LanguageCode != "und" {
		t.Fatalf("Whisper with empty DetectedLanguage should normalize to %q, got %q", "und", bundle.LanguageCode)
	}
	if bundle.SourceType != detail.TextSourceWhisper {
		t.Fatalf("SourceType = %q, want %q", bundle.SourceType, detail.TextSourceWhisper)
	}
	if trans.calls != 1 {
		t.Fatalf("Whisper should be called exactly once, got %d", trans.calls)
	}
}

// TestAcquireSegmentText_WhisperDetectsLanguage is the Fase 1.b typed-
// port test: the Whisper stub returns a non-empty DetectedLanguage,
// and the resolver's AcquireSegmentText propagates it (normalized
// via the canonical bcp47.Normalize helper).
func TestAcquireSegmentText_WhisperDetectsLanguage(t *testing.T) {
	subs := &stubSubtitles{bundle: nil}
	trans := &stubTranscriber{det: &detail.TranscriptResult{
		Text:             "Buongiorno a tutti",
		DetectedLanguage: "IT", // uppercase → normalize to "it"
	}}
	resolver := newTestResolver(&stubRepo{}, subs, trans)
	bundle, err := resolver.AcquireSegmentText(context.Background(), usecase.TextTrackAcquireRequest{
		ClipID:    "yt_phase1b_det_001",
		VideoID:   "v_det_001",
		LocalPath: "/tmp/x.mp4",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if bundle == nil || bundle.LanguageCode != "it" {
		t.Fatalf("Whisper DetectedLanguage=IT should normalize to %q, got %+v", "it", bundle)
	}
}

// TestAcquireSegmentText_WhisperPropagatesCues pins the priority-5
// fix (PR-PY-CLIPS-CORRETTE-TRADOTTE, Aug 2026): the Whisper branch of
// AcquireSegmentText MUST propagate det.Cues verbatim into the
// ResolvedTextBundle — a Whisper-transcribed clip keeps its timed
// segments (asset_text_track_segments) instead of degrading to plain
// text with no timing (which broke SRT/VTT/ASS generation).
func TestAcquireSegmentText_WhisperPropagatesCues(t *testing.T) {
	cues := []detail.TimedCue{
		{StartMs: 0, EndMs: 1200, Text: "Hello"},
		{StartMs: 1200, EndMs: 2400, Text: "world"},
	}
	subs := &stubSubtitles{bundle: nil} // valid "not found" → fall through to Whisper
	trans := &stubTranscriber{det: &detail.TranscriptResult{
		Text:             "Hello world",
		DetectedLanguage: "en",
		Cues:             cues,
	}}
	resolver := newTestResolver(&stubRepo{}, subs, trans)

	bundle, err := resolver.AcquireSegmentText(context.Background(), usecase.TextTrackAcquireRequest{
		ClipID:    "yt_whisper_cues_001",
		VideoID:   "v_whisper_cues_001",
		LocalPath: "/tmp/x.mp4",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if bundle == nil {
		t.Fatal("bundle should not be nil")
	}
	if bundle.SourceType != detail.TextSourceWhisper {
		t.Fatalf("SourceType = %q, want %q", bundle.SourceType, detail.TextSourceWhisper)
	}
	if bundle.LanguageCode != "en" {
		t.Fatalf("LanguageCode = %q, want %q", bundle.LanguageCode, "en")
	}
	if len(bundle.Cues) != len(cues) {
		t.Fatalf("Cues should propagate det.Cues verbatim; got len=%d want %d", len(bundle.Cues), len(cues))
	}
	for i := range cues {
		if bundle.Cues[i] != cues[i] {
			t.Fatalf("cue %d mismatch: got %+v want %+v", i, bundle.Cues[i], cues[i])
		}
	}
}

func TestAcquireSegmentText_SubtitleErrorFallsThroughToWhisper(t *testing.T) {
	// godlike/07: subtitle errors are LOGGED + SWALLOWED so the chain
	// can fall through to Whisper. Whisper errors are propagated.
	subs := &stubSubtitles{err: errStub()} // typed error
	trans := &stubTranscriber{text: "from whisper"}
	resolver := newTestResolver(&stubRepo{}, subs, trans)

	bundle, err := resolver.AcquireSegmentText(context.Background(), usecase.TextTrackAcquireRequest{
		ClipID:    "yt_phase1a_006",
		VideoID:   "v006",
		LocalPath: "/tmp/x.mp4",
	})
	if err != nil {
		t.Fatalf("subtitle error should NOT bubble; got %v", err)
	}
	if bundle == nil || bundle.PlainText != "from whisper" {
		t.Fatalf("Whisper should fire after subtitle error; got %+v", bundle)
	}
}

func TestAcquireSegmentText_NilWhenAllLevelsFail(t *testing.T) {
	resolver := newTestResolver(&stubRepo{}, &stubSubtitles{bundle: nil}, &stubTranscriber{text: ""})
	bundle, err := resolver.AcquireSegmentText(context.Background(), usecase.TextTrackAcquireRequest{
		ClipID:    "yt_phase1a_007",
		VideoID:   "v007",
		LocalPath: "/tmp/x.mp4",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if bundle != nil {
		t.Fatalf("expected (nil, nil) on full miss; got %+v", bundle)
	}
}

// ── Fase 1.b: ErrLanguageUndeterminable policy gate ─────────────────────

// TestAcquireSegmentText_RequireLanguageCertaintyFiresError is the
// godlike/07 fail-closed policy gate: when the chain exhausts AND
// RequireLanguageCertainty=true, the resolver MUST surface
// asset.ErrLanguageUndeterminable (pre-Step-9) instead of degrading
// to (nil, nil).
func TestAcquireSegmentText_RequireLanguageCertaintyFiresError(t *testing.T) {
	resolver := &usecase.TextTrackResolver{
		Repo:                     &stubRepo{},
		Subtitles:                &stubSubtitles{bundle: nil},
		Transcriber:              &stubTranscriber{text: ""}, // Whisper returns no language
		Log:                      zap.NewNop(),
		RequireLanguageCertainty: true,
	}
	_, err := resolver.AcquireSegmentText(context.Background(), usecase.TextTrackAcquireRequest{
		ClipID:    "yt_phase1b_cer_001",
		VideoID:   "v_cer_001",
		LocalPath: "/tmp/x.mp4",
	})
	if err == nil {
		t.Fatal("RequireLanguageCertainty=true with full-miss chain MUST return ErrLanguageUndeterminable")
	}
	if !asset.IsLanguageUndeterminable(err) {
		t.Fatalf("err must be errors.As-probeable as *asset.ErrLanguageUndeterminable; got %T %v", err, err)
	}
}

// TestAcquireSegmentText_NoCertaintySilentlyDegrades is the negative
// case: RequireLanguageCertainty=false preserves the pre-Fase-1.b
// behavior (chain miss → (nil, nil), no error).
func TestAcquireSegmentText_NoCertaintySilentlyDegrades(t *testing.T) {
	resolver := newTestResolver(&stubRepo{}, &stubSubtitles{bundle: nil}, &stubTranscriber{text: ""})
	bundle, err := resolver.AcquireSegmentText(context.Background(), usecase.TextTrackAcquireRequest{
		ClipID:    "yt_phase1b_cer_002",
		VideoID:   "v_cer_002",
		LocalPath: "/tmp/x.mp4",
	})
	if err != nil {
		t.Fatalf("RequireLanguageCertainty=false should NOT fire; got %v", err)
	}
	if bundle != nil {
		t.Fatalf("expected (nil, nil) on full miss; got %+v", bundle)
	}
}

// ── Fase 1.b: Subtitle language filtered against PreferredLanguages ─────

// TestAcquireSegmentText_SubtitleLanguageNotInPreferredFallsThrough
// is the Fase 1.b filtering contract: the SubtitleFetcherPort
// surfaces whatever language it found (e.g. "en" from a video with
// only English subs); the resolver filters against
// PreferredLanguages and falls through to Whisper when the
// surface doesn't match.
func TestAcquireSegmentText_SubtitleLanguageNotInPreferredFallsThrough(t *testing.T) {
	subs := &stubSubtitles{bundle: &detail.ResolvedTextBundle{
		LanguageCode: "en",
		PlainText:    "English subs",
		SourceType:   detail.TextSourceYouTubeSubtitle,
		IsOriginal:   true,
	}}
	trans := &stubTranscriber{det: &detail.TranscriptResult{
		Text:             "Texto en español",
		DetectedLanguage: "es",
	}}
	resolver := newTestResolver(&stubRepo{}, subs, trans)
	bundle, err := resolver.AcquireSegmentText(context.Background(), usecase.TextTrackAcquireRequest{
		ClipID:             "yt_phase1b_filter_001",
		VideoID:            "v_filt_001",
		LocalPath:          "/tmp/x.mp4",
		PreferredLanguages: []string{"it", "es"}, // "en" NOT in list
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if bundle == nil {
		t.Fatal("Whisper should fire after subtitle-language filter rejection; got nil bundle")
	}
	if bundle.SourceType != detail.TextSourceWhisper {
		t.Fatalf("expected Whisper (SourceType=%q), got %q", detail.TextSourceWhisper, bundle.SourceType)
	}
	if bundle.LanguageCode != "es" {
		t.Fatalf("Whisper DetectedLanguage=es should normalize to %q, got %q", "es", bundle.LanguageCode)
	}
}

// ── Fase 1.b: ResolveOriginal/ResolveLanguage/ResolveBestAvailable ─────

func TestResolveOriginal_PayloadWins(t *testing.T) {
	resolver := newTestResolver(&stubRepo{}, nil, nil)
	bundle, err := resolver.ResolveOriginal(context.Background(), "yt_phase1b_001", []youtubetypes.LocalizedClipText{
		{LanguageCode: "en", Transcript: "Hello"},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if bundle == nil || bundle.PlainText != "Hello" {
		t.Fatalf("ResolveOriginal should fire on payload; got %+v", bundle)
	}
}

func TestResolveOriginal_EmptyPayloadReturnsNil(t *testing.T) {
	resolver := newTestResolver(&stubRepo{}, nil, nil)
	bundle, err := resolver.ResolveOriginal(context.Background(), "yt_phase1b_002", nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if bundle != nil {
		t.Fatalf("expected (nil, nil) on empty payload; got %+v", bundle)
	}
}

func TestResolveOriginal_NormalizesLanguage(t *testing.T) {
	// godlike/07: language code is BCP-47 normalized. Uppercase
	// "EN-US" → "en-US". Empty → "und". Underscore-separated
	// locales (e.g. "pt_BR") are REJECTED per Fase 1.b strict
	// BCP-47 enforcement (BCP-47 strictly uses hyphen, not underscore).
	resolver := newTestResolver(&stubRepo{}, nil, nil)
	cases := []struct {
		in   string
		want string
	}{
		{"EN-US", "en-US"},
		{"EN", "en"},
		{"pt-BR", "pt-BR"},
		{"", "und"},
	}
	for _, c := range cases {
		bundle, err := resolver.ResolveOriginal(context.Background(), "yt_phase1b_003", []youtubetypes.LocalizedClipText{
			{LanguageCode: c.in, Transcript: "x"},
		})
		if err != nil {
			t.Fatalf("ResolveOriginal(%q) err: %v", c.in, err)
		}
		if bundle == nil || bundle.LanguageCode != c.want {
			t.Errorf("ResolveOriginal(%q) LanguageCode = %+v, want %q", c.in, bundle, c.want)
		}
	}
}

// TestResolveOriginal_RejectsUnderscoreLanguage pins the Fase 1.b
// strict BCP-47 enforcement: underscore-separated locales like
// "pt_BR" are REJECTED (BCP-47 strictly uses hyphen, not underscore).
// This is the user-spec requirement "Rifiutare varianti miste tipo
// pt_br" — mixed variants with non-BCP-47 separators.
func TestResolveOriginal_RejectsUnderscoreLanguage(t *testing.T) {
	resolver := newTestResolver(&stubRepo{}, nil, nil)
	underscoreInputs := []string{"pt_BR", "en_US", "pt_br", "en_us", "it_IT"}
	for _, in := range underscoreInputs {
		_, err := resolver.ResolveOriginal(context.Background(), "yt_phase1b_underscore", []youtubetypes.LocalizedClipText{
			{LanguageCode: in, Transcript: "x"},
		})
		if err == nil {
			t.Errorf("ResolveOriginal(%q) MUST return error (underscore separator rejected); got nil", in)
		}
	}
}

func TestResolveOriginal_RejectsMalformedLanguage(t *testing.T) {
	resolver := newTestResolver(&stubRepo{}, nil, nil)
	_, err := resolver.ResolveOriginal(context.Background(), "yt_phase1b_004", []youtubetypes.LocalizedClipText{
		{LanguageCode: "portuguese", Transcript: "x"},
	})
	if err == nil {
		t.Fatal("malformed language code MUST return error; got nil")
	}
}

func TestResolveLanguage_DBFindsReady(t *testing.T) {
	repo := &stubRepo{rows: []detail.TextTrack{
		{
			AssetID:      "yt_phase1b_005",
			LanguageCode: "it",
			TextKind:     detail.TextTrackTranscript,
			TextContent:  "Ciao",
			Status:       detail.TextTrackReady,
		},
	}}
	resolver := newTestResolver(repo, nil, nil)
	row, err := resolver.ResolveLanguage(context.Background(), "yt_phase1b_005", "it", detail.TextTrackTranscript)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if row == nil || row.TextContent != "Ciao" {
		t.Fatalf("ResolveLanguage should find the it row; got %+v", row)
	}
}

func TestResolveLanguage_IgnoresNonReady(t *testing.T) {
	// godlike/07: PENDING/FAILED rows are not authoritative. The
	// resolver must NOT surface a non-READY row even when the row
	// exists (Fase 4 video-pipeline contract).
	repo := &stubRepo{rows: []detail.TextTrack{
		{
			AssetID:      "yt_phase1b_006",
			LanguageCode: "it",
			TextKind:     detail.TextTrackTranscript,
			TextContent:  "PENDING",
			Status:       detail.TextTrackPending,
		},
	}}
	resolver := newTestResolver(repo, nil, nil)
	row, err := resolver.ResolveLanguage(context.Background(), "yt_phase1b_006", "it", detail.TextTrackTranscript)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if row != nil {
		t.Fatalf("non-READY row MUST be treated as not-found; got %+v", row)
	}
}

func TestResolveBestAvailable_PicksFirst(t *testing.T) {
	repo := &stubRepo{rows: []detail.TextTrack{
		{
			AssetID:      "yt_phase1b_007",
			LanguageCode: "es",
			TextKind:     detail.TextTrackTranscript,
			TextContent:  "Hola",
			Status:       detail.TextTrackReady,
		},
	}}
	resolver := newTestResolver(repo, nil, nil)
	row, err := resolver.ResolveBestAvailable(context.Background(), "yt_phase1b_007",
		[]string{"it", "es", "fr"}, detail.TextTrackTranscript)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if row == nil || row.LanguageCode != "es" {
		t.Fatalf("ResolveBestAvailable should pick 'es' from preferred list; got %+v", row)
	}
}

func TestResolveBestAvailable_EmptyListReturnsNil(t *testing.T) {
	resolver := newTestResolver(&stubRepo{rows: []detail.TextTrack{
		{AssetID: "yt_phase1b_008", LanguageCode: "en", TextKind: detail.TextTrackTranscript, TextContent: "x", Status: detail.TextTrackReady},
	}}, nil, nil)
	row, err := resolver.ResolveBestAvailable(context.Background(), "yt_phase1b_008", nil, detail.TextTrackTranscript)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if row != nil {
		t.Fatalf("empty PreferredLanguages should NOT probe DB; got %+v", row)
	}
}

// ── MaterializePayloadTexts: all kinds, hashes populated (Fase 1.a) ─────

func TestMaterializePayloadTexts_AllFourKinds(t *testing.T) {
	resolver := newTestResolver(&stubRepo{}, nil, nil)
	texts := []youtubetypes.LocalizedClipText{
		{
			LanguageCode: "en",
			Transcript:   "Hello",
			Title:        "My Title",
			Summary:      "My summary",
			Description:  "My description",
			SourceType:   "provided",
			IsOriginal:   true,
		},
	}
	rows := resolver.MaterializePayloadTexts("yt_phase1a_008", texts)
	if len(rows) != 4 {
		t.Fatalf("expected 4 rows (transcript+title+summary+description), got %d", len(rows))
	}
	for _, r := range rows {
		if r.TextHash == "" {
			t.Fatalf("TextHash empty for TextKind=%s (canonical factory MUST be called)", r.TextKind)
		}
		if r.SourceVersion == "" {
			t.Fatalf("SourceVersion empty for TextKind=%s (canonical factory MUST be called)", r.TextKind)
		}
		if r.Status != detail.TextTrackReady {
			t.Fatalf("Status for TextKind=%s = %q, want READY", r.TextKind, r.Status)
		}
	}
	// Verify text_kind coverage exactly matches the input.
	seen := map[detail.TextTrackKind]bool{}
	for _, r := range rows {
		seen[r.TextKind] = true
	}
	for _, k := range []detail.TextTrackKind{
		detail.TextTrackTranscript, detail.TextTrackTitle,
		detail.TextTrackSummary, detail.TextTrackDescription,
	} {
		if !seen[k] {
			t.Fatalf("missing row for TextKind=%s", k)
		}
	}
}

func TestMaterializePayloadTexts_HashesAreStableAcrossCalls(t *testing.T) {
	resolver := newTestResolver(&stubRepo{}, nil, nil)
	texts := []youtubetypes.LocalizedClipText{
		{LanguageCode: "en", Transcript: "Hello"},
	}
	a := resolver.MaterializePayloadTexts("clipA", texts)
	b := resolver.MaterializePayloadTexts("clipB", texts)
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("unexpected row counts: %d / %d", len(a), len(b))
	}
	// TextHash + SourceVersion must NOT depend on clipID (the
	// canonical hash factory formula does not include clipID —
	// the row identity is enforced by the UNIQUE(asset_id,
	// language_code, text_kind) constraint downstream).
	if a[0].TextHash != b[0].TextHash {
		t.Fatalf("TextHash unexpectedly depends on clipID: %s vs %s", a[0].TextHash, b[0].TextHash)
	}
	if a[0].SourceVersion != b[0].SourceVersion {
		t.Fatalf("SourceVersion unexpectedly depends on clipID")
	}
}

// ── Save: empty language → "und"; empty Source → error (Fase 1.a) ──────

func TestSave_DefaultsEmptyLanguageToUnd(t *testing.T) {
	repo := &stubRepo{}
	resolver := newTestResolver(repo, nil, nil)
	if err := resolver.Save(context.Background(), "yt_phase1a_009", "x", detail.TextSourceProvided, ""); err != nil {
		t.Fatalf("Save err: %v", err)
	}
	if len(repo.rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(repo.rows))
	}
	if repo.rows[0].LanguageCode != "und" {
		t.Fatalf("LanguageCode = %q, want %q", repo.rows[0].LanguageCode, "und")
	}
}

func TestSave_RejectsEmptySource(t *testing.T) {
	resolver := newTestResolver(&stubRepo{}, nil, nil)
	err := resolver.Save(context.Background(), "yt_phase1a_010", "x", detail.TextTrackSource(""), "en")
	if err == nil {
		t.Fatal("expected typed error for empty Source")
	}
}

// ── SaveMany: nil/empty safety ───────────────────────────────────────────

func TestSaveMany_NilSliceIsNoOp(t *testing.T) {
	repo := &stubRepo{}
	resolver := newTestResolver(repo, nil, nil)
	if err := resolver.SaveMany(context.Background(), nil); err != nil {
		t.Fatalf("SaveMany(nil) err: %v", err)
	}
	if len(repo.rows) != 0 {
		t.Fatalf("empty/nil slice MUST NOT append rows; got %d", len(repo.rows))
	}
}

func TestSaveMany_PopulatesBatches(t *testing.T) {
	repo := &stubRepo{}
	resolver := newTestResolver(repo, nil, nil)
	rows := []detail.TextTrack{
		{AssetID: "c1", LanguageCode: "en", TextKind: detail.TextTrackTranscript, TextContent: "a", Status: detail.TextTrackReady, TextHash: "h1"},
		{AssetID: "c1", LanguageCode: "it", TextKind: detail.TextTrackTranscript, TextContent: "b", Status: detail.TextTrackReady, TextHash: "h2"},
	}
	if err := resolver.SaveMany(context.Background(), rows); err != nil {
		t.Fatalf("SaveMany err: %v", err)
	}
	if len(repo.rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(repo.rows))
	}
}

// ── Helpers ──────────────────────────────────────────────────────────────

type stubError struct{}

func (stubError) Error() string { return "stub" }

func errStub() error { return stubError{} }

// ensureIsErrLanguageUndeterminable_GoldenMessage pins the godlike/07
// honest-lock contract: the typed error MUST carry the assetID
// verbatim so operator dashboards can correlate to the right clip.
func TestErrLanguageUndeterminable_AssetIDPropagated(t *testing.T) {
	// Indirect pin: simulate the policy-gate path and ensure the
	// returned error is probeable AND carries the assetID.
	resolver := &usecase.TextTrackResolver{
		Repo:                     &stubRepo{},
		Subtitles:                &stubSubtitles{bundle: nil},
		Transcriber:              &stubTranscriber{text: ""},
		Log:                      zap.NewNop(),
		RequireLanguageCertainty: true,
	}
	_, err := resolver.AcquireSegmentText(context.Background(), usecase.TextTrackAcquireRequest{
		ClipID:    "yt_pin_001",
		VideoID:   "v_pin_001",
		LocalPath: "/tmp/x.mp4",
	})
	if !asset.IsLanguageUndeterminable(err) {
		t.Fatalf("expected ErrLanguageUndeterminable; got %T %v", err, err)
	}
	var typed *asset.ErrLanguageUndeterminable
	if !errors.As(err, &typed) {
		t.Fatalf("errors.As must probe the typed error; got %T", err)
	}
	if typed.AssetID != "yt_pin_001" {
		t.Fatalf("AssetID = %q, want %q", typed.AssetID, "yt_pin_001")
	}
}
