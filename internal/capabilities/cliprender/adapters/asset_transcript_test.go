package adapters

import (
	"context"
	"errors"
	"fmt"
	"testing"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"go.uber.org/zap"
)

type fakeAssetTranscriptTrackReader struct {
	track    *detail.TextTrack
	cues     []detail.TimedCue
	all      []detail.TextTrack
	err      error
	findLang string
	findCall int
}

func (f *fakeAssetTranscriptTrackReader) FindReady(_ context.Context, _ string, languageCode string, _ detail.TextTrackKind) (*detail.TextTrack, []detail.TimedCue, error) {
	f.findCall++
	f.findLang = languageCode
	return f.track, f.cues, f.err
}

func (f *fakeAssetTranscriptTrackReader) ListByAsset(_ context.Context, _ string) ([]detail.TextTrack, error) {
	return f.all, f.err
}

type fakeAssetTranscriptAssets struct {
	ref   *cliprender.AssetRef
	err   error
	calls int
}

func (f *fakeAssetTranscriptAssets) ResolveAsset(_ context.Context, _ string) (*cliprender.AssetRef, error) {
	f.calls++
	return f.ref, f.err
}

type fakeAssetTranscriptMaterializer struct {
	asset *cliprender.MaterializedAsset
	err   error
	calls int
}

func (f *fakeAssetTranscriptMaterializer) Materialize(_ context.Context, _ cliprender.AssetRef) (*cliprender.MaterializedAsset, error) {
	f.calls++
	return f.asset, f.err
}

type fakeAssetTranscriptGenerator struct {
	result *cliprender.TranscriptResult
	err    error
	input  cliprender.TranscriptInput
	source *cliprender.MaterializedAsset
}

func (f *fakeAssetTranscriptGenerator) Lookup(context.Context, cliprender.TranscriptInput) (*cliprender.TranscriptResult, bool, error) {
	return nil, false, nil
}

func (f *fakeAssetTranscriptGenerator) Generate(_ context.Context, in cliprender.TranscriptInput, source *cliprender.MaterializedAsset) (*cliprender.TranscriptResult, error) {
	f.input = in
	f.source = source
	return f.result, f.err
}

func TestAssetTranscriptResolver_ReusesReadyTrack(t *testing.T) {
	tracks := &fakeAssetTranscriptTrackReader{
		track: &detail.TextTrack{
			LanguageCode:       "it",
			SourceLanguageCode: "it",
			TextKind:           detail.TextTrackTranscript,
			TextContent:        "Ciao dal text track",
			Status:             detail.TextTrackReady,
			IsOriginal:         true,
			SourceType:         detail.TextSourceWhisper,
			Provider:           "whisper",
		},
		cues: []detail.TimedCue{{StartMs: 0, EndMs: 1000, Text: "Ciao"}},
	}
	r, err := NewAssetTranscriptResolver(nil, nil, nil, tracks, zap.NewNop())
	if err != nil {
		t.Fatalf("NewAssetTranscriptResolver: %v", err)
	}

	bundle, found, err := r.FetchAssetTranscript(context.Background(), "asset-1", "it", 0, 0)
	if err != nil || !found {
		t.Fatalf("FetchAssetTranscript = (%v, %v), want found, nil", found, err)
	}
	if bundle.PlainText != "Ciao dal text track" || len(bundle.Cues) != 1 {
		t.Fatalf("bundle = %+v", bundle)
	}
	if tracks.findLang != "it" {
		t.Fatalf("FindReady language = %q, want it", tracks.findLang)
	}
}

func TestAssetTranscriptResolver_PrefersOriginalLanguage(t *testing.T) {
	it := detail.TextTrack{
		LanguageCode: "it", TextKind: detail.TextTrackTranscript, Status: detail.TextTrackReady,
		TextContent: "originale", IsOriginal: true,
	}
	en := detail.TextTrack{
		LanguageCode: "en", TextKind: detail.TextTrackTranscript, Status: detail.TextTrackReady,
		TextContent: "translation", IsOriginal: false,
	}
	tracks := &fakeAssetTranscriptTrackReader{track: &it, all: []detail.TextTrack{en, it}}
	r, err := NewAssetTranscriptResolver(nil, nil, nil, tracks, zap.NewNop())
	if err != nil {
		t.Fatalf("NewAssetTranscriptResolver: %v", err)
	}

	if _, found, err := r.FetchAssetTranscript(context.Background(), "asset-1", "", 0, 0); err != nil || !found {
		t.Fatalf("FetchAssetTranscript = (%v, %v)", found, err)
	}
	if tracks.findLang != "it" {
		t.Fatalf("FindReady language = %q, want the original it", tracks.findLang)
	}
}

