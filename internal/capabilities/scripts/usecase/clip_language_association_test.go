// Package usecase — clip_language_association_test.go
//
// Pins the script-language ↔ clip-association check and the runtime
// create-and-persist remediation:
//
//  1. The pure classifier distinguishes ready / missing / mismatch.
//  2. With NO ensurer wired the resolver fails closed with the typed
//     *ClipLanguageAssociationError (which still unwraps to
//     *ErrTextTrackNotReady, so the historical contract is preserved).
//  3. With an ensurer wired a missing track is CREATED AND PERSISTED at
//     runtime and the run continues with the freshly materialized transcript.
//  4. A materialization that does not actually produce a READY track is a hard
//     failure (Status=materialize_failed), never a silent pass.
package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"go.uber.org/zap"
)

// ── classifier ────────────────────────────────────────────────────────────

func TestClassifyClipLanguageAssociation(t *testing.T) {
	cases := []struct {
		name      string
		requested string
		available []string
		want      ClipLanguageAssociationStatus
	}{
		{"exact match", "en", []string{"en", "es"}, ClipLanguageReady},
		{"case and whitespace insensitive", " EN ", []string{"en"}, ClipLanguageReady},
		{"no tracks at all", "en", nil, ClipLanguageMissing},
		{"empty available slice", "en", []string{}, ClipLanguageMissing},
		{"other languages only", "en", []string{"es", "fr"}, ClipLanguageMismatch},
		{"unspecified request is never a mismatch", "", []string{"es"}, ClipLanguageReady},
		{"whitespace-only request", "  ", nil, ClipLanguageReady},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyClipLanguageAssociation(tc.requested, tc.available); got != tc.want {
				t.Fatalf("ClassifyClipLanguageAssociation(%q, %v) = %q, want %q",
					tc.requested, tc.available, got, tc.want)
			}
		})
	}
}

// ── stubs ─────────────────────────────────────────────────────────────────

// mutableTextTrackReader is a TextTrackReader whose READY set can be extended
// at runtime (which is exactly what an ensurer does). It also records the
// language lookups so a test can prove the resolver re-read after the ensure.
type mutableTextTrackReader struct {
	tracks map[string]map[string]string // assetID -> language -> text
}

func newMutableTextTrackReader() *mutableTextTrackReader {
	return &mutableTextTrackReader{tracks: map[string]map[string]string{}}
}

func (r *mutableTextTrackReader) put(assetID, language, text string) {
	if r.tracks[assetID] == nil {
		r.tracks[assetID] = map[string]string{}
	}
	r.tracks[assetID][language] = text
}

func (r *mutableTextTrackReader) FindReady(_ context.Context, assetID, languageCode string, kind detail.TextTrackKind) (*detail.TextTrack, []detail.TimedCue, error) {
	if kind != detail.TextTrackTranscript {
		return nil, nil, nil
	}
	langs := r.tracks[assetID]
	if langs == nil {
		return nil, nil, nil
	}
	text, ok := langs[languageCode]
	if !ok {
		return nil, nil, nil
	}
	return &detail.TextTrack{
		AssetID:       assetID,
		LanguageCode:  languageCode,
		TextKind:      detail.TextTrackTranscript,
		TextContent:   text,
		TextHash:      "hash-" + assetID + "-" + languageCode,
		SourceVersion: "v1",
		Status:        detail.TextTrackReady,
	}, nil, nil
}

func (r *mutableTextTrackReader) ListReadyLanguages(_ context.Context, assetID string, kind detail.TextTrackKind) ([]string, error) {
	if kind != detail.TextTrackTranscript {
		return []string{}, nil
	}
	out := []string{}
	for lang := range r.tracks[assetID] {
		out = append(out, lang)
	}
	return out, nil
}

// recordingEnsurer is a ClipTextTrackEnsurer stub. It records calls and either
// persists the requested track into the reader (success) or returns a failure.
type recordingEnsurer struct {
	reader *mutableTextTrackReader
	// text persisted on success.
	text string
	// err, when set, is returned WITHOUT persisting anything.
	err error
	// persistButStayMissing simulates a materializer that reports success but
	// leaves no READY track (must still fail closed).
	persistButStayMissing bool

	calls []ensurerCall
}

type ensurerCall struct {
	assetID  string
	language string
	kind     detail.TextTrackKind
}

func (e *recordingEnsurer) EnsureReadyTextTrack(_ context.Context, assetID, languageCode string, kind detail.TextTrackKind) error {
	e.calls = append(e.calls, ensurerCall{assetID: assetID, language: languageCode, kind: kind})
	if e.err != nil {
		return e.err
	}
	if e.persistButStayMissing {
		return nil
	}
	text := e.text
	if text == "" {
		text = "materialized at runtime"
	}
	e.reader.put(assetID, languageCode, text)
	return nil
}

// ── no ensurer wired: fail closed with the typed association error ────────

