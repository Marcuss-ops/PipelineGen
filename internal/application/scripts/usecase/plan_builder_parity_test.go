package usecase

import (
	"reflect"
	"testing"

	generationpkg "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/generation"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// TestBuildPlanParityWithGeneration pins the EXPAND/BACKFILL boundary:
// generation is the canonical future owner, while usecase remains the
// compatibility facade until CUTOVER. Any behavior drift must be resolved
// before callers are switched to generation.BuildPlan.
func TestBuildPlanParityWithGeneration(t *testing.T) {
	cases := []struct {
		name string
		item scriptpkg.GenerationItemV2
	}{
		{
			name: "text_defaults",
			item: scriptpkg.GenerationItemV2{
				ID: "text-1", Language: "it", Style: "documentary", Tone: "neutral",
				Source: scriptpkg.SourceSpec{Type: scriptpkg.SourceText, Topic: "topic", SourceText: "source"},
			},
		},
		{
			name: "clip_translation_persistence",
			item: scriptpkg.GenerationItemV2{
				ID: "clip-1", Language: "en", MediaMode: scriptpkg.MediaModeClipOnly,
				Source: scriptpkg.SourceSpec{Type: scriptpkg.SourceClips, ClipIDs: []string{"clip-a"}},
				Output: scriptpkg.OutputSpec{TranslateTo: "it", SaveToDB: true, VoiceoverFolderID: "voiceover-folder"},
			},
		},
		{
			name: "media_metadata_documents",
			item: scriptpkg.GenerationItemV2{
				ID: "media-1", Language: "en",
				Source: scriptpkg.SourceSpec{Type: scriptpkg.SourceSearch, Topic: "search topic"},
				Docs:   scriptpkg.DocumentsSpec{Enabled: true, Languages: []string{"en", "it"}, FolderID: "docs-folder"}, MediaPlan: media.MediaPlanSpec{Mode: "hybrid"},
				VideoMetadata: &scriptpkg.VideoMetadata{Title: "manual", Tags: []string{"one"}, TranslationStatus: "translated"},
				Output:        scriptpkg.OutputSpec{GenerateSceneImages: scriptpkg.ToggleEnabled, SaveToDB: true},
			},
		},
		{
			name: "segments_and_guidelines",
			item: scriptpkg.GenerationItemV2{
				ID: "segments-1", Language: "it",
				Source: scriptpkg.SourceSpec{Type: scriptpkg.SourceCatalog, Topic: "catalog", Guidelines: "source guidance"},
				ScriptParams: scriptpkg.ScriptSpec{
					Guidelines: "script guidance", TargetWords: 400, MinWords: 200,
					Segments:      []scriptpkg.ScriptSegment{{ID: "s1", Topic: "one", SourceText: "text"}},
				},
				Output: scriptpkg.OutputSpec{StockEnabled: scriptpkg.ToggleEnabled, StockBindings: []scriptpkg.StockBindingInput{{}}},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			legacy := BuildPlan(tc.item)
			canonical := generationpkg.BuildPlan(tc.item)
			if !reflect.DeepEqual(legacy, canonical) {
				t.Fatalf("legacy usecase plan diverged from canonical generation plan\nlegacy=%#v\ncanonical=%#v", legacy, canonical)
			}
		})
	}
}

func TestBuildPlansParityWithGeneration(t *testing.T) {
	items := []scriptpkg.GenerationItemV2{
		{ID: "one", Source: scriptpkg.SourceSpec{Type: scriptpkg.SourceText, Topic: "one"}},
		{ID: "two", Source: scriptpkg.SourceSpec{Type: scriptpkg.SourceClips, ClipIDs: []string{"clip-1"}}, Output: scriptpkg.OutputSpec{SaveToDB: true}},
	}

	legacy := BuildPlans(items)
	canonical := generationpkg.BuildPlans(items)
	if !reflect.DeepEqual(legacy, canonical) {
		t.Fatalf("legacy usecase plans diverged from canonical generation plans\nlegacy=%#v\ncanonical=%#v", legacy, canonical)
	}
}
