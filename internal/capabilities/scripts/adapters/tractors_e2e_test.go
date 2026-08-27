package adapters

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	scriptgeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type tractorE2EEnricher struct{}

func (tractorE2EEnricher) Enrich(_ context.Context, _ *scriptpkg.ResolvedGenerationPlan, scene scriptpkg.SpecScene) (scriptpkg.VidRushSegmentResult, error) {
	profile := scriptpkg.SegmentSemanticProfile{
		SegmentID: scene.ID, TextHash: scriptgeneration.SceneTextHash(scene.Text), Topic: "origine dei primi trattori",
		Subtopics:   []string{"macchine agricole", "motori a vapore"},
		Keywords:    []scriptpkg.WeightedKeyword{{Value: "trattori", Confidence: .98}, {Value: "agricoltura", Confidence: .86}},
		VisualTerms: []scriptpkg.WeightedKeyword{{Value: "steam tractor farm field", Confidence: .92}},
		Entities:    []scriptpkg.ExtractedEntity{{Value: "John Froelich", Type: "PERSON", Confidence: .99}, {Value: "Iowa", Type: "PLACE", Confidence: .96}, {Value: "1892", Type: "DATE", Confidence: .99}},
	}
	profile.Retrieval = &scriptpkg.RetrievalIntent{
		YouTube: []string{"John Froelich first gasoline tractor 1892"}, Artlist: []string{"steam tractor farm field", "vintage agricultural machinery"}, Images: []string{"John Froelich", "1892 gasoline tractor"},
	}
	return scriptpkg.VidRushSegmentResult{SegmentID: scene.ID, SceneID: scene.ID, Position: scene.Index, Text: scene.Text, TextHash: profile.TextHash, Insights: scriptpkg.SegmentInsights{
		SegmentID: scene.ID, TextHash: profile.TextHash, Entities: profile.Entities, ImportantWords: []string{"trattori", "agricoltura"}, ImportantPhrases: []string{"origine dei primi trattori"}, YouTubeQueries: profile.Retrieval.YouTube, ArtlistQueries: profile.Retrieval.Artlist, ImageQueries: profile.Retrieval.Images,
	}}, nil
}

type tractorE2EResolver struct {
	mu    sync.Mutex
	calls int
}

func (r *tractorE2EResolver) ResolveProviders(_ context.Context, _ *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult) (scriptpkg.VidRushSegmentResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	segment.Assets.Candidates = append(segment.Assets.Candidates, scriptpkg.SegmentAssetCandidate{AssetID: "remote-asset", Provider: scriptpkg.VidRushProviderArtlist, Query: segment.Insights.ArtlistQueries[0]})
	return segment, nil
}

type tractorE2EMaterializer struct{ calls int }

func (m *tractorE2EMaterializer) Materialize(_ context.Context, _ *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult) (scriptpkg.VidRushSegmentResult, error) {
	m.calls++
	for i := range segment.Assets.Candidates {
		c := &segment.Assets.Candidates[i]
		c.DriveLink = "drive://" + c.AssetID
		c.AcquisitionStatus = scriptpkg.VidRushStatusAcquired
		c.VerificationStatus = scriptpkg.VidRushStatusVerified
		c.PersistenceStatus = scriptpkg.VidRushStatusPersisted
		c.IndexStatus = scriptpkg.VidRushStatusIndexed
		c.RightsStatus = "verified"
	}
	return segment, nil
}

type tractorE2ECatalog struct{ calls int }

func (c *tractorE2ECatalog) SearchAssets(_ context.Context, q assetsearch.AssetSearchQuery) ([]assetsearch.AssetSearchHit, error) {
	c.calls++
	return []assetsearch.AssetSearchHit{{AssetID: "tractor-indexed-001", Source: "stock", Score: .97}}, nil
}

func TestTractorSegmentsEndToEndProfileQueriesAndIndexedReuse(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{Title: "Quando sono stati inventati i trattori?", Language: "it", Model: "gemma3:1b", PromptVersion: "segment-v1", MediaPlan: mediadomain.MediaPlanSpec{ProviderPolicy: mediadomain.MediaProviderPolicy{YouTube: mediadomain.MediaToggleEnabled, Artlist: mediadomain.MediaToggleEnabled, InternetImages: mediadomain.MediaToggleEnabled}}}
	coordinator := scriptgeneration.NewVidRushIncrementalCoordinator(tractorE2EEnricher{}, plan, 2)
	remote := &tractorE2EResolver{}
	materializer := &tractorE2EMaterializer{}
	coordinator.SetSegmentProviderResolver(remote)
	coordinator.SetSegmentMaterializer(materializer)
	text := "Alla fine del XIX secolo iniziarono ad apparire le prime macchine agricole a vapore."
	require.NoError(t, coordinator.OnSceneCommitted(context.Background(), scriptgeneration.SceneCommitted{RunID: "tractor-run", SceneID: "segment-002", SceneIndex: 1, Text: text, TextHash: scriptgeneration.SceneTextHash(text), Revision: 1, Language: "it"}))
	results, err := coordinator.WaitForVidRush(context.Background(), "tractor-run")
	require.NoError(t, err)
	require.Len(t, results, 1)
	result := results[0]
	require.Len(t, result.Insights.Entities, 3)
	require.Contains(t, result.Insights.YouTubeQueries, "John Froelich first gasoline tractor 1892")
	require.Contains(t, result.Insights.ArtlistQueries, "steam tractor farm field")
	require.Contains(t, result.Insights.ImageQueries, "John Froelich")
	require.Equal(t, 1, remote.calls)
	require.Equal(t, 1, materializer.calls)

	catalog := &tractorE2ECatalog{}
	candidate, err := (VidRushSourceResolver{Canonical: catalog}).Resolve(context.Background(), SourceResolutionRequest{Plan: plan, Segment: scriptpkg.CanonicalSegment{ID: result.SegmentID, Text: result.Text}, Slot: mediadomain.SlotPrimaryVideo, Profile: scriptpkg.SegmentSemanticProfile{Topic: result.Insights.ImportantPhrases[0], Entities: result.Insights.Entities}})
	require.NoError(t, err)
	require.Equal(t, "canonical_stock", candidate.Stage)
	require.Equal(t, "tractor-indexed-001", candidate.Source.AssetID)
	require.Equal(t, 1, catalog.calls)
	// The canonical catalog hit terminates resolution before any remote source.
	require.Equal(t, 1, remote.calls)
}
