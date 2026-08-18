package localization

// localization_e2e_test.go is the first end-to-end test of the localization
// pipeline. It wires every step built so far — request → LocalizedClipPlan →
// fingerprint + validate → parallel scheduler → subtitle wire (TextTrack →
// ASS) → compiler (LocalizedClipPlan → RenderPlan) → render → artifact → docs
// — through fakes for the source registry, the text-track store, the Rust
// renderer and the Drive DocClient.
//
// The SAME helper runs 2 languages (EN + ES) and 10 languages with zero
// changes: the fan-out is data-driven, so scaling the language set never
// rewrites the pipeline.

import (
	"context"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// e2eSubtitleResolver echoes the expected SHA256 as the track's TextHash so
// the wire's hash check always matches for a well-formed plan, and returns
// one timed cue per track.
type e2eSubtitleResolver struct{}

func (e2eSubtitleResolver) ResolveSubtitleTrack(_ context.Context, trackID int64, expectedSHA256 string) (*ResolvedSubtitleTrack, error) {
	return &ResolvedSubtitleTrack{
		TrackID:  trackID,
		Cues:     []asset.TimedCue{{StartMs: 0, EndMs: 1000, Text: "localized cue"}},
		TextHash: expectedSHA256,
	}, nil
}

// e2eSubtitleCompiler produces a deterministic ASS asset keyed by language.
type e2eSubtitleCompiler struct{}

func (e2eSubtitleCompiler) Compile(_ context.Context, in SubtitleCompileInput) (*SubtitleAsset, error) {
	return &SubtitleAsset{
		LocalPath: "/tmp/ass/" + in.ClipID + "." + in.Language + ".ass",
		SHA256:    "ass-" + in.Language,
		StyleHash: in.StyleHash,
		TrackID:   in.TrackID,
	}, nil
}

// localizedPlanFor builds a valid, fingerprinted plan for one language.
func localizedPlanFor(lang string, priority int) LocalizedClipPlan {
	p := LocalizedClipPlan{
		Version:           LocalizedClipPlanVersion,
		JobID:             "job-e2e",
		SceneID:           "scene-1",
		ClipID:            "clip-1",
		SourceAssetID:     "source-asset-1",
		SourceSHA256:      strings.Repeat("a", 64),
		SourceLanguage:    "en",
		TargetLanguage:    lang,
		TranscriptTrackID: 101,
		TranscriptSHA256:  "transcript-sha",
		SubtitleTrackID:   int64(200 + priority),
		SubtitleSHA256:    "subtitle-" + lang,
		SubtitleStyleHash: "style-sha",
		DurationMS:        10000,
		OutputProfileHash: "profile-sha",
		RendererVersion:   "renderer-v1",
		Priority:          priority,
	}
	p.Fingerprint = Fingerprint(p)
	return p
}

// runLocalizationE2E runs the full fan-out for the given languages and returns
// the scheduler results + the assembled doc ref. This is the ONLY code path:
// 2 languages and 10 languages both flow through it unchanged.
func runLocalizationE2E(t *testing.T, languages []string, concurrency int) ([]TaskResult, *LocalizedDocumentRef) {
	t.Helper()

	compiler, err := NewLocalizedClipCompiler(fakeSourceResolver{facts: validSourceFacts()}, validCompilerConfig())
	if err != nil {
		t.Fatalf("NewLocalizedClipCompiler: %v", err)
	}
	wire, err := NewSubtitleWire(e2eSubtitleResolver{}, e2eSubtitleCompiler{}, "/tmp/ass")
	if err != nil {
		t.Fatalf("NewSubtitleWire: %v", err)
	}

	// One render function for the whole fan-out: subtitle wire → compile
	// render plan → simulated Rust render → certified artifact.
	renderFunc := func(ctx context.Context, plan LocalizedClipPlan) (LocalizedClipArtifact, error) {
		ass, err := wire.Wire(ctx, plan)
		if err != nil {
			return LocalizedClipArtifact{Status: LocalizedClipFailed}, err
		}
		rendered, err := compiler.Compile(ctx, plan)
		if err != nil {
			return LocalizedClipArtifact{Status: LocalizedClipFailed}, err
		}
		if err := rendered.Validate(); err != nil {
			return LocalizedClipArtifact{Status: LocalizedClipFailed}, err
		}
		return LocalizedClipArtifact{
			Version:         LocalizedClipArtifactVersion,
			JobID:           plan.JobID,
			SceneID:         plan.SceneID,
			ClipID:          plan.ClipID,
			Language:        plan.TargetLanguage,
			PlanFingerprint: plan.Fingerprint,
			AssetID:         "asset-" + plan.TargetLanguage,
			LocalPath:       rendered.OutputPath,
			// The burned output's bytes depend on the burned ASS, so the
			// certified content hash folds the subtitle artifact hash.
			SHA256:      "render-" + plan.TargetLanguage + "-" + ass.SHA256,
			SizeBytes:   1234,
			DurationMS:  plan.DurationMS,
			VideoCodec:  "h264",
			AudioCodec:  "aac",
			DriveFileID: "drive-" + plan.TargetLanguage,
			DriveLink:   "https://drive/" + plan.TargetLanguage,
			Status:      LocalizedClipUploaded,
		}, nil
	}

	scheduler, err := NewScheduler(context.Background(), renderFunc, concurrency)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	for i, lang := range languages {
		scheduler.Submit(LocalizedClipTask{Priority: i, Plan: localizedPlanFor(lang, i)})
	}
	results := scheduler.Wait()

	entries := make([]LocalizedDocumentEntry, 0, len(results))
	for _, r := range results {
		if r.Err != nil {
			t.Fatalf("language %q render failed: %v", r.Artifact.Language, r.Err)
		}
		entries = append(entries, LocalizedDocumentEntry{
			SceneID:      r.Artifact.SceneID,
			ClipID:       r.Artifact.ClipID,
			Language:     r.Artifact.Language,
			Priority:     r.Priority,
			VideoAssetID: r.Artifact.AssetID,
			DriveFileID:  r.Artifact.DriveFileID,
			DriveLink:    r.Artifact.DriveLink,
			DurationMS:   r.Artifact.DurationMS,
			SHA256:       r.Artifact.SHA256,
		})
	}

	assembler, err := NewDocumentAssembler(&fakeDocPublisher{result: &DocPublishResult{ID: "doc-e2e", Link: "https://docs/doc-e2e"}})
	if err != nil {
		t.Fatalf("NewDocumentAssembler: %v", err)
	}
	ref, err := assembler.Assemble(context.Background(), AssembleInput{Title: "Localization E2E", Entries: entries})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	return results, ref
}

// assertFanOut verifies the deterministic, priority-ordered result of a
// fan-out: one successful artifact per language, in request order.
func assertFanOut(t *testing.T, languages []string, results []TaskResult, ref *LocalizedDocumentRef) {
	t.Helper()
	if len(results) != len(languages) {
		t.Fatalf("results: got %d, want %d", len(results), len(languages))
	}
	if len(ref.Entries) != len(languages) {
		t.Fatalf("doc entries: got %d, want %d", len(ref.Entries), len(languages))
	}
	for i, lang := range languages {
		if results[i].Priority != i || results[i].Artifact.Language != lang {
			t.Fatalf("results[%d]: got priority=%d lang=%q, want %d/%q", i, results[i].Priority, results[i].Artifact.Language, i, lang)
		}
		if results[i].Artifact.Status != LocalizedClipUploaded {
			t.Errorf("results[%d] status: got %q, want UPLOADED", i, results[i].Artifact.Status)
		}
		if results[i].Artifact.PlanFingerprint != localizedPlanFor(lang, i).Fingerprint {
			t.Errorf("results[%d] plan_fingerprint drift", i)
		}
		if ref.Entries[i].Language != lang || ref.Entries[i].Priority != i {
			t.Fatalf("doc entries[%d]: got lang=%q priority=%d, want %q/%d", i, ref.Entries[i].Language, ref.Entries[i].Priority, lang, i)
		}
	}
}

// TestLocalizationE2E_EnEs is the FIRST end-to-end test: two languages, one
// full fan-out through every step.
func TestLocalizationE2E_EnEs(t *testing.T) {
	results, ref := runLocalizationE2E(t, []string{"en", "es"}, 2)
	assertFanOut(t, []string{"en", "es"}, results, ref)
}

// TestLocalizationE2E_TenLanguages scales the SAME pipeline to 10 languages
// with no rewrites — only the language slice changes.
func TestLocalizationE2E_TenLanguages(t *testing.T) {
	languages := []string{"en", "es", "it", "pt-BR", "fr", "de", "pl", "ru", "tr", "id"}
	results, ref := runLocalizationE2E(t, languages, 4)
	assertFanOut(t, languages, results, ref)
}

// TestLocalizationE2E_IsolatedLanguageFailureDoesNotBlockOthers verifies the
// fan-out degrades gracefully: a single failing language is recorded, and the
// remaining languages still render (the scheduler's per-task error contract).
func TestLocalizationE2E_IsolatedLanguageFailureDoesNotBlockOthers(t *testing.T) {
	compiler, err := NewLocalizedClipCompiler(fakeSourceResolver{facts: validSourceFacts()}, validCompilerConfig())
	if err != nil {
		t.Fatalf("NewLocalizedClipCompiler: %v", err)
	}
	wire, err := NewSubtitleWire(e2eSubtitleResolver{}, e2eSubtitleCompiler{}, "/tmp/ass")
	if err != nil {
		t.Fatalf("NewSubtitleWire: %v", err)
	}

	renderFunc := func(ctx context.Context, plan LocalizedClipPlan) (LocalizedClipArtifact, error) {
		if plan.TargetLanguage == "es" {
			return LocalizedClipArtifact{Language: plan.TargetLanguage, Status: LocalizedClipFailed}, context.DeadlineExceeded
		}
		if _, err := wire.Wire(ctx, plan); err != nil {
			return LocalizedClipArtifact{Status: LocalizedClipFailed}, err
		}
		if _, err := compiler.Compile(ctx, plan); err != nil {
			return LocalizedClipArtifact{Status: LocalizedClipFailed}, err
		}
		return LocalizedClipArtifact{Language: plan.TargetLanguage, Status: LocalizedClipUploaded}, nil
	}

	scheduler, err := NewScheduler(context.Background(), renderFunc, 3)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	languages := []string{"en", "es", "it"}
	for i, lang := range languages {
		scheduler.Submit(LocalizedClipTask{Priority: i, Plan: localizedPlanFor(lang, i)})
	}
	results := scheduler.Wait()

	if results[0].Err != nil || results[0].Artifact.Status != LocalizedClipUploaded {
		t.Errorf("en must succeed: %v", results[0].Err)
	}
	if results[1].Err == nil {
		t.Error("es must record its failure")
	}
	if results[2].Err != nil || results[2].Artifact.Status != LocalizedClipUploaded {
		t.Errorf("it must succeed despite es failing: %v", results[2].Err)
	}
}