func TestResolveTranscriptChecked_NoEnsurer_MissingIsTyped(t *testing.T) {
	reader := newMutableTextTrackReader()
	b := &ClipSourceBuilder{textTrackReader: reader, log: zap.NewNop()}

	_, _, err := b.resolveTranscriptChecked(context.Background(), "clip-x", "en", nil)
	if err == nil {
		t.Fatal("a clip with no READY track must fail closed")
	}
	var assoc *ClipLanguageAssociationError
	if !errors.As(err, &assoc) {
		t.Fatalf("error must be errors.As-probeable as *ClipLanguageAssociationError; got %T %v", err, err)
	}
	if assoc.Status != ClipLanguageMissing {
		t.Fatalf("Status = %q, want %q", assoc.Status, ClipLanguageMissing)
	}
	if assoc.AssetID != "clip-x" || assoc.RequestedLanguage != "en" {
		t.Fatalf("carry data = %+v", assoc)
	}
	// Historical contract preserved: the wrapped not-READY error is reachable.
	var notReady *ErrTextTrackNotReady
	if !errors.As(err, &notReady) {
		t.Fatalf("error must still unwrap to *ErrTextTrackNotReady; got %T", err)
	}
	if !errors.Is(err, &ErrTextTrackNotReady{}) {
		t.Fatal("errors.Is(err, &ErrTextTrackNotReady{}) must succeed through the chain")
	}
	if !errors.Is(err, &ClipLanguageAssociationError{}) {
		t.Fatal("errors.Is(err, &ClipLanguageAssociationError{}) must succeed")
	}
}

func TestResolveTranscriptChecked_NoEnsurer_MismatchNamesAvailableLanguages(t *testing.T) {
	reader := newMutableTextTrackReader()
	reader.put("clip-y", "es", "texto en espanol")
	reader.put("clip-y", "fr", "texte en francais")
	b := &ClipSourceBuilder{textTrackReader: reader, log: zap.NewNop()}

	_, _, err := b.resolveTranscriptChecked(context.Background(), "clip-y", "en", nil)
	if err == nil {
		t.Fatal("a clip bound to the wrong language must fail closed")
	}
	var assoc *ClipLanguageAssociationError
	if !errors.As(err, &assoc) {
		t.Fatalf("error must be errors.As-probeable as *ClipLanguageAssociationError; got %T %v", err, err)
	}
	if assoc.Status != ClipLanguageMismatch {
		t.Fatalf("Status = %q, want %q", assoc.Status, ClipLanguageMismatch)
	}
	if len(assoc.AvailableLanguages) != 2 {
		t.Fatalf("AvailableLanguages = %v, want the two READY languages", assoc.AvailableLanguages)
	}
}

// ── ensurer wired: create and persist at runtime ──────────────────────────

func TestResolveTranscriptChecked_EnsurerCreatesAndPersistsMissingTrack(t *testing.T) {
	reader := newMutableTextTrackReader()
	ensurer := &recordingEnsurer{reader: reader, text: "transcript created at runtime"}
	b := &ClipSourceBuilder{textTrackReader: reader, transcriptEnsurer: ensurer, log: zap.NewNop()}

	transcript, track, err := b.resolveTranscriptChecked(context.Background(), "clip-z", "en", nil)
	if err != nil {
		t.Fatalf("the runtime materialization must let the run continue: %v", err)
	}
	if transcript != "transcript created at runtime" {
		t.Fatalf("transcript = %q", transcript)
	}
	if track == nil || track.Status != detail.TextTrackReady {
		t.Fatalf("resolved track = %+v", track)
	}
	if len(ensurer.calls) != 1 {
		t.Fatalf("ensurer calls = %d, want exactly 1", len(ensurer.calls))
	}
	call := ensurer.calls[0]
	if call.assetID != "clip-z" || call.language != "en" || call.kind != detail.TextTrackTranscript {
		t.Fatalf("ensurer called with %+v", call)
	}
	// The track really was persisted through the reader (create AND save).
	if _, ok := reader.tracks["clip-z"]["en"]; !ok {
		t.Fatal("the ensurer did not persist the track")
	}
}

func TestResolveTranscriptChecked_ReadyTrackNeverMaterializes(t *testing.T) {
	reader := newMutableTextTrackReader()
	reader.put("clip-ready", "en", "already there")
	ensurer := &recordingEnsurer{reader: reader}
	b := &ClipSourceBuilder{textTrackReader: reader, transcriptEnsurer: ensurer, log: zap.NewNop()}

	transcript, _, err := b.resolveTranscriptChecked(context.Background(), "clip-ready", "en", nil)
	if err != nil {
		t.Fatalf("ready track must not fail: %v", err)
	}
	if transcript != "already there" {
		t.Fatalf("transcript = %q", transcript)
	}
	if len(ensurer.calls) != 0 {
		t.Fatalf("a READY track must never trigger materialization; got %d calls", len(ensurer.calls))
	}
}

