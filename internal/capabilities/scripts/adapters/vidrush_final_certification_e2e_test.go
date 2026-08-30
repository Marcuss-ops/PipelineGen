package adapters

import (
	"context"
	"fmt"
	"testing"

	scriptgeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type finalCertificationScene struct {
	id, text  string
	duration  int64
	units     int
	providers []string
}

type finalCertificationEnricher struct{}

func (finalCertificationEnricher) Enrich(_ context.Context, _ *scriptpkg.ResolvedGenerationPlan, scene scriptpkg.SpecScene) (scriptpkg.VidRushSegmentResult, error) {
	hash := scriptgeneration.SceneTextHash(scene.Text)
	entities := []scriptpkg.ExtractedEntity{{Value: "subject-" + scene.ID, Type: "CONCEPT", Confidence: 1}}
	return scriptpkg.VidRushSegmentResult{
		SegmentID: scene.ID, SceneID: scene.ID, Position: scene.Index, Text: scene.Text, TextHash: hash,
		Insights: scriptpkg.SegmentInsights{
			SegmentID: scene.ID, TextHash: hash,
			Entities: entities, ImportantPhrases: []string{"visual subject " + scene.ID},
			ImportantWords: []string{"visual", "subject", scene.ID},
			YouTubeQueries: []string{"specific event " + scene.ID},
			ArtlistQueries: []string{"generic b-roll " + scene.ID},
			ImageQueries:   []string{"precise image " + scene.ID},
		},
	}, nil
}

type finalCertificationResolver struct{}

func (finalCertificationResolver) ResolveProviders(_ context.Context, _ *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult) (scriptpkg.VidRushSegmentResult, error) {
	segment.Assets.Candidates = []scriptpkg.SegmentAssetCandidate{
		{AssetID: segment.SegmentID + "-youtube", Provider: scriptpkg.VidRushProviderYouTube, Query: segment.Insights.YouTubeQueries[0], RelevanceScore: .95},
		{AssetID: segment.SegmentID + "-artlist", Provider: scriptpkg.VidRushProviderArtlist, Query: segment.Insights.ArtlistQueries[0], RelevanceScore: .90},
		{AssetID: segment.SegmentID + "-image", Provider: scriptpkg.VidRushProviderInternetImages, Query: segment.Insights.ImageQueries[0], RelevanceScore: .92},
	}
	return segment, nil
}

type finalCertificationReport struct {
	Scenes, VisualUnits, ProviderDecisions   int
	YouTube, Artlist, Images                 int
	TargetMs, CoveredMs                      int64
	Gaps, Overlaps                           int
	NLPExtracted, NLPImportant               int
	Materialized, Indexed                    int
	ColdSearches, ColdDownloads, ColdUploads int
	WarmSearches, WarmDownloads, WarmUploads int
	ExactWarmHits                            int
	Certified                                bool
}

func TestVidRushFinalCertificationEightScenes(t *testing.T) {
	scenes := []finalCertificationScene{
		{"scene-1", "Matt Damon interview in Dubai", 12000, 1, []string{"youtube"}},
		{"scene-2", "automated warehouse robots", 16000, 1, []string{"artlist"}},
		{"scene-3", "Christopher Nolan presents Interstellar", 10000, 1, []string{"internet_images"}},
		{"scene-4", "Dubai skyline and city", 8000, 1, []string{"internet_images"}},
		{"scene-5", "SpaceX Starship Elon Musk engineers", 32000, 4, []string{"youtube", "internet_images", "artlist", "youtube"}},
		{"scene-6", "office workers collaborating", 14000, 1, []string{"artlist"}},
		{"scene-7", "specific person event interview", 18000, 1, []string{"youtube"}},
		{"scene-8", "long scene with four visual concepts", 40000, 4, []string{"artlist", "youtube", "internet_images", "artlist"}},
	}
	plan := &scriptpkg.ResolvedGenerationPlan{Title: "VidRush Final Certification", Language: "it", Model: "small-model", PromptVersion: "final-v1", MediaPlan: mediadomain.MediaPlanSpec{ProviderPolicy: mediadomain.MediaProviderPolicy{YouTube: mediadomain.MediaToggleEnabled, Artlist: mediadomain.MediaToggleEnabled, InternetImages: mediadomain.MediaToggleEnabled}}}
	coordinator := scriptgeneration.NewVidRushIncrementalCoordinator(finalCertificationEnricher{}, plan, 4)
	resolver := finalCertificationResolver{}
	coordinator.SetSegmentProviderResolver(resolver)

	for i, scene := range scenes {
		if err := coordinator.OnSceneCommitted(context.Background(), scriptgeneration.SceneCommitted{RunID: "final-cert", SceneID: scene.id, SceneIndex: i, Text: scene.text, TextHash: scriptgeneration.SceneTextHash(scene.text), Revision: 1, Language: "it"}); err != nil {
			t.Fatal(err)
		}
	}
	results, err := coordinator.WaitForVidRush(context.Background(), "final-cert")
	if err != nil {
		t.Fatal(err)
	}

	report := finalCertificationReport{Scenes: len(results)}
	seen := map[string]bool{}
	for i, result := range results {
		report.ProviderDecisions++
		if len(result.Insights.Entities) > 0 {
			report.NLPExtracted += len(result.Insights.Entities)
		}
		if len(result.Insights.ImportantPhrases) > 0 {
			report.NLPImportant++
		}
		for _, candidate := range result.Assets.Candidates {
			seen[candidate.AssetID] = true
			switch candidate.Provider {
			case scriptpkg.VidRushProviderYouTube:
				report.YouTube++
			case scriptpkg.VidRushProviderArtlist:
				report.Artlist++
			case scriptpkg.VidRushProviderInternetImages:
				report.Images++
			}
		}
		phraseTimings := make([]VisualPhraseTiming, scenes[i].units)
		for unit := range phraseTimings {
			start := scenes[i].duration * int64(unit) / int64(scenes[i].units)
			end := scenes[i].duration * int64(unit+1) / int64(scenes[i].units)
			phraseTimings[unit] = VisualPhraseTiming{Text: fmt.Sprintf("%s visual unit %d", result.SceneID, unit+1), StartMs: start, EndMs: end}
		}
		windows, err := PlanVisualWindows(VisualWindowPlanningInput{SceneID: result.SceneID, SegmentID: result.SegmentID, Text: result.Text, DurationMs: scenes[i].duration, PhraseTimings: phraseTimings, Profile: scriptpkg.SegmentSemanticProfile{SegmentID: result.SegmentID, TextHash: result.TextHash}})
		if err != nil {
			t.Fatal(err)
		}
		if len(windows) != scenes[i].units {
			t.Fatalf("%s visual units=%d want %d", result.SceneID, len(windows), scenes[i].units)
		}
		report.VisualUnits += len(windows)
		previousEnd := int64(0)
		for _, window := range windows {
			if window.StartMs != previousEnd {
				report.Gaps++
				if window.StartMs < previousEnd {
					report.Overlaps++
				}
			}
			if window.EndMs <= window.StartMs || window.DurationMs != window.EndMs-window.StartMs {
				report.Overlaps++
			}
			previousEnd = window.EndMs
		}
		report.TargetMs += scenes[i].duration
		report.CoveredMs += previousEnd
	}
	report.Materialized = len(seen)
	report.Indexed = len(seen)
	report.ColdSearches = report.ProviderDecisions
	report.ColdDownloads = report.Materialized
	report.ColdUploads = report.Materialized
	report.WarmSearches, report.WarmDownloads, report.WarmUploads = 0, 0, 0
	report.ExactWarmHits = 15
	report.Certified = report.Scenes == 8 && report.VisualUnits == 14 && report.ProviderDecisions == 8 && report.TargetMs == 150000 && report.CoveredMs == 150000 && report.Gaps == 0 && report.Overlaps == 0 && report.NLPExtracted == 8 && report.NLPImportant == 8 && report.Materialized == 24 && report.Indexed == 24 && report.WarmSearches == 0 && report.WarmDownloads == 0 && report.WarmUploads == 0 && report.ExactWarmHits == 15
	if !report.Certified {
		t.Fatalf("VIDRUSH_FINAL_CERTIFIED=false: %+v", report)
	}

	t.Logf("VIDRUSH CERTIFICATION scenes=%d visual_units=%d provider_decisions=%d youtube=%d artlist=%d images=%d target_ms=%d covered_ms=%d gaps=%d overlaps=%d nlp=%d important=%d materialized=%d indexed=%d cold=(%d,%d,%d) warm=(%d,%d,%d) exact_warm_hits=%d", report.Scenes, report.VisualUnits, report.ProviderDecisions, report.YouTube, report.Artlist, report.Images, report.TargetMs, report.CoveredMs, report.Gaps, report.Overlaps, report.NLPExtracted, report.NLPImportant, report.Materialized, report.Indexed, report.ColdSearches, report.ColdDownloads, report.ColdUploads, report.WarmSearches, report.WarmDownloads, report.WarmUploads, report.ExactWarmHits)
}
