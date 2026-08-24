package wiring

// localization_service_test.go — the composition root: NewLocalizationService
// (fail-closed wiring) + Localize (the full fan-out entry) +
// LocalizationConfigFromConfig (deterministic deployment facts).

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/localization"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// ── service fakes ───────────────────────────────────────────────────

// fakeServiceSourceResolver returns fixed SourceFacts for the fan-out entry.
type fakeServiceSourceResolver struct {
	facts localization.SourceFacts
	err   error
}

func (f fakeServiceSourceResolver) ResolveSource(_ context.Context, assetID string) (localization.SourceFacts, error) {
	if f.err != nil {
		return localization.SourceFacts{}, f.err
	}
	return f.facts, nil
}

// fakeServiceTrackResolver returns a READY track ref per (asset, language).
type fakeServiceTrackResolver struct{}

func (fakeServiceTrackResolver) ResolveTrack(_ context.Context, _ string, language string, _ asset.TextTrackKind) (*localization.TrackRef, error) {
	if language == "missing" {
		return nil, errors.New("no READY track")
	}
	return &localization.TrackRef{TrackID: int64(len(language) * 10), SHA256: "track-" + language}, nil
}

// fakeServiceSubtitleResolver echoes the plan's expected hash as the text
// hash so the wire always matches a well-formed plan.
type fakeServiceSubtitleResolver struct{}

func (fakeServiceSubtitleResolver) ResolveSubtitleTrack(_ context.Context, trackID int64, expected string) (*localization.ResolvedSubtitleTrack, error) {
	return &localization.ResolvedSubtitleTrack{
		TrackID:  trackID,
		Cues:     []asset.TimedCue{{StartMs: 0, EndMs: 1000, Text: "cue"}},
		TextHash: expected,
	}, nil
}

// fakeServiceSubtitleCompiler produces a deterministic ASS asset.
type fakeServiceSubtitleCompiler struct{}

func (fakeServiceSubtitleCompiler) Compile(_ context.Context, in localization.SubtitleCompileInput) (*localization.SubtitleAsset, error) {
	return &localization.SubtitleAsset{
		LocalPath: "/tmp/ass/" + in.ClipID + "." + in.Language + ".ass",
		SHA256:    "ass-" + in.Language,
		StyleHash: in.StyleHash,
		TrackID:   in.TrackID,
	}, nil
}

// fakeServiceExecutor executes the sealed render plan and returns verified
// facts without touching Rust.
type fakeServiceExecutor struct{}

func (fakeServiceExecutor) Execute(_ context.Context, plan render.RenderPlan, _ *localization.SubtitleAsset) (localization.RenderFacts, error) {
	return localization.RenderFacts{
		LocalPath:  plan.OutputPath,
		SHA256:     strings.Repeat("a", 64),
		SizeBytes:  1234,
		DurationMS: 10000,
		VideoCodec: "h264",
		AudioCodec: "aac",
	}, nil
}

// fakeServiceUploader records every upload input and returns a Drive result.
type fakeServiceUploader struct {
	mu  sync.Mutex
	got []localization.DriveUploadInput
}

func (f *fakeServiceUploader) Upload(_ context.Context, in localization.DriveUploadInput) (*localization.DriveUploadResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, in)
	return &localization.DriveUploadResult{FileID: "drive-" + in.Language, Link: "https://drive/" + in.Language}, nil
}

// fakeServiceDocPublisher records the publish input and returns a doc ref.
type fakeServiceDocPublisher struct {
	got localization.DocPublishInput
}

func (f *fakeServiceDocPublisher) Publish(_ context.Context, in localization.DocPublishInput) (*localization.DocPublishResult, error) {
	f.got = in
	return &localization.DocPublishResult{ID: "doc-1", Link: "https://docs/doc-1"}, nil
}

func testServiceConfig() LocalizationConfig {
	return LocalizationConfig{
		SourceLanguage:    "en",
		OutputProfileHash: "profile-sha",
		RendererVersion:   "renderer-v1",
		SubtitleStyleHash: "style-sha",
		EncoderPolicyHash: strings.Repeat("b", 64),
		WorkDir:           "/tmp/renders",
	}
}

func newTestLocalizationService(t *testing.T) (*LocalizationService, *fakeServiceUploader, *fakeServiceDocPublisher) {
	t.Helper()
	uploader := &fakeServiceUploader{}
	docs := &fakeServiceDocPublisher{}
	svc, err := NewLocalizationService(LocalizationDeps{
		Sources: fakeServiceSourceResolver{facts: localization.SourceFacts{
			AssetID:    "source-asset-1",
			LocalPath:  "/media/source.mp4",
			SHA256:     strings.Repeat("a", 64),
			DurationMS: 10000,
			FrameRate:  audio.IntegerFrameRate(30),
		}},
		TrackResolver:    fakeServiceTrackResolver{},
		SubtitleResolver: fakeServiceSubtitleResolver{},
		SubtitleCompiler: fakeServiceSubtitleCompiler{},
		Executor:         fakeServiceExecutor{},
		Uploader:         uploader,
		DocPublisher:     docs,
	}, testServiceConfig())
	if err != nil {
		t.Fatalf("NewLocalizationService: %v", err)
	}
	return svc, uploader, docs
}

