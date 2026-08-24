package wiring

// localization_subtitle_test.go — adapters for the localization subtitle
// wiring: the TextTrack→cues resolver (fail-closed on status/hash) and the
// cues→.ass compiler (deterministic via texttracks.CompileASSContent).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/localization"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── fakes ───────────────────────────────────────────────────────────

type fakeTextTrackByIDReader struct {
	track *asset.TextTrack
	cues  []asset.TimedCue
	err   error
	gotID int64
}

func (f *fakeTextTrackByIDReader) FindByID(_ context.Context, trackID int64) (*asset.TextTrack, []asset.TimedCue, error) {
	f.gotID = trackID
	if f.err != nil {
		return nil, nil, f.err
	}
	return f.track, f.cues, nil
}

func readyTrack(id int64, lang, text string) *asset.TextTrack {
	return &asset.TextTrack{
		ID:           id,
		AssetID:      "asset-1",
		LanguageCode: lang,
		TextKind:     asset.TextTrackTranscript,
		TextContent:  text,
		TextHash:     asset.TextHash(text, lang, asset.TextTrackTranscript),
		Status:       asset.TextTrackReady,
	}
}

func twoCues() []asset.TimedCue {
	return []asset.TimedCue{
		{StartMs: 0, EndMs: 3000, Text: "hola"},
		{StartMs: 3000, EndMs: 6000, Text: "mundo"},
	}
}

// ── resolver ────────────────────────────────────────────────────────

func TestLocalizationSubtitleResolver_ResolvesReadyTrack(t *testing.T) {
	text := "hola mundo"
	trk := readyTrack(202, "es", text)
	r := newLocalizationSubtitleResolver(&fakeTextTrackByIDReader{track: trk, cues: twoCues()})

	got, err := r.ResolveSubtitleTrack(context.Background(), 202, trk.TextHash)
	if err != nil {
		t.Fatalf("ResolveSubtitleTrack: %v", err)
	}
	if got.TrackID != 202 || got.LanguageCode != "es" || len(got.Cues) != 2 || got.TextHash != trk.TextHash {
		t.Fatalf("resolved track = %+v", got)
	}
}

func TestLocalizationSubtitleResolver_RecomputesEmptyHash(t *testing.T) {
	trk := readyTrack(202, "es", "hola mundo")
	trk.TextHash = "" // legacy row: no persisted hash
	r := newLocalizationSubtitleResolver(&fakeTextTrackByIDReader{track: trk, cues: twoCues()})

	got, err := r.ResolveSubtitleTrack(context.Background(), 202, asset.TextHash(trk.TextContent, "es", asset.TextTrackTranscript))
	if err != nil {
		t.Fatalf("ResolveSubtitleTrack: %v", err)
	}
	if got.TextHash != asset.TextHash(trk.TextContent, "es", asset.TextTrackTranscript) {
		t.Fatalf("recomputed hash = %q", got.TextHash)
	}
}

func TestLocalizationSubtitleResolver_RejectsHashMismatch(t *testing.T) {
	trk := readyTrack(202, "es", "hola mundo")
	r := newLocalizationSubtitleResolver(&fakeTextTrackByIDReader{track: trk, cues: twoCues()})

	if _, err := r.ResolveSubtitleTrack(context.Background(), 202, "wrong-hash"); err == nil {
		t.Fatal("must reject a text-hash mismatch")
	}
}

func TestLocalizationSubtitleResolver_RejectsNonReady(t *testing.T) {
	trk := readyTrack(202, "es", "hola mundo")
	trk.Status = asset.TextTrackPending
	r := newLocalizationSubtitleResolver(&fakeTextTrackByIDReader{track: trk, cues: twoCues()})

	if _, err := r.ResolveSubtitleTrack(context.Background(), 202, trk.TextHash); err == nil {
		t.Fatal("must reject a non-READY track")
	}
}

func TestLocalizationSubtitleResolver_RejectsNotFound(t *testing.T) {
	r := newLocalizationSubtitleResolver(&fakeTextTrackByIDReader{track: nil, cues: nil})

	if _, err := r.ResolveSubtitleTrack(context.Background(), 404, "sha"); err == nil {
		t.Fatal("must reject a not-found track")
	}
}

