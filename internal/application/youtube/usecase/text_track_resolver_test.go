package usecase_test

// text_track_resolver_test.go pins the Fase 1.a contract for
// TextTrackResolver (PR-PY-CLIPS-CORRETTE-TRADOTTE, July 2026):
//
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
// Drift from any of the above MUST FAIL the test, not regress silently.
// All assertions use the asset.TextTrack field shape (TextHash,
// SourceVersion) defined in internal/domain/asset/text_track.go and
// the canonical hash factory in internal/domain/asset/text_track_hashes.go.

import (
	"context"
	"testing"

	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/application/youtube/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// ── Stubs (in-memory mocks) ──────────────────────────────────────────────

// stubRepo is the minimal TextTrackRepository used by the chain tests.
// Stores rows in-memory; ListByAsset filters by assetID.
type stubRepo struct {
	rows []asset.TextTrack
}

func (s *stubRepo) UpsertBatch(_ context.Context, tracks []asset.TextTrack) error {
	s.rows = append(s.rows, tracks...)
	return nil
}

func (s *stubRepo) Find(_ context.Context, assetID, languageCode string, kind asset.TextTrackKind) (*asset.TextTrack, error) {
	for i := range s.rows {
		if s.rows[i].AssetID == assetID &&
			s.rows[i].LanguageCode == languageCode &&
			s.rows[i].TextKind == kind {
			return &s.rows[i], nil
		}
	}
	return nil, nil
}

func (s *stubRepo) ListByAsset(_ context.Context, assetID string) ([]asset.TextTrack, error) {
	var out []asset.TextTrack
	for _, r := range s.rows {
		if r.AssetID == assetID {
			out = append(out, r)
		}
	}
	return out, nil
}

// stubSubtitles records FetchSegmentSubtitles invocations and returns
// a deterministic bundle.
type stubSubtitles struct {
	bundle *asset.ResolvedTextBundle
	err    error
	calls  int
}

func (s *stubSubtitles) SliceSubtitles(_ context.Context, _ string, _, _ int, _ string) error {
	return nil
}
func (s *stubSubtitles) FetchSegmentSubtitles(_ context.Context, _ string, _, _ int) (*asset.ResolvedTextBundle, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.bundle, nil
}

// stubTranscriber records TranscribeAudio invocations and returns
// deterministic text.
type stubTranscriber struct {
	text  string
	err   error
	calls int
}

func (s *stubTranscriber) TranscribeAudio(_ context.Context, _ string) (string, error) {
	s.calls++
	if s.err != nil {
		return "", s.err
	}
	return s.text, nil
}

// Compile-time guarantees that the stubs satisfy the ports the
// resolver depends on.
var (
	_ asset.TextTrackRepository           = (*stubRepo)(nil)
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

// ── AcquireSegmentText: priority chain ───────────────────────────────────

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
	if bundle.SourceType != asset.TextSourceProvided {
		t.Fatalf("SourceType = %q, want %q", bundle.SourceType, asset.TextSourceProvided)
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
	repo := &stubRepo{rows: []asset.TextTrack{
		{
			AssetID:      "yt_phase1a_003",
			LanguageCode: "it",
			TextKind:     asset.TextTrackTranscript,
			TextContent:  "Ciao dal DB",
			Status:       asset.TextTrackReady,
			SourceType:   asset.TextSourceYouTubeSubtitle,
			IsOriginal:   true,
		},
	}}
	resolver := newTestResolver(repo, nil, nil)
	bundle, err := resolver.AcquireSegmentText(context.Background(), usecase.TextTrackAcquireRequest{
		ClipID: "yt_phase1a_003",
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
	subs := &stubSubtitles{bundle: &asset.ResolvedTextBundle{
		LanguageCode: "es",
		PlainText:    "Hola mundo",
		Cues: []asset.TimedCue{
			{StartMs: 0, EndMs: 2000, Text: "Hola"},
		},
		SourceType: asset.TextSourceYouTubeSubtitle,
		IsOriginal: true,
		Provider:   "yt-dlp",
	}}
	trans := &stubTranscriber{text: "should not be called"}
	resolver := newTestResolver(&stubRepo{}, subs, trans)

	bundle, err := resolver.AcquireSegmentText(context.Background(), usecase.TextTrackAcquireRequest{
		ClipID: "yt_phase1a_004", VideoID: "v004", LocalPath: "/tmp/x.mp4",
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
	subs := &stubSubtitles{bundle: nil} // valid "not found"
	trans := &stubTranscriber{text: "Whisper text"}
	resolver := newTestResolver(&stubRepo{}, subs, trans)

	bundle, err := resolver.AcquireSegmentText(context.Background(), usecase.TextTrackAcquireRequest{
		ClipID: "yt_phase1a_005", VideoID: "v005", LocalPath: "/tmp/x.mp4",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if bundle == nil || bundle.PlainText != "Whisper text" {
		t.Fatalf("Whisper fallback should fire; got %+v", bundle)
	}
	if bundle.LanguageCode != "und" {
		t.Fatalf("Whisper should report LanguageCode=%q (Fase 1.b will surface DetectedLanguage), got %q", "und", bundle.LanguageCode)
	}
	if bundle.SourceType != asset.TextSourceWhisper {
		t.Fatalf("SourceType = %q, want %q", bundle.SourceType, asset.TextSourceWhisper)
	}
	if trans.calls != 1 {
		t.Fatalf("Whisper should be called exactly once, got %d", trans.calls)
	}
}

func TestAcquireSegmentText_SubtitleErrorFallsThroughToWhisper(t *testing.T) {
	// godlike/07: subtitle errors are LOGGED + SWALLOWED so the chain
	// can fall through to Whisper. Whisper errors are propagated.
	subs := &stubSubtitles{err: errStub()} // typed error
	trans := &stubTranscriber{text: "from whisper"}
	resolver := newTestResolver(&stubRepo{}, subs, trans)

	bundle, err := resolver.AcquireSegmentText(context.Background(), usecase.TextTrackAcquireRequest{
		ClipID: "yt_phase1a_006", VideoID: "v006", LocalPath: "/tmp/x.mp4",
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
		ClipID: "yt_phase1a_007", VideoID: "v007", LocalPath: "/tmp/x.mp4",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if bundle != nil {
		t.Fatalf("expected (nil, nil) on full miss; got %+v", bundle)
	}
}

// ── MaterializePayloadTexts: all kinds, hashes populated ────────────────

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
		if r.Status != asset.TextTrackReady {
			t.Fatalf("Status for TextKind=%s = %q, want READY", r.TextKind, r.Status)
		}
	}
	// Verify text_kind coverage exactly matches the input.
	seen := map[asset.TextTrackKind]bool{}
	for _, r := range rows {
		seen[r.TextKind] = true
	}
	for _, k := range []asset.TextTrackKind{
		asset.TextTrackTranscript, asset.TextTrackTitle,
		asset.TextTrackSummary, asset.TextTrackDescription,
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

// ── Save: empty language → "und"; empty Source → error ───────────────────

func TestSave_DefaultsEmptyLanguageToUnd(t *testing.T) {
	repo := &stubRepo{}
	resolver := newTestResolver(repo, nil, nil)
	if err := resolver.Save(context.Background(), "yt_phase1a_009", "x", asset.TextSourceProvided, ""); err != nil {
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
	err := resolver.Save(context.Background(), "yt_phase1a_010", "x", asset.TextTrackSource(""), "en")
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
	rows := []asset.TextTrack{
		{AssetID: "c1", LanguageCode: "en", TextKind: asset.TextTrackTranscript, TextContent: "a", Status: asset.TextTrackReady, TextHash: "h1"},
		{AssetID: "c1", LanguageCode: "it", TextKind: asset.TextTrackTranscript, TextContent: "b", Status: asset.TextTrackReady, TextHash: "h2"},
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