// ── NewLocalizationService (fail-closed wiring) ─────────────────────

func TestNewLocalizationService_FailsClosedOnMissingPorts(t *testing.T) {
	base := func() LocalizationDeps {
		return LocalizationDeps{
			Sources:          fakeServiceSourceResolver{},
			TrackResolver:    fakeServiceTrackResolver{},
			SubtitleResolver: fakeServiceSubtitleResolver{},
			SubtitleCompiler: fakeServiceSubtitleCompiler{},
			Executor:         fakeServiceExecutor{},
			Uploader:         &fakeServiceUploader{},
			DocPublisher:     &fakeServiceDocPublisher{},
		}
	}
	cases := map[string]func(*LocalizationDeps){
		"nil sources":           func(d *LocalizationDeps) { d.Sources = nil },
		"nil track resolver":    func(d *LocalizationDeps) { d.TrackResolver = nil },
		"nil subtitle resolver": func(d *LocalizationDeps) { d.SubtitleResolver = nil },
		"nil subtitle compiler": func(d *LocalizationDeps) { d.SubtitleCompiler = nil },
		"nil executor":          func(d *LocalizationDeps) { d.Executor = nil },
		"nil uploader":          func(d *LocalizationDeps) { d.Uploader = nil },
		"nil doc publisher":     func(d *LocalizationDeps) { d.DocPublisher = nil },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			deps := base()
			mutate(&deps)
			if _, err := NewLocalizationService(deps, testServiceConfig()); err == nil {
				t.Fatalf("NewLocalizationService must fail closed on %s", name)
			}
		})
	}
}

// ── Localize (fan-out entry) ────────────────────────────────────────

func localizationRequestFor(langs ...string) localization.LocalizationRequest {
	req := localization.LocalizationRequest{RenderConcurrency: 2}
	for i, l := range langs {
		req.Languages = append(req.Languages, localization.LanguageRequest{Language: l, Priority: i})
	}
	return req
}

func TestLocalize_RunsFullFanOut(t *testing.T) {
	svc, uploader, docs := newTestLocalizationService(t)

	result, err := svc.Localize(context.Background(), LocalizeInput{
		AssetID:     "source-asset-1",
		JobID:       "job-1",
		SceneID:     "scene-1",
		ClipID:      "clip-1",
		Request:     localizationRequestFor("en", "es"),
		FolderID:    "folder-1",
		DocTitle:    "Localization — clip-1",
		DocFolderID: "docs-folder",
	})
	if err != nil {
		t.Fatalf("Localize: %v", err)
	}
	if len(result.Artifacts) != 2 {
		t.Fatalf("artifacts: got %d, want 2", len(result.Artifacts))
	}
	for i, lang := range []string{"en", "es"} {
		if result.Artifacts[i].Language != lang {
			t.Fatalf("artifacts[%d].Language = %q, want %q", i, result.Artifacts[i].Language, lang)
		}
		if result.Artifacts[i].Status != localization.LocalizedClipUploaded {
			t.Fatalf("artifacts[%d].Status = %q, want UPLOADED", i, result.Artifacts[i].Status)
		}
	}
	if len(result.Failures) != 0 {
		t.Fatalf("failures: got %d, want 0", len(result.Failures))
	}
	if result.Ref == nil || result.Ref.ID != "doc-1" || len(result.Ref.Entries) != 2 {
		t.Fatalf("doc ref = %+v", result.Ref)
	}
	// The uploader must receive the deterministic "<clip>.<lang>.mp4" filename
	// and the certified content hash for every language.
	if len(uploader.got) != 2 {
		t.Fatalf("uploader calls: got %d, want 2", len(uploader.got))
	}
	byLang := map[string]localization.DriveUploadInput{}
	for _, in := range uploader.got {
		byLang[in.Language] = in
	}
	for _, lang := range []string{"en", "es"} {
		in, ok := byLang[lang]
		if !ok {
			t.Fatalf("uploader never received language %q", lang)
		}
		if in.Filename != "clip-1."+lang+".mp4" || in.FolderID != "folder-1" {
			t.Fatalf("uploader input for %q = %+v", lang, in)
		}
		if in.ContentHash != strings.Repeat("a", 64) {
			t.Fatalf("uploader content hash for %q = %q", lang, in.ContentHash)
		}
	}
	// The doc publisher must receive the priority-ordered manifest.
	if docs.got.Title != "Localization — clip-1" || !strings.Contains(docs.got.Content, "English") {
		t.Fatalf("doc publish input title/content unexpected: %+v", docs.got)
	}
}

