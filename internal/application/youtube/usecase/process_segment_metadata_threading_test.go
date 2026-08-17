// Package usecase — process_segment_metadata_threading_test.go
//
// Pins the Step 10 / canonical-commit metadata threading: the caller's
// Segment.Summary / Segment.Tags / Segment.Topics / Segment.MentionedPeople
// MUST flow through ClipMetadataInput → the analyzer (request-provided
// survives) → CanonicalClipEnrichment → foldEnrichmentIntoClipAsset →
// ClipAsset.Metadata, never dropped at the door nor overwritten by the
// LLM-derived values. This is the regression guard for the METADATA
// THREADING BUG surfaced by the Di-Awl0XyQs certification.
package usecase

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	ytmetadata "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/metadata"
)

// llmStubBuilder is a ClipMetadataBuilder fake that returns a fully
// populated LLM-derived envelope whose semantic fields DIFFER from the
// caller-supplied values. The request-provided-survives contract must
// make the caller's values win.
type llmStubBuilder struct{}

func (llmStubBuilder) Build(_ context.Context, _ youtubetypes.ClipMetadataInput) (youtubetypes.CanonicalClipMetadata, error) {
	return youtubetypes.CanonicalClipMetadata{
		Summary:         "LLM-derived summary",
		Topics:          []string{"llm-topic"},
		Speakers:        []string{"llm-speaker"},
		MentionedPeople: []string{"llm-person"},
		Tags:            []string{"llm-merged-tag"},
		QualityScore:    0.9,
	}, nil
}

// newThreadingMetadataService builds a real *ytmetadata.MetadataService
// wired to llmStubBuilder (LLM values) + a no-op writer. The service is
// the SAME concrete analyzer the use case consumes at runtime, so the
// test exercises the full analyzeClipForCommit path.
func newThreadingMetadataService(t *testing.T) *ytmetadata.MetadataService {
	t.Helper()
	svc, err := ytmetadata.NewMetadataService(ytmetadata.MetadataDeps{
		Builder: llmStubBuilder{},
		Writer:  noopMetadataWriter{},
	})
	require.NoError(t, err, "NewMetadataService must succeed with llmStubBuilder + noopMetadataWriter")
	require.NotNil(t, svc)
	return svc
}

// threadingSegmentCommand builds the canonical ProcessSegmentCommand with
// request-provided semantic fields on the Segment.
func threadingSegmentCommand() youtubetypes.ProcessSegmentCommand {
	return youtubetypes.ProcessSegmentCommand{
		VideoID:  "abc",
		VideoURL: "https://www.youtube.com/watch?v=abc",
		Segment: youtubetypes.Segment{
			Name:            "Input Name",
			Summary:         "input summary",
			Tags:            []string{"tag-a", "tag-b"},
			Topics:          []string{"input-topic"},
			Speakers:        []string{"input-speaker"},
			MentionedPeople: []string{"input-person"},
		},
	}
}

// TestAnalyzeClipForCommit_ThreadsRequestProvidedSemanticFields pins the
// first half of the chain: analyzeClipForCommit must thread the Segment's
// Summary/Tags/Topics/Speakers/MentionedPeople into ClipMetadataInput and
// the returned enrichment must carry the INPUT values (not the LLM's).
func TestAnalyzeClipForCommit_ThreadsRequestProvidedSemanticFields(t *testing.T) {
	t.Parallel()

	core, media, metadata, observability := validProcessSegmentDeps()
	metadata.MetadataService = newThreadingMetadataService(t)
	uc := NewProcessYouTubeSegmentFromSubBundles(core, media, metadata, observability)

	cmd := threadingSegmentCommand()
	enrichment, err := uc.analyzeClipForCommit(context.Background(), cmd, "yt_abc_0_60_v1", 0, 60, nil)
	require.NoError(t, err, "analyzeClipForCommit must succeed with a wired analyzer")

	require.Equal(t, "input summary", enrichment.Summary, "enrichment.Summary must carry the input summary, not the LLM value")
	require.Equal(t, []string{"tag-a", "tag-b"}, enrichment.Tags, "enrichment.Tags must carry the input tags verbatim")
	require.Equal(t, []string{"input-topic"}, enrichment.Topics, "enrichment.Topics must carry the input topics")
	require.Equal(t, []string{"input-speaker"}, enrichment.Speakers, "enrichment.Speakers must carry the input speakers")
	require.Equal(t, []string{"input-person"}, enrichment.MentionedPeople, "enrichment.MentionedPeople must carry the input mentioned people")
}

// TestFoldEnrichmentIntoClipAsset_RequestProvidedFieldsLandInMetadata pins
// the second half of the chain: folding the analyzer enrichment into the
// canonical ClipAsset must persist the request-provided semantic fields in
// ClipAsset.Metadata and reflect them in the recomputed SearchText.
func TestFoldEnrichmentIntoClipAsset_RequestProvidedFieldsLandInMetadata(t *testing.T) {
	t.Parallel()

	core, media, metadata, observability := validProcessSegmentDeps()
	metadata.MetadataService = newThreadingMetadataService(t)
	uc := NewProcessYouTubeSegmentFromSubBundles(core, media, metadata, observability)

	cmd := threadingSegmentCommand()
	const clipID = "yt_abc_0_60_v1"
	enrichment, err := uc.analyzeClipForCommit(context.Background(), cmd, clipID, 0, 60, nil)
	require.NoError(t, err)

	// buildClipAsset already threads Segment.Summary/Tags/Topics/Speakers/
	// MentionedPeople into Metadata (process_segment_helpers.go); the fold
	// must preserve them (request-provided wins over the LLM enrichment).
	out := youtubetypes.ProcessSegmentResult{
		Item: youtubetypes.ExtractItem{
			StartSeconds: 0,
			EndSeconds:   60,
			Duration:     60,
			LocalPath:    "/tmp/clip.mp4",
		},
	}
	asset := buildClipAsset(clipID, cmd, out, "sha256:content", "v1")
	asset = foldEnrichmentIntoClipAsset(asset, enrichment)

	require.Equal(t, "input summary", asset.Metadata.Summary, "ClipAsset.Metadata.Summary must carry the input summary")
	require.Equal(t, []string{"tag-a", "tag-b"}, asset.Metadata.Tags, "ClipAsset.Metadata.Tags must carry the input tags verbatim")
	require.Equal(t, []string{"input-topic"}, asset.Metadata.Topics, "ClipAsset.Metadata.Topics must carry the input topics")
	require.Equal(t, []string{"input-speaker"}, asset.Metadata.Speakers, "ClipAsset.Metadata.Speakers must carry the input speakers")
	require.Equal(t, []string{"input-person"}, asset.Metadata.MentionedPeople, "ClipAsset.Metadata.MentionedPeople must carry the input mentioned people")

	// The recomputed search text must surface the threaded fields so the
	// semantic retrieval path can match them (e.g. "backstage").
	require.Contains(t, asset.SearchText, "input summary", "SearchText must include the threaded summary")
	require.Contains(t, asset.SearchText, "input-topic", "SearchText must include the threaded topics")
	require.Contains(t, asset.SearchText, "input-speaker", "SearchText must include the threaded speakers")
	require.Contains(t, asset.SearchText, "input-person", "SearchText must include the threaded mentioned people")
}
