package localization

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
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
	// calls counts Compile invocations, so a rejection can be proven to happen
	// BEFORE any ASS is written (not merely that an error was returned).
	calls int
}

func (f *fakeSubtitleCompiler) Compile(_ context.Context, in SubtitleCompileInput) (*SubtitleAsset, error) {
	f.calls++
	f.got = in
	if f.err != nil {
		return nil, f.err
	}
	return f.asset, nil
}

// languageDerivedSubtitleCompiler derives the ASS identity from the compile
// input, so a test can prove each language produced its OWN artifact instead of
// N languages sharing (and overwriting) one.
type languageDerivedSubtitleCompiler struct {
	inputs []SubtitleCompileInput
}

func (c *languageDerivedSubtitleCompiler) Compile(_ context.Context, in SubtitleCompileInput) (*SubtitleAsset, error) {
	c.inputs = append(c.inputs, in)
	return &SubtitleAsset{
		LocalPath: "/tmp/subtitles/" + in.ClipID + "." + in.Language + ".ass",
		SHA256:    "ass-sha-" + in.Language,
		StyleHash: in.StyleHash,
		TrackID:   in.TrackID,
	}, nil
}

// matchingTrack returns a resolved track whose TextHash matches validPlan()'s
// SubtitleSHA256 ("subtitle-sha").
func matchingTrack() *ResolvedSubtitleTrack {
	return &ResolvedSubtitleTrack{
		TrackID:      202,
		LanguageCode: "es",
		Cues:         []detail.TimedCue{{StartMs: 0, EndMs: 1000, Text: "hola"}},
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

// TestSubtitleWire_RejectsWrongLanguageTrack pins the invariant behind
// "the subtitles are in the language of the script": a resolved track written
// in another language is refused, and it is refused BEFORE the ASS compiler
// runs — a wrong-language artifact must never be written, let alone burned.
//
// The text-hash check would reject this fixture too (the language is folded
// into the hash), so the fixture keeps the plan's hash EQUAL to the track's to
// prove the language pairing is checked on its own and not merely inherited
// from the digest.
func TestSubtitleWire_RejectsWrongLanguageTrack(t *testing.T) {
	track := matchingTrack()
	track.LanguageCode = "de" // the plan renders "es"
	compiler := &fakeSubtitleCompiler{asset: validSubtitleAsset()}
	w := newTestWire(t, &fakeSubtitleResolver{track: track}, compiler)

	_, err := w.Wire(context.Background(), validPlan())
	if err == nil {
		t.Fatal("Wire must refuse a subtitle track in a different language than the plan renders")
	}
	if !strings.Contains(err.Error(), "refusing to burn the wrong language") {
		t.Fatalf("error must name the wrong-language refusal, got %v", err)
	}
	if compiler.calls != 0 {
		t.Fatalf("the ASS compiler ran %d time(s) for a wrong-language track; no artifact may be written", compiler.calls)
	}
}

// TestSubtitleWire_AcceptsLanguageNeutralTrack verifies a track that carries no
// determined language (BCP-47 "und") is still renderable: it has no language to
// contradict the plan, and refusing it would break the legitimate
// undetermined-transcript path.
func TestSubtitleWire_AcceptsLanguageNeutralTrack(t *testing.T) {
	track := matchingTrack()
	track.LanguageCode = "und"
	compiler := &fakeSubtitleCompiler{asset: validSubtitleAsset()}
	w := newTestWire(t, &fakeSubtitleResolver{track: track}, compiler)

	if _, err := w.Wire(context.Background(), validPlan()); err != nil {
		t.Fatalf("Wire must accept a language-neutral (und) track: %v", err)
	}
	if compiler.calls != 1 {
		t.Fatalf("compiler calls = %d, want 1", compiler.calls)
	}
}

// TestSubtitleWire_AcceptsBCP47CaseVariant verifies the language pairing is
// compared through the canonical BCP-47 normalizer, not by raw string equality:
// "ES" and "es" are the same language and must not fail the gate.
func TestSubtitleWire_AcceptsBCP47CaseVariant(t *testing.T) {
	track := matchingTrack()
	track.LanguageCode = "ES"
	w := newTestWire(t, &fakeSubtitleResolver{track: track}, &fakeSubtitleCompiler{asset: validSubtitleAsset()})

	if _, err := w.Wire(context.Background(), validPlan()); err != nil {
		t.Fatalf("Wire must accept a case variant of the plan's language: %v", err)
	}
}

// TestSubtitleWire_EachLanguageBurnsItsOwnTrack is the multi-language subtitle
// certificate: the SAME clip rendered into several languages must resolve each
// language's own track and compile a distinct ASS artifact per language. A
// shared artifact path, or a compile input carrying the wrong language, would
// mean N languages overwriting each other's subtitles — the failure this gate
// exists to catch, reachable without a GPU.
func TestSubtitleWire_EachLanguageBurnsItsOwnTrack(t *testing.T) {
	languages := []string{"es", "de", "it", "pt-BR", "ru"}
	paths := make(map[string]struct{}, len(languages))

	for _, lang := range languages {
		plan := validPlan()
		plan.TargetLanguage = lang
		plan.SubtitleTrackID = 202
		plan.SubtitleSHA256 = "subtitle-sha-" + lang
		plan.Fingerprint = Fingerprint(plan)

		resolver := &fakeSubtitleResolver{track: &ResolvedSubtitleTrack{
			TrackID:      202,
			LanguageCode: lang,
			Cues:         []detail.TimedCue{{StartMs: 0, EndMs: 900, Text: "sub " + lang}},
			TextHash:     "subtitle-sha-" + lang,
		}}
		compiler := &languageDerivedSubtitleCompiler{}
		w := newTestWire(t, resolver, compiler)

		got, err := w.Wire(context.Background(), plan)
		if err != nil {
			t.Fatalf("language %s: Wire must succeed for its own track: %v", lang, err)
		}
		if len(compiler.inputs) != 1 {
			t.Fatalf("language %s: compile calls = %d, want 1", lang, len(compiler.inputs))
		}
		if compiler.inputs[0].Language != lang {
			t.Fatalf("language %s: compile input language = %q, want that language", lang, compiler.inputs[0].Language)
		}
		if !strings.HasSuffix(got.LocalPath, "."+lang+".ass") {
			t.Fatalf("language %s: artifact %q must be that language's ASS", lang, got.LocalPath)
		}
		paths[got.LocalPath] = struct{}{}
	}

	if len(paths) != len(languages) {
		t.Fatalf("distinct ASS artifacts = %d, want %d: two languages shared one path", len(paths), len(languages))
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