func TestLocalize_RejectsInvalidRequest(t *testing.T) {
	svc, _, _ := newTestLocalizationService(t)
	_, err := svc.Localize(context.Background(), LocalizeInput{
		AssetID: "source-asset-1",
		Request: localization.LocalizationRequest{}, // empty languages
	})
	if err == nil {
		t.Fatal("Localize must reject an invalid request")
	}
}

func TestLocalize_SkipDocumentOnlyUploadsClips(t *testing.T) {
	svc, _, docs := newTestLocalizationService(t)
	result, err := svc.Localize(context.Background(), LocalizeInput{
		AssetID:      "source-asset-1",
		Request:      localizationRequestFor("en"),
		FolderID:     "clip-folder",
		SkipDocument: true,
	})
	if err != nil {
		t.Fatalf("Localize: %v", err)
	}
	if len(result.Artifacts) != 1 || result.Ref != nil {
		t.Fatalf("clip-only localization result = %+v", result)
	}
	if docs.got.Title != "" {
		t.Fatalf("clip localization must not publish a Google Doc: %+v", docs.got)
	}
}

func TestLocalize_PropagatesSourceResolutionError(t *testing.T) {
	svc, _, _ := newTestLocalizationService(t)
	svc.sources = fakeServiceSourceResolver{err: errors.New("asset not found")}
	if _, err := svc.Localize(context.Background(), LocalizeInput{
		AssetID: "missing",
		Request: localizationRequestFor("en"),
	}); err == nil {
		t.Fatal("Localize must propagate a source resolution error")
	}
}

func TestLocalize_PropagatesPlanBuildError(t *testing.T) {
	svc, _, _ := newTestLocalizationService(t)
	// The "missing" language has no READY track → plan build fails.
	req := localizationRequestFor("en", "missing")
	if _, err := svc.Localize(context.Background(), LocalizeInput{
		AssetID: "source-asset-1",
		Request: req,
	}); err == nil {
		t.Fatal("Localize must propagate a plan build error")
	}
}

func TestLocalize_SourceLanguageOverride(t *testing.T) {
	svc, _, _ := newTestLocalizationService(t)
	// Source language override changes which language burns the transcript;
	// the fan-out must still succeed (both languages resolve to tracks).
	result, err := svc.Localize(context.Background(), LocalizeInput{
		AssetID:        "source-asset-1",
		SourceLanguage: "it",
		Request:        localizationRequestFor("it", "es"),
		FolderID:       "folder-1",
	})
	if err != nil {
		t.Fatalf("Localize: %v", err)
	}
	if len(result.Artifacts) != 2 {
		t.Fatalf("artifacts: got %d, want 2", len(result.Artifacts))
	}
}

func TestLocalize_NilReceiverFailsClosed(t *testing.T) {
	var svc *LocalizationService
	if _, err := svc.Localize(context.Background(), LocalizeInput{AssetID: "x", Request: localizationRequestFor("en")}); err == nil {
		t.Fatal("Localize on a nil service must fail")
	}
}

// ── LocalizationConfigFromConfig ────────────────────────────────────

func TestLocalizationConfigFromConfig_Deterministic(t *testing.T) {
	cfg := &config.Config{}
	c1 := LocalizationConfigFromConfig(cfg)
	c2 := LocalizationConfigFromConfig(cfg)
	if c1.OutputProfileHash != c2.OutputProfileHash || c1.EncoderPolicyHash != c2.EncoderPolicyHash {
		t.Fatalf("config facts must be deterministic: %+v vs %+v", c1, c2)
	}
	if len(c1.OutputProfileHash) != 64 || len(c1.EncoderPolicyHash) != 64 {
		t.Fatalf("profile/policy hashes must be 64-hex: %q / %q", c1.OutputProfileHash, c1.EncoderPolicyHash)
	}
	if c1.RendererVersion != LocalizationRendererVersion || c1.SubtitleStyleHash != LocalizationSubtitleStyleHash {
		t.Fatalf("canonical constants not applied: %+v", c1)
	}
	if c1.SourceLanguage != "en" {
		t.Fatalf("default source language = %q, want en", c1.SourceLanguage)
	}
}

func TestLocalizationConfigFromConfig_ProfileChangeBumpsHash(t *testing.T) {
	cfg := &config.Config{}
	base := LocalizationConfigFromConfig(cfg)
	cfg.Video.Width = 1280
	cfg.Video.Height = 720
	changed := LocalizationConfigFromConfig(cfg)
	if base.OutputProfileHash == changed.OutputProfileHash {
		t.Fatal("profile change must bump the output profile hash")
	}
}

func TestLocalizationConfigFromConfig_SourceLanguageFromMultilingual(t *testing.T) {
	cfg := &config.Config{}
	cfg.Media.Multilingual.SourceLanguage = "it"
	c := LocalizationConfigFromConfig(cfg)
	if c.SourceLanguage != "it" {
		t.Fatalf("source language = %q, want it", c.SourceLanguage)
	}
}
