// Package ingest — clip_ingest_pipeline_test.go (PR-CLIPINGEST-PIPELINE, July 2026).
//
// Test surface:
//   - Happy path: Ingest walks 8 component stages; each called exactly
//     once; result.OK=true; FinalState=StateAssetIndexed.
//   - Fail-closed: any of the 9 components nil at construction surfaces
//     ErrClipIngestPipelineFailClosed.
//   - Empty URL: Ingest surfaces ErrClipIngestPipelineSourceRefEmpty.
//   - Dispatcher error propagation: stub returns sentinel; Ingest must
//     errors.Is surface the sentinel.
//
// Field shapes (canonical from internal/kernel/asset/*.go):
//   - asset.TranscriptResult{ Text string, DetectedLanguage string,
//     Confidence *float64 } — NO bare Language field.
//   - asset.TextTrack{ LanguageCode, TextContent, ... } — NO bare Language
//     or Text field (those would not compile against the canonical surface).
package ingest

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/mutations"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── Test stubs satisfying the 9 named ports ────────────────────────────

type stubDownloader struct{ called atomic.Int32 }

func (s *stubDownloader) Download(_ context.Context, _ assets.SourceRef) (*assets.StagedAsset, error) {
	s.called.Add(1)
	return &assets.StagedAsset{LocalPath: "/tmp/staged.mp4", Bytes: 1234}, nil
}

type stubNormalizer struct{ called atomic.Int32 }

func (s *stubNormalizer) Normalize(_ context.Context, _ *assets.StagedAsset) (NormalizedMedia, error) {
	s.called.Add(1)
	return NormalizedMedia{
		LocalPath: "/tmp/normalized.mp4",
		Container: "mp4",
		Codec:     "h264",
		Width:     1920,
		Height:    1080,
		Duration:  60000,
	}, nil
}

type stubHasher struct{ called atomic.Int32 }

func (s *stubHasher) Hash(_ context.Context, _ NormalizedMedia) (ContentHashResult, error) {
	s.called.Add(1)
	return ContentHashResult{ContentHash: "sha256:abcd1234", Algorithm: "sha256", Bytes: 1234}, nil
}

type stubStore struct{ called atomic.Int32 }

func (s *stubStore) StoreArtifact(_ context.Context, _ *artifacts.MediaRecord) error {
	s.called.Add(1)
	return nil
}

type stubTranscriber struct{ called atomic.Int32 }

func (s *stubTranscriber) Transcribe(_ context.Context, _ NormalizedMedia) (asset.TranscriptResult, error) {
	s.called.Add(1)
	// Canonical TranscriptResult field is DetectedLanguage (NOT Language).
	return asset.TranscriptResult{Text: "hello world", DetectedLanguage: "en"}, nil
}

type stubEnricher struct{ called atomic.Int32 }

func (s *stubEnricher) Enrich(_ context.Context, _ string, _ NormalizedMedia, _ asset.TranscriptResult) error {
	s.called.Add(1)
	return nil
}

type stubTranslator struct{ called atomic.Int32 }

func (s *stubTranslator) Translate(_ context.Context, _ string, _ asset.TranscriptResult) ([]asset.TextTrack, error) {
	s.called.Add(1)
	// Canonical TextTrack fields: LanguageCode, TextContent, TextKind
	// (typed-kind). Min-shape literal valid against the canonical
	// TextTrack declaration.
	return []asset.TextTrack{{
		LanguageCode: "en",
		TextKind:     asset.TextTrackTranscript,
		TextContent:  "hello world",
	}}, nil
}

type stubComposer struct{ called atomic.Int32 }

func (s *stubComposer) Compose(_ context.Context, _ ClipIngestContext) (string, error) {
	s.called.Add(1)
	return "search-text-composed", nil
}

type stubDispatcher struct {
	called atomic.Int32
	err    error
}

func (s *stubDispatcher) EnqueueAndIndex(_ context.Context, _ *asset.Asset, _ string) error {
	s.called.Add(1)
	return s.err
}

func (s *stubDispatcher) EnqueueAndRestore(_ context.Context, _ string) error { return nil }
func (s *stubDispatcher) EnqueueAndDelete(_ context.Context, _ string) error  { return nil }

// compile-time port-pin: stub satisfies the canonical AssetMutationDispatcher interface.
var _ mutations.AssetMutationDispatcher = (*stubDispatcher)(nil)

func newAllPassDeps() (ClipIngestPipelineDeps, *stubDownloader, *stubNormalizer, *stubHasher, *stubStore, *stubTranscriber, *stubEnricher, *stubTranslator, *stubComposer, *stubDispatcher) {
	d := &stubDownloader{}
	n := &stubNormalizer{}
	h := &stubHasher{}
	st := &stubStore{}
	tr := &stubTranscriber{}
	e := &stubEnricher{}
	tt := &stubTranslator{}
	sc := &stubComposer{}
	disp := &stubDispatcher{}
	return ClipIngestPipelineDeps{
		MediaProcessing: MediaProcessingDeps{
			Downloader:      d,
			MediaNormalizer: n,
			ContentHasher:   h,
			ArtifactStore:   st,
		},
		Enrichment: EnrichmentDeps{
			Transcriber:         tr,
			ClipEnricher:        e,
			TextTrackTranslator: tt,
			SearchTextComposer:  sc,
		},
		AssetMutationDispatcher: disp,
		Log:                     zap.NewNop(),
	}, d, n, h, st, tr, e, tt, sc, disp
}

