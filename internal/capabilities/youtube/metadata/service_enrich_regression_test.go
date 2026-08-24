// Package metadata — service_enrich_regression_test.go
//
// Pins the godlike/06 "request-provided-survives" contract that closes the
// METADATA THREADING BUG: the transcript-only LLM builder must NEVER
// overwrite the caller's Summary / Tags / Topics / Speakers /
// MentionedPeople with derived values. Before this contract, the caller's
// summary and tags were structurally dropped (ClipMetadataInput had no
// Summary/Tags fields) and the LLM-derived topics/speakers/mentioned-people
// silently replaced the input lists. These tests lock the repaired boundary
// so a future regressions surfaces immediately.
package metadata

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
)

// TestGenerateClipMetadata_RequestProvidedFieldsSurviveLLMEnrichment pins
// the LLM path: the builder returns a fully-populated (LLM-derived)
// envelope, but every caller-supplied semantic field must win.
func TestGenerateClipMetadata_RequestProvidedFieldsSurviveLLMEnrichment(t *testing.T) {
	t.Parallel()

	b := &stubBuilder{
		out: youtubetypes.CanonicalClipMetadata{
			Summary:         "LLM-derived summary",
			Topics:          []string{"llm-topic"},
			Speakers:        []string{"llm-speaker"},
			MentionedPeople: []string{"llm-person"},
			Tags:            []string{"llm-merged-tag"},
			QualityScore:    0.9,
		},
	}
	svc, err := NewMetadataService(MetadataDeps{Builder: b, Writer: &stubWriter{}})
	require.NoError(t, err, "NewMetadataService must succeed with stub builder + stub writer")

	in := youtubetypes.ClipMetadataInput{
		ClipID:          "yt_abc_0_60_v1",
		Title:           "Input Title",
		Summary:         "input summary",
		Tags:            []string{"tag-a", "tag-b"},
		Topics:          []string{"input-topic"},
		Speakers:        []string{"input-speaker"},
		MentionedPeople: []string{"input-person"},
		ClipDuration:    60,
	}

	out, err := svc.GenerateClipMetadata(context.Background(), in)
	require.NoError(t, err, "GenerateClipMetadata must succeed for a valid ClipID")

	require.Equal(t, "input summary", out.Summary, "request-provided Summary must survive LLM enrichment (not %q)", out.Summary)
	require.Equal(t, []string{"tag-a", "tag-b"}, out.Tags, "request-provided Tags must survive LLM enrichment verbatim")
	require.Equal(t, []string{"input-topic"}, out.Topics, "request-provided Topics must survive LLM enrichment")
	require.Equal(t, []string{"input-speaker"}, out.Speakers, "request-provided Speakers must survive LLM enrichment")
	require.Equal(t, []string{"input-person"}, out.MentionedPeople, "request-provided MentionedPeople must survive LLM enrichment")
}

// TestGenerateClipMetadata_RequestProvidedFieldsFillLLMGaps pins the
// gap-fill half of the contract: when the builder leaves a field empty,
// the caller-supplied value still lands (empty LLM fields do not wipe
// the input).
func TestGenerateClipMetadata_RequestProvidedFieldsFillLLMGaps(t *testing.T) {
	t.Parallel()

	b := &stubBuilder{
		out: youtubetypes.CanonicalClipMetadata{
			Summary:      "LLM-derived summary", // non-empty so the fallback path is not taken
			QualityScore: 0.8,
			// Topics / Speakers / MentionedPeople / Tags left empty.
		},
	}
	svc, err := NewMetadataService(MetadataDeps{Builder: b, Writer: &stubWriter{}})
	require.NoError(t, err)

	in := youtubetypes.ClipMetadataInput{
		ClipID:          "yt_abc_0_60_v1",
		Title:           "Input Title",
		Summary:         "input summary",
		Tags:            []string{"tag-a"},
		Topics:          []string{"input-topic"},
		Speakers:        []string{"input-speaker"},
		MentionedPeople: []string{"input-person"},
		ClipDuration:    60,
	}

	out, err := svc.GenerateClipMetadata(context.Background(), in)
	require.NoError(t, err)

	require.Equal(t, []string{"tag-a"}, out.Tags)
	require.Equal(t, []string{"input-topic"}, out.Topics)
	require.Equal(t, []string{"input-speaker"}, out.Speakers)
	require.Equal(t, []string{"input-person"}, out.MentionedPeople)
}

// TestFallbackMetadata_CarriesRequestProvidedFieldsVerbatim pins the
// deterministic (non-LLM) path: it must thread the caller's Summary /
// Tags / Topics / Speakers / MentionedPeople verbatim instead of only
// the Title-derived summary.
func TestFallbackMetadata_CarriesRequestProvidedFieldsVerbatim(t *testing.T) {
	t.Parallel()

	out := FallbackMetadata(youtubetypes.ClipMetadataInput{
		ClipID:          "yt_abc_0_60_v1",
		Title:           "Title",
		Summary:         "input summary",
		Tags:            []string{"tag-a", "tag-b"},
		Topics:          []string{"input-topic"},
		Speakers:        []string{"input-speaker"},
		MentionedPeople: []string{"input-person"},
		ClipDuration:    60,
	})

	require.Equal(t, "input summary", out.Summary, "FallbackMetadata must prefer the request-provided Summary over the Title fallback")
	require.Equal(t, []string{"tag-a", "tag-b"}, out.Tags)
	require.Equal(t, []string{"input-topic"}, out.Topics)
	require.Equal(t, []string{"input-speaker"}, out.Speakers)
	require.Equal(t, []string{"input-person"}, out.MentionedPeople)
}