func TestResolveTranscriptChecked_EnsureFailureFailsClosed(t *testing.T) {
	reader := newMutableTextTrackReader()
	ensurer := &recordingEnsurer{reader: reader, err: errors.New("translation provider unavailable")}
	b := &ClipSourceBuilder{textTrackReader: reader, transcriptEnsurer: ensurer, log: zap.NewNop()}

	_, _, err := b.resolveTranscriptChecked(context.Background(), "clip-f", "en", nil)
	if err == nil {
		t.Fatal("a failed materialization must fail closed")
	}
	var assoc *ClipLanguageAssociationError
	if !errors.As(err, &assoc) {
		t.Fatalf("error must be errors.As-probeable as *ClipLanguageAssociationError; got %T %v", err, err)
	}
	if assoc.Status != ClipLanguageMaterializeFailed {
		t.Fatalf("Status = %q, want %q", assoc.Status, ClipLanguageMaterializeFailed)
	}
	if assoc.EnsureError == nil {
		t.Fatal("EnsureError must carry the materialization failure")
	}
	var notReady *ErrTextTrackNotReady
	if !errors.As(err, &notReady) {
		t.Fatal("the typed error must still unwrap to *ErrTextTrackNotReady")
	}
}

func TestResolveTranscriptChecked_EnsureWithoutTrackStillFailsClosed(t *testing.T) {
	reader := newMutableTextTrackReader()
	ensurer := &recordingEnsurer{reader: reader, persistButStayMissing: true}
	b := &ClipSourceBuilder{textTrackReader: reader, transcriptEnsurer: ensurer, log: zap.NewNop()}

	_, _, err := b.resolveTranscriptChecked(context.Background(), "clip-n", "en", nil)
	if err == nil {
		t.Fatal("an ensurer that reports success without a READY track must still fail closed")
	}
	var assoc *ClipLanguageAssociationError
	if !errors.As(err, &assoc) {
		t.Fatalf("error must be errors.As-probeable as *ClipLanguageAssociationError; got %T %v", err, err)
	}
	if assoc.Status != ClipLanguageMaterializeFailed {
		t.Fatalf("Status = %q, want %q", assoc.Status, ClipLanguageMaterializeFailed)
	}
}

// ── end-to-end through BuildClipContext ───────────────────────────────────

// TestBuildClipContext_MaterializesMissingSubsAtRuntime pins the whole
// contract at the surface every caller actually uses: a clip whose transcript
// is not associated with the script language is materialized at runtime and
// the resulting evidence carries the freshly created transcript.
func TestBuildClipContext_MaterializesMissingSubsAtRuntime(t *testing.T) {
	const clipID = "clip-runtime-subs"
	clip := makeTestClip(clipID, "Runtime subs", time.Second)
	resolver := &fase4StubResolver{byID: map[string]*asset.Asset{clipID: clip}}

	reader := newMutableTextTrackReader()
	ensurer := &recordingEnsurer{reader: reader, text: "TEXT-CREATED-AT-RUNTIME"}
	b := NewClipSourceBuilder(resolver, nil, zap.NewNop())
	b.ConfigureTextTrackReader(reader)
	b.ConfigureTextTrackEnsurer(ensurer)

	evidence, _, sourceText, err := b.BuildClipContext(context.Background(), []string{clipID}, &ClipGenerationOptions{
		Language: "en", TranscriptPolicy: "strict",
	})
	if err != nil {
		t.Fatalf("BuildClipContext must materialize the missing transcript and continue: %v", err)
	}
	if evidence == nil {
		t.Fatal("evidence is nil")
	}
	if len(ensurer.calls) != 1 {
		t.Fatalf("ensurer calls = %d, want 1", len(ensurer.calls))
	}
	if evidence.ClipDetails[clipID].Transcript != "TEXT-CREATED-AT-RUNTIME" {
		t.Fatalf("materialized transcript did not reach the clip detail: %+v", evidence.ClipDetails[clipID])
	}
	if sourceText == "" {
		t.Fatal("source text must carry the materialized transcript")
	}
}

// TestBuildClipContext_NoEnsurerStillFailsClosed pins that the remediation is
// strictly additive: without an ensurer the strict transcript policy still
// rejects the clip (no regression on the pre-existing contract).
func TestBuildClipContext_NoEnsurerStillFailsClosed(t *testing.T) {
	const clipID = "clip-no-ensurer"
	clip := makeTestClip(clipID, "No ensurer", time.Second)
	resolver := &fase4StubResolver{byID: map[string]*asset.Asset{clipID: clip}}

	b := NewClipSourceBuilder(resolver, nil, zap.NewNop())
	b.ConfigureTextTrackReader(newMutableTextTrackReader())

	_, _, _, err := b.BuildClipContext(context.Background(), []string{clipID}, &ClipGenerationOptions{
		Language: "en", TranscriptPolicy: "strict",
	})
	if err == nil {
		t.Fatal("without an ensurer the strict policy must still reject a clip with no transcript")
	}
	var assoc *ClipLanguageAssociationError
	if !errors.As(err, &assoc) {
		t.Fatalf("error must be errors.As-probeable as *ClipLanguageAssociationError; got %T %v", err, err)
	}
}