func TestLocalizationSubtitleResolver_RejectsEmptyCues(t *testing.T) {
	trk := readyTrack(202, "es", "hola mundo")
	r := newLocalizationSubtitleResolver(&fakeTextTrackByIDReader{track: trk, cues: nil})

	if _, err := r.ResolveSubtitleTrack(context.Background(), 202, trk.TextHash); err == nil {
		t.Fatal("must reject a track without timed cues")
	}
}

func TestLocalizationSubtitleResolver_PropagatesRepoError(t *testing.T) {
	r := newLocalizationSubtitleResolver(&fakeTextTrackByIDReader{err: errors.New("db down")})

	if _, err := r.ResolveSubtitleTrack(context.Background(), 202, "sha"); err == nil {
		t.Fatal("must propagate a repo error")
	}
}

func TestLocalizationSubtitleResolver_NilReaderFailsClosed(t *testing.T) {
	r := newLocalizationSubtitleResolver(nil)
	if _, err := r.ResolveSubtitleTrack(context.Background(), 202, "sha"); err == nil {
		t.Fatal("must fail closed on an unwired resolver")
	}
}

// ── compiler ────────────────────────────────────────────────────────

func localizationCompileInput(t *testing.T) localization.SubtitleCompileInput {
	t.Helper()
	return localization.SubtitleCompileInput{
		ClipID:         "clip-1",
		Language:       "es",
		StyleHash:      "shorts-v1",
		TrackID:        202,
		Cues:           twoCues(),
		ClipDurationMS: 6500,
		OutputDir:      t.TempDir(),
	}
}

func TestLocalizationSubtitleCompiler_CompilesDeterministicASS(t *testing.T) {
	c := newLocalizationSubtitleCompiler()
	in := localizationCompileInput(t)

	out, err := c.Compile(context.Background(), in)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if filepath.Base(out.LocalPath) != "clip-1.es.ass" {
		t.Fatalf("filename = %q, want clip-1.es.ass", out.LocalPath)
	}
	if out.StyleHash != "shorts-v1" || out.TrackID != 202 {
		t.Fatalf("artifact = %+v", out)
	}
	content, err := os.ReadFile(out.LocalPath)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	if !strings.Contains(string(content), "Style: shorts-v1,") {
		t.Fatalf("missing style line:\n%s", content)
	}
	sum := sha256.Sum256(content)
	if out.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("sha mismatch: artifact %s != file %x", out.SHA256, sum)
	}
	if err := texttracks.ValidateASSFile(out.LocalPath, 6500); err != nil {
		t.Fatalf("ValidateASSFile: %v", err)
	}

	// Determinism: same input → identical bytes.
	out2, err := c.Compile(context.Background(), localizationCompileInput(t))
	if err != nil {
		t.Fatalf("Compile (2nd): %v", err)
	}
	if out2.SHA256 != out.SHA256 {
		t.Fatalf("determinism violated: %s vs %s", out.SHA256, out2.SHA256)
	}
}

func TestLocalizationSubtitleCompiler_EmptyCuesFailsClosed(t *testing.T) {
	c := newLocalizationSubtitleCompiler()
	in := localizationCompileInput(t)
	in.Cues = nil
	if _, err := c.Compile(context.Background(), in); err == nil {
		t.Fatal("must fail closed on zero cues")
	}
}

func TestLocalizationSubtitleCompiler_DurationViolationClipsToMediaBoundary(t *testing.T) {
	c := newLocalizationSubtitleCompiler()
	in := localizationCompileInput(t)
	in.ClipDurationMS = 4000
	out, err := c.Compile(context.Background(), in)
	if err != nil {
		t.Fatalf("Compile must trim cues to the media boundary: %v", err)
	}
	if err := texttracks.ValidateASSFile(out.LocalPath, 4000); err != nil {
		t.Fatalf("trimmed ASS must validate against the clip duration: %v", err)
	}
}

func TestLocalizationSubtitleCompiler_DefaultLanguageUnd(t *testing.T) {
	c := newLocalizationSubtitleCompiler()
	in := localizationCompileInput(t)
	in.Language = ""
	out, err := c.Compile(context.Background(), in)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if filepath.Base(out.LocalPath) != "clip-1.und.ass" {
		t.Fatalf("filename = %q, want clip-1.und.ass", out.LocalPath)
	}
}