func TestClipIngestPipeline_Ingest_HappyPath(t *testing.T) {
	deps, d, n, h, st, tr, e, tt, sc, disp := newAllPassDeps()
	p, err := NewClipIngestPipeline(deps)
	if err != nil {
		t.Fatalf("NewClipIngestPipeline: %v", err)
	}
	res, err := p.Ingest(context.Background(), assets.SourceRef{URL: "https://example.com/clip-mp4"})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if !res.OK {
		t.Fatalf("res.OK = false (want true)")
	}
	if res.FinalState != asset.StateAssetIndexed {
		t.Fatalf("FinalState = %v, want %v", res.FinalState, asset.StateAssetIndexed)
	}
	if res.SearchText != "search-text-composed" {
		t.Fatalf("SearchText = %q, want search-text-composed", res.SearchText)
	}
	pairs := map[string]*atomic.Int32{
		"Downloader":    &d.called,
		"Normalizer":    &n.called,
		"Hasher":        &h.called,
		"ArtifactStore": &st.called,
		"Transcriber":   &tr.called,
		"Enricher":      &e.called,
		"Translator":    &tt.called,
		"Composer":      &sc.called,
		"Dispatcher":    &disp.called,
	}
	for name, c := range pairs {
		if c.Load() != 1 {
			t.Errorf("%s called %d times, want 1", name, c.Load())
		}
	}
}

func TestClipIngestPipeline_NilAnyComponent_Fails(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ClipIngestPipelineDeps)
	}{
		{"nil Downloader", func(d *ClipIngestPipelineDeps) { d.MediaProcessing.Downloader = nil }},
		{"nil MediaNormalizer", func(d *ClipIngestPipelineDeps) { d.MediaProcessing.MediaNormalizer = nil }},
		{"nil ContentHasher", func(d *ClipIngestPipelineDeps) { d.MediaProcessing.ContentHasher = nil }},
		{"nil ArtifactStore", func(d *ClipIngestPipelineDeps) { d.MediaProcessing.ArtifactStore = nil }},
		{"nil Transcriber", func(d *ClipIngestPipelineDeps) { d.Enrichment.Transcriber = nil }},
		{"nil ClipEnricher", func(d *ClipIngestPipelineDeps) { d.Enrichment.ClipEnricher = nil }},
		{"nil TextTrackTranslator", func(d *ClipIngestPipelineDeps) { d.Enrichment.TextTrackTranslator = nil }},
		{"nil SearchTextComposer", func(d *ClipIngestPipelineDeps) { d.Enrichment.SearchTextComposer = nil }},
		{"nil AssetMutationDispatcher", func(d *ClipIngestPipelineDeps) { d.AssetMutationDispatcher = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps, _, _, _, _, _, _, _, _, _ := newAllPassDeps()
			tc.mutate(&deps)
			_, err := NewClipIngestPipeline(deps)
			if err == nil {
				t.Fatalf("NewClipIngestPipeline: want fail-closed error, got nil")
			}
			if !errors.Is(err, ErrClipIngestPipelineFailClosed) {
				t.Fatalf("err = %v, want chain containing ErrClipIngestPipelineFailClosed", err)
			}
		})
	}
}

func TestClipIngestPipeline_Ingest_EmptyURLTypedSentinel(t *testing.T) {
	deps, _, _, _, _, _, _, _, _, _ := newAllPassDeps()
	p, _ := NewClipIngestPipeline(deps)
	if p == nil {
		t.Fatalf("NewClipIngestPipeline: nil")
	}
	_, err := p.Ingest(context.Background(), assets.SourceRef{URL: ""})
	if err == nil {
		t.Fatalf("Ingest(empty URL): want ErrClipIngestPipelineSourceRefEmpty")
	}
	if !errors.Is(err, ErrClipIngestPipelineSourceRefEmpty) {
		t.Fatalf("err = %v, want chain containing ErrClipIngestPipelineSourceRefEmpty", err)
	}
}

func TestClipIngestPipeline_Ingest_DispatcherErrorPropagates(t *testing.T) {
	deps, _, _, _, _, _, _, _, _, disp := newAllPassDeps()
	sentinel := errors.New("db unavailable at dispatcher")
	disp.err = sentinel
	p, _ := NewClipIngestPipeline(deps)
	if p == nil {
		t.Fatalf("NewClipIngestPipeline: nil")
	}
	_, err := p.Ingest(context.Background(), assets.SourceRef{URL: "https://example.com/clip-mp4"})
	if err == nil {
		t.Fatalf("Ingest: want non-nil error")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want chain containing sentinel %v", err, sentinel)
	}
}
