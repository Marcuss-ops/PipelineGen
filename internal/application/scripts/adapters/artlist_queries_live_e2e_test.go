package adapters_test

import (
	"context"
	"strings"
	"testing"

	adapters "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	localnlp "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/nlp/local"
)

type controlledArtlistSegment struct {
	id       string
	text     string
	expected []string
}

func controlledArtlistSegments() []controlledArtlistSegment {
	return []controlledArtlistSegment{
		{id: "segment-01", text: "A business team meets inside a modern office.", expected: []string{"business", "office"}},
		{id: "segment-02", text: "Aerial drone footage shows a city skyline at night.", expected: []string{"city", "skyline"}},
		{id: "segment-03", text: "Industrial robots automate production inside a factory.", expected: []string{"robots", "factory"}},
		{id: "segment-04", text: "Doctors use advanced technology inside a hospital.", expected: []string{"doctor", "hospital"}},
		{id: "segment-05", text: "Solar panels produce renewable energy on a rooftop.", expected: []string{"solar", "panels"}},
		{id: "segment-06", text: "A boxer trains intensely inside a professional gym.", expected: []string{"boxer", "gym"}},
		{id: "segment-07", text: "Basketball players practice on an indoor court.", expected: []string{"basketball", "court"}},
		{id: "segment-08", text: "A family cooks dinner together in a kitchen.", expected: []string{"family", "kitchen"}},
		{id: "segment-09", text: "Cybersecurity specialists monitor computers in a server room.", expected: []string{"cybersecurity", "server"}},
		{id: "segment-10", text: "Financial traders watch market screens in an office.", expected: []string{"financial", "traders"}},
		{id: "segment-11", text: "Construction workers build a structure at a site.", expected: []string{"construction", "workers"}},
		{id: "segment-12", text: "An electric car charges at a modern station.", expected: []string{"electric", "car"}},
		{id: "segment-13", text: "A scientist conducts research in a laboratory.", expected: []string{"scientist", "laboratory"}},
		{id: "segment-14", text: "Travelers walk through a busy airport terminal.", expected: []string{"airport", "terminal"}},
		{id: "segment-15", text: "A farmer drives a tractor across a field.", expected: []string{"farmer", "tractor"}},
		{id: "segment-16", text: "Ocean waves roll along a rocky coastline.", expected: []string{"ocean", "waves"}},
		{id: "segment-17", text: "Students learn together inside a classroom.", expected: []string{"students", "classroom"}},
		{id: "segment-18", text: "Workers sort packages in a warehouse logistics center.", expected: []string{"packages", "warehouse"}},
		{id: "segment-19", text: "A coffee shop barista prepares a cappuccino.", expected: []string{"coffee", "barista"}},
		{id: "segment-20", text: "Hikers climb a mountain during an outdoor adventure.", expected: []string{"hikers", "mountain"}},
	}
}

func TestLiveNLPArtlistQueriesAcrossTwentyControlledSegments(t *testing.T) {
	extractor := localnlp.NewHybridExtractor()
	enricher := adapters.NewVidRushSegmentEnricher(extractor, nil)
	plan := &scriptpkg.ResolvedGenerationPlan{
		Title:    "Artlist NLP query certification",
		Language: "en",
		Model:    "local",
		MediaPlan: mediadomain.MediaPlanSpec{
			Extraction: mediadomain.MediaExtractionPolicy{
				Enabled:                       true,
				Device:                        "cpu",
				MaxEntitiesPerSegment:         8,
				MaxImportantPhrasesPerSegment: 1,
				MaxImportantWordsPerSegment:   8,
				MaxArtlistQueriesPerSegment:   5,
			},
			ForceRefreshExtraction: true,
		},
	}

	segments := controlledArtlistSegments()
	if len(segments) != 20 {
		t.Fatalf("controlled segment count = %d, want 20", len(segments))
	}

	for index, segment := range segments {
		segment := segment
		t.Run(segment.id, func(t *testing.T) {
			result, err := enricher.Enrich(context.Background(), plan, scriptpkg.SpecScene{
				ID: segment.id, SegmentID: segment.id, Index: index, Text: segment.text,
			})
			if err != nil {
				t.Fatalf("NLP enrichment failed: %v", err)
			}
			if result.SegmentID != segment.id {
				t.Fatalf("segment identity = %q, want %q", result.SegmentID, segment.id)
			}
			if len(result.Insights.ArtlistQueries) == 0 {
				t.Fatalf("NLP produced no ArtlistQueries for %q", segment.text)
			}

			haystack := strings.ToLower(strings.Join(result.Insights.ArtlistQueries, " "))
			hits := 0
			for _, expected := range segment.expected {
				if strings.Contains(haystack, strings.ToLower(expected)) {
					hits++
				}
			}
			if hits < 2 {
				t.Fatalf("semantic query mismatch: queries=%q expected at least 2 of %v, hits=%d", result.Insights.ArtlistQueries, segment.expected, hits)
			}
			t.Logf("query=%q expected=%v hits=%d/2", result.Insights.ArtlistQueries, segment.expected, hits)
		})
	}
}
