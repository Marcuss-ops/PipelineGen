package localization

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// fakeSubtitleResolver returns a fixed track (or error), recording the
// requested (trackID, expectedSHA256).
type fakeSubtitleResolver struct {
	track  *ResolvedSubtitleTrack
	err    error
	gotID  int64
	gotSHA string
}

func (f *fakeSubtitleResolver) ResolveSubtitleTrack(_ context.Context, trackID int64, expectedSHA256 string) (*ResolvedSubtitleTrack, error) {
	f.gotID = trackID
	f.gotSHA = expectedSHA256
	if f.err != nil {
		return nil, f.err
	}
	return f.track, nil
}

// fakeSubtitleCompiler returns a fixed ASS asset (or error), recording the
// compile input.
type fakeSubtitleCompiler struct {
	asset *SubtitleAsset
	err   error
	got   SubtitleCompileInput
}

func (f *fakeSubtitleCompiler) Compile(_ context.Context, in SubtitleCompileInput) (*SubtitleAsset, error) {
	f.got = in
	if f.err != nil {
		return nil, f.err
	}
	return f.asset, nil
}

// matchingTrack returns a resolved track whose TextHash matches validPlan()'s
// SubtitleSHA256 ("subtitle-sha").
func matchingTrack() *ResolvedSubtitleTrack {
	return &ResolvedSubtitleTrack{
		TrackID:      202,
		LanguageCode: "es",
		Cues:         []asset.TimedCue{{StartMs: 0, EndMs: 1000, Text: "hola"}},
		TextHash:     "subtitle-sha",
	}
}

func validSubtitleAsset() *SubtitleAsset {
	return &SubtitleAsset{LocalPath: "/tmp/subtitles/clip-1.es.ass", SHA256: "ass-sha", StyleHash: "style-sha", TrackID: 202}
}

func newTestWire(t *testing.T, resolver SubtitleResolver, compiler SubtitleArtifactCompiler) *SubtitleWire {
	t.Helper()
	w, err := NewSubtitleWire(resolver, compiler, "/tmp/subtitles")
	if err != nil {
		t.Fatalf("NewSubtitleWire: %v", err)
	}
	return w
}

// TestSubtitleWire_WiresTrackToASS verifies the wire resolves the plan's
// track reference and compiles it into the ASS artifact, passing the plan's
// style/duration/language through.
func TestSubtitleWire_WiresTrackToASS(t *testing.T) {
	resolver := &fakeSubtitleResolver{track: matchingTrack()}
	compiler := &fakeSubtitleCompiler{asset: validSubtitleAsset()}
	w := newTestWire(t, resolver, compiler)

	plan := validPlan()
	got, err := w.Wire(context.Background(), plan)
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	if got == nil || *got != *validSubtitleAsset() {
		t.Fatalf("Wire: got %+v, want %+v", got, validSubtitleAsset())
	}
	if resolver.gotID != plan.SubtitleTrackID || resolver.gotSHA != plan.SubtitleSHA256 {
		t.Errorf("resolver got (%d, %q), want (%d, %q)", resolver.gotID, resolver.gotSHA, plan.SubtitleTrackID, plan.SubtitleSHA256)
	}
	in := compiler.got
	if in.TrackID != plan.SubtitleTrackID || in.StyleHash != plan.SubtitleStyleHash || in.Language != plan.TargetLanguage || in.ClipDurationMS != plan.DurationMS || len(in.Cues) != 1 {
		t.Errorf("compiler input: got %+v, want plan-derived fields", in)
	}
	if in.OutputDir != "/tmp/subtitles" {
		t.Errorf("compiler OutputDir: got %q, want %q", in.OutputDir, "/tmp/subtitles")
	}
}