func TestAssetTranscriptResolver_GeneratesWhenNoTrack(t *testing.T) {
	tracks := &fakeAssetTranscriptTrackReader{}
	assets := &fakeAssetTranscriptAssets{ref: &cliprender.AssetRef{AssetID: "asset-1", MediaType: "video"}}
	materializer := &fakeAssetTranscriptMaterializer{asset: &cliprender.MaterializedAsset{
		AssetID: "asset-1", LocalPath: "/tmp/asset-1.mp4", SHA256: "sha-source",
	}}
	generator := &fakeAssetTranscriptGenerator{result: &cliprender.TranscriptResult{
		Language:         "it",
		DetectedLanguage: "it",
		Text:             "testo ASR",
		Cues:             []cliprender.Cue{{StartMs: 0, EndMs: 900, Text: "testo ASR"}},
		StreamSourceType: "whisper",
	}}
	r, err := NewAssetTranscriptResolver(assets, materializer, generator, tracks, zap.NewNop())
	if err != nil {
		t.Fatalf("NewAssetTranscriptResolver: %v", err)
	}

	bundle, found, err := r.FetchAssetTranscript(context.Background(), "asset-1", "it", 0, 0)
	if err != nil || !found {
		t.Fatalf("FetchAssetTranscript = (%v, %v), want found, nil", found, err)
	}
	if assets.calls != 1 || materializer.calls != 1 {
		t.Fatalf("assets calls=%d materializer calls=%d", assets.calls, materializer.calls)
	}
	if !generator.input.Persist || generator.input.Language != "it" || generator.input.SourceSHA256 != "sha-source" {
		t.Fatalf("Generate input = %+v", generator.input)
	}
	if generator.source == nil || generator.source.LocalPath != "/tmp/asset-1.mp4" {
		t.Fatalf("Generate source = %+v", generator.source)
	}
	if bundle.Provider != "whisper" || bundle.SourceType != detail.TextSourceWhisper || !bundle.IsOriginal {
		t.Fatalf("bundle provenance = %+v", bundle)
	}
}

func TestAssetTranscriptResolver_NoSpeechIsNotFound(t *testing.T) {
	generator := &fakeAssetTranscriptGenerator{err: fmt.Errorf("%w: empty transcript", cliprender.ErrNoSpeech)}
	r, err := NewAssetTranscriptResolver(
		&fakeAssetTranscriptAssets{ref: &cliprender.AssetRef{AssetID: "asset-1"}},
		&fakeAssetTranscriptMaterializer{asset: &cliprender.MaterializedAsset{AssetID: "asset-1", LocalPath: "/tmp/a.mp4"}},
		generator,
		&fakeAssetTranscriptTrackReader{},
		zap.NewNop(),
	)
	if err != nil {
		t.Fatalf("NewAssetTranscriptResolver: %v", err)
	}

	bundle, found, err := r.FetchAssetTranscript(context.Background(), "asset-1", "it", 0, 0)
	if err != nil {
		t.Fatalf("FetchAssetTranscript error = %v, want nil (no speech is not a failure)", err)
	}
	if found || bundle != nil {
		t.Fatalf("FetchAssetTranscript = (%v, %v), want not found", bundle, found)
	}
}

func TestAssetTranscriptResolver_SlicesRequestedWindow(t *testing.T) {
	tracks := &fakeAssetTranscriptTrackReader{
		track: &detail.TextTrack{
			LanguageCode: "it", TextKind: detail.TextTrackTranscript, Status: detail.TextTrackReady,
			TextContent: "a\nb\nc", IsOriginal: true,
		},
		cues: []detail.TimedCue{
			{StartMs: 0, EndMs: 1000, Text: "a"},
			{StartMs: 1000, EndMs: 2000, Text: "b"},
			{StartMs: 2000, EndMs: 3000, Text: "c"},
		},
	}
	r, err := NewAssetTranscriptResolver(nil, nil, nil, tracks, zap.NewNop())
	if err != nil {
		t.Fatalf("NewAssetTranscriptResolver: %v", err)
	}

	bundle, found, err := r.FetchAssetTranscript(context.Background(), "asset-1", "it", 1, 2)
	if err != nil || !found {
		t.Fatalf("FetchAssetTranscript = (%v, %v)", found, err)
	}
	if len(bundle.Cues) != 1 || bundle.Cues[0].Text != "b" {
		t.Fatalf("window cues = %+v", bundle.Cues)
	}
	if bundle.PlainText != "b" {
		t.Fatalf("window text = %q, want b", bundle.PlainText)
	}
}

func TestAssetTranscriptResolver_GenerationUnwiredFailsClosed(t *testing.T) {
	r, err := NewAssetTranscriptResolver(nil, nil, nil, &fakeAssetTranscriptTrackReader{}, zap.NewNop())
	if err != nil {
		t.Fatalf("NewAssetTranscriptResolver: %v", err)
	}
	_, found, err := r.FetchAssetTranscript(context.Background(), "asset-1", "it", 0, 0)
	if err == nil || found {
		t.Fatalf("FetchAssetTranscript = (found=%v, err=%v), want fail-closed error", found, err)
	}
	if !errors.Is(err, context.Canceled) && err.Error() == "" {
		t.Fatalf("unexpected empty error")
	}
}
