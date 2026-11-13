package adapters

// subtitle_multilingual_e2e_test.go — the offline end-to-end certificate for
// the subtitle handoff: a Spanish source clip rendered with Italian text must
// resolve the Italian track, compile an Italian ASS, and never write the
// Spanish transcript into it. It exercises the real plan builder, the real
// TextTrack→cues resolver and the real ASS compiler; only the track store is a
// fake, because the capability under test is the language routing, not SQL.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/localization"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

type e2eTrackStore struct {
	refs map[string]localization.TrackRef
}

func (s *e2eTrackStore) ResolveTrack(_ context.Context, assetID, language string, _ detail.TextTrackKind) (*localization.TrackRef, error) {
	ref, ok := s.refs[language]
	if !ok {
		return nil, fmt.Errorf("no READY track for %s/%s", assetID, language)
	}
	return &ref, nil
}

func TestSubtitlePipeline_SpanishClipItalianTextCompilesItalianASS(t *testing.T) {
	const (
		assetID     = "clip-1"
		sourceText  = "Hola mundo"
		italianText = "Ciao mondo"
	)
	sourceHash := strings.Repeat("a", 64)
	italianHash := detail.TextHash(italianText, "it", detail.TextTrackTranscript)

	store := &e2eTrackStore{refs: map[string]localization.TrackRef{
		"es": {TrackID: 201, SHA256: sourceHash},
		"it": {TrackID: 202, SHA256: italianHash},
	}}
	builder, err := localization.NewLocalizationPlanBuilder(store)
	if err != nil {
		t.Fatalf("NewLocalizationPlanBuilder: %v", err)
	}
	plans, err := builder.Build(context.Background(), localization.SourceInput{
		JobID:             "job-1",
		SceneID:           "scene-1",
		AssetID:           assetID,
		SourceLanguage:    "es",
		SourceSHA256:      sourceHash,
		DurationMS:        6500,
		OutputProfileHash: "profile-sha",
		RendererVersion:   "renderer-v1",
		SubtitleStyleHash: "shorts-v1",
	}, []localization.LanguageRequest{{Language: "it", Priority: 0}})
	if err != nil {
		t.Fatalf("Build plans: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("plans = %d, want 1", len(plans))
	}
	plan := plans[0]
	if plan.SourceLanguage != "es" || plan.TargetLanguage != "it" {
		t.Fatalf("plan languages = %s->%s, want es->it", plan.SourceLanguage, plan.TargetLanguage)
	}
	if plan.TranscriptTrackID != 201 {
		t.Fatalf("transcript track = %d, want the Spanish source (201)", plan.TranscriptTrackID)
	}
	if plan.SubtitleTrackID != 202 || plan.SubtitleSHA256 != italianHash {
		t.Fatalf("subtitle track = (%d, %q), want the Italian track (202, %q)", plan.SubtitleTrackID, plan.SubtitleSHA256, italianHash)
	}

	// Real resolver over the Italian track + real ASS compiler.
	resolver := NewSubtitleResolver(&fakeTextTrackByIDReader{
		track: readyTrack(202, "it", italianText),
		cues:  []detail.TimedCue{{StartMs: 0, EndMs: 3000, Text: italianText}},
	})
	wire, err := localization.NewSubtitleWire(resolver, NewSubtitleCompiler(), t.TempDir())
	if err != nil {
		t.Fatalf("NewSubtitleWire: %v", err)
	}
	artifact, err := wire.Wire(context.Background(), plan)
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	if filepath.Base(artifact.LocalPath) != "clip-1.it.ass" {
		t.Fatalf("artifact = %q, want clip-1.it.ass", artifact.LocalPath)
	}
	content, err := os.ReadFile(artifact.LocalPath)
	if err != nil {
		t.Fatalf("read ASS: %v", err)
	}
	ass := string(content)
	if !strings.Contains(ass, italianText) {
		t.Fatalf("ASS does not contain the Italian text %q:\n%s", italianText, ass)
	}
	if strings.Contains(ass, sourceText) {
		t.Fatalf("ASS leaked the Spanish source text %q:\n%s", sourceText, ass)
	}
	if err := texttracks.ValidateASSFile(artifact.LocalPath, plan.DurationMS); err != nil {
		t.Fatalf("ValidateASSFile: %v", err)
	}
}