// TestSubtitleWire_RejectsHashMismatch verifies a track whose text hash does
// not match the plan's SubtitleSHA256 is rejected (no wrong-language burn).
func TestSubtitleWire_RejectsHashMismatch(t *testing.T) {
	track := matchingTrack()
	track.TextHash = "different-hash"
	w := newTestWire(t, &fakeSubtitleResolver{track: track}, &fakeSubtitleCompiler{})

	if _, err := w.Wire(context.Background(), validPlan()); err == nil {
		t.Fatal("Wire must reject a subtitle track text-hash mismatch")
	}
}

// TestSubtitleWire_RejectsTrackNotFound verifies a nil track is a typed
// failure, never a silent empty ASS.
func TestSubtitleWire_RejectsTrackNotFound(t *testing.T) {
	w := newTestWire(t, &fakeSubtitleResolver{track: nil}, &fakeSubtitleCompiler{})

	if _, err := w.Wire(context.Background(), validPlan()); err == nil {
		t.Fatal("Wire must reject a not-found subtitle track")
	}
}

// TestSubtitleWire_RejectsEmptyCues verifies a track without timed cues never
// reaches the compiler.
func TestSubtitleWire_RejectsEmptyCues(t *testing.T) {
	track := matchingTrack()
	track.Cues = nil
	w := newTestWire(t, &fakeSubtitleResolver{track: track}, &fakeSubtitleCompiler{})

	if _, err := w.Wire(context.Background(), validPlan()); err == nil {
		t.Fatal("Wire must reject a subtitle track with no timed cues")
	}
}

// TestSubtitleWire_RejectsInvalidPlan verifies the plan is validated before
// any resolution.
func TestSubtitleWire_RejectsInvalidPlan(t *testing.T) {
	w := newTestWire(t, &fakeSubtitleResolver{track: matchingTrack()}, &fakeSubtitleCompiler{asset: validSubtitleAsset()})

	plan := validPlan()
	plan.Fingerprint = "bogus"
	if _, err := w.Wire(context.Background(), plan); err == nil {
		t.Fatal("Wire must reject an invalid plan")
	}
}

// TestSubtitleWire_PropagatesResolverAndCompilerErrors verifies resolver and
// compiler failures are surfaced, not swallowed.
func TestSubtitleWire_PropagatesResolverAndCompilerErrors(t *testing.T) {
	w := newTestWire(t, &fakeSubtitleResolver{err: errors.New("db down")}, &fakeSubtitleCompiler{})
	if _, err := w.Wire(context.Background(), validPlan()); err == nil {
		t.Fatal("Wire must propagate a resolver error")
	}

	w2 := newTestWire(t, &fakeSubtitleResolver{track: matchingTrack()}, &fakeSubtitleCompiler{err: errors.New("ass write failed")})
	if _, err := w2.Wire(context.Background(), validPlan()); err == nil {
		t.Fatal("Wire must propagate a compiler error")
	}
}

// TestSubtitleWire_RejectsIncompleteAsset verifies a compiler returning an
// empty/incomplete ASS artifact is a typed failure.
func TestSubtitleWire_RejectsIncompleteAsset(t *testing.T) {
	w := newTestWire(t, &fakeSubtitleResolver{track: matchingTrack()}, &fakeSubtitleCompiler{asset: &SubtitleAsset{LocalPath: "/tmp/x.ass"}})

	if _, err := w.Wire(context.Background(), validPlan()); err == nil {
		t.Fatal("Wire must reject an incomplete ASS artifact")
	}
}

// TestSubtitleWire_NilDepsFailConstruction verifies the wire cannot be built
// without both ports.
func TestSubtitleWire_NilDepsFailConstruction(t *testing.T) {
	if _, err := NewSubtitleWire(nil, &fakeSubtitleCompiler{}, ""); err == nil {
		t.Fatal("NewSubtitleWire must reject a nil resolver")
	}
	if _, err := NewSubtitleWire(&fakeSubtitleResolver{}, nil, ""); err == nil {
		t.Fatal("NewSubtitleWire must reject a nil compiler")
	}
}
