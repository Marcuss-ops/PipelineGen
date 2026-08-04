// Package adapters — compat_adapters_test.go (PR-noop-adapters-purge, 2026-07-25).
//
// TDD-first contract tests for the typed-fail adapter pattern that
// replaces the noopEntityExtractionAdapter + noopMetadataGenerationAdapter
// (PR 3, May 2026). The new pattern honours godlike/07 NO-FAKE-AVAILABILITY:
// an unwired backend returns a typed error sentinel that callers can probe
// via errors.Is, NOT a successful empty payload.
//
// Per godlike/06 SSOT one-canonical-owner-per-fact: EntityExtractor +
// MetadataGenerator ports live ONLY in compat_adapters.go (= this package).
// The dto/compat_types.go duplicate was retired in this PR.
package adapters

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// TestUnavailableEntityExtractionAdapter_ReturnsTypedSentinel pins the
// godlike/07 fail-closed contract for the entity-extraction typed-fail
// adapter. NewUnavailableEntityExtractionAdapter returns an adapter whose
// every ExtractEntities call returns ErrEntityExtractorUnavailable — even
// when the request is well-formed. errors.Is probe confirms the typed
// sentinel is reachable.
func TestUnavailableEntityExtractionAdapter_ReturnsTypedSentinel(t *testing.T) {
	adapter := NewUnavailableEntityExtractionAdapter()
	if adapter == nil {
		t.Fatal("NewUnavailableEntityExtractionAdapter returned nil adapter")
	}

	req := scriptpkg.EntityExtractionRequest{
		Text:     "Test script body",
		Title:    "Test Title",
		Language: "en",
		Model:    "test-model",
	}
	got, err := adapter.ExtractEntities(context.Background(), req)

	if got != nil {
		t.Errorf("expected nil *EntityResult on typed-fail, got %+v", got)
	}
	if err == nil {
		t.Fatal("expected ErrEntityExtractorUnavailable, got nil error")
	}
	if !errors.Is(err, ErrEntityExtractorUnavailable) {
		t.Fatalf("expected ErrEntityExtractorUnavailable via errors.Is, got: %v", err)
	}
}

// TestUnavailableMetadataGenerationAdapter_ReturnsTypedSentinel pins the
// godlike/07 fail-closed contract for the metadata-generation typed-fail
// adapter. NewUnavailableMetadataGenerationAdapter returns an adapter
// whose every GenerateMetadata call returns ErrMetadataGeneratorUnavailable.
// errors.Is probe confirms the typed sentinel is reachable.
func TestUnavailableMetadataGenerationAdapter_ReturnsTypedSentinel(t *testing.T) {
	adapter := NewUnavailableMetadataGenerationAdapter()
	if adapter == nil {
		t.Fatal("NewUnavailableMetadataGenerationAdapter returned nil adapter")
	}

	req := scriptpkg.MetadataGenerationRequest{
		Text:     "Test script body",
		Title:    "Test Title",
		Language: "en",
		Model:    "test-model",
	}
	got, err := adapter.GenerateMetadata(context.Background(), req)

	if got != nil {
		t.Errorf("expected nil []VideoMetadata on typed-fail, got %+v", got)
	}
	if err == nil {
		t.Fatal("expected ErrMetadataGeneratorUnavailable, got nil error")
	}
	if !errors.Is(err, ErrMetadataGeneratorUnavailable) {
		t.Fatalf("expected ErrMetadataGeneratorUnavailable via errors.Is, got: %v", err)
	}
}

// TestEntitiesProcessor_NilExtractor_ReturnsErrPostprocessFailed pins
// the EXISTING processor-level nil-check contract (ProcessorRequired
// posture per PR 3). Refactoring the outer typed-fail adapter MUST NOT
// regress this internal guard. The processor continues to wrap
// ErrPostprocessFailed for nil-extractor at PROCESS time.
func TestEntitiesProcessor_NilExtractor_ReturnsErrPostprocessFailed(t *testing.T) {
	p := NewEntitiesProcessor(nil)
	if p == nil {
		t.Fatal("NewEntitiesProcessor(nil) returned nil processor")
	}

	plan := &scriptpkg.ResolvedGenerationPlan{
		Title:    "Test",
		Language: "en",
		Model:    "test-model",
	}
	input := ProcessInput{
		Text: "Test script body",
	}

	got, err := p.Process(context.Background(), plan, input)

	if got != nil {
		t.Errorf("expected nil PostProcessResult on nil-extractor, got %+v", got)
	}
	if err == nil {
		t.Fatal("expected nil-extractor error, got nil")
	}
	if !errors.Is(err, scriptpkg.ErrPostprocessFailed) {
		t.Fatalf("expected ErrPostprocessFailed wrap, got: %v", err)
	}
}

// TestMetadataProcessor_NilGenerator_ReturnsErrPostprocessFailed mirrors
// the entities nil-check pin. See TestEntitiesProcessor_NilExtractor_ReturnsErrPostprocessFailed.
func TestMetadataProcessor_NilGenerator_ReturnsErrPostprocessFailed(t *testing.T) {
	p := NewMetadataProcessor(nil)
	if p == nil {
		t.Fatal("NewMetadataProcessor(nil) returned nil processor")
	}

	plan := &scriptpkg.ResolvedGenerationPlan{Title: "Test", Language: "en", Model: "test-model"}
	input := ProcessInput{Text: "Test script body"}

	got, err := p.Process(context.Background(), plan, input)

	if got != nil {
		t.Errorf("expected nil PostProcessResult on nil-generator, got %+v", got)
	}
	if err == nil {
		t.Fatal("expected nil-generator error, got nil")
	}
	if !errors.Is(err, scriptpkg.ErrPostprocessFailed) {
		t.Fatalf("expected ErrPostprocessFailed wrap, got: %v", err)
	}
}

type countingMetadataGenerator struct {
	calls int
}

func (g *countingMetadataGenerator) GenerateMetadata(
	ctx context.Context,
	req scriptpkg.MetadataGenerationRequest,
) ([]scriptpkg.VideoMetadata, error) {
	g.calls++
	return nil, errors.New("must not be called")
}

func TestMetadataProcessor_ManualMetadata_WorksWithoutGenerator(t *testing.T) {
	t.Parallel()
	p := NewMetadataProcessor(nil)
	plan := &scriptpkg.ResolvedGenerationPlan{
		Language: "it",
		VideoMetadata: &scriptpkg.VideoMetadata{
			Title: "Titolo manuale",
		},
	}

	got, err := p.Process(context.Background(), plan, ProcessInput{})
	if err != nil {
		t.Fatalf("manual metadata should work without a generator: %v", err)
	}
	if got == nil || len(got.Metadata) != 1 {
		t.Fatalf("expected one manual metadata record, got %#v", got)
	}
	if got.Metadata[0].Language != "it" {
		t.Fatalf("manual metadata language = %q, want plan language %q", got.Metadata[0].Language, "it")
	}
	if got.Metadata[0].Title != "Titolo manuale" {
		t.Fatalf("manual metadata title = %q, want %q", got.Metadata[0].Title, "Titolo manuale")
	}
}

func TestMetadataProcessor_ManualMetadataWins(t *testing.T) {
	t.Parallel()
	generator := &countingMetadataGenerator{}
	p := NewMetadataProcessor(generator)

	inputTitle := "Il Combattimento Che Ha Cambiato Tutto"
	inputDescription := "In questo video analizziamo il combattimento..."
	inputTags := []string{"boxe", "combattimento"}

	plan := &scriptpkg.ResolvedGenerationPlan{
		Title:    "Fallback Title",
		Language: "it",
		Model:    "test-model",
		VideoMetadata: &scriptpkg.VideoMetadata{
			Title:       inputTitle,
			Description: inputDescription,
			Tags:        inputTags,
		},
	}
	input := ProcessInput{Text: "Test script body"}

	got, err := p.Process(context.Background(), plan, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if generator.calls != 0 {
		t.Fatalf("metadata generator called %d times; expected 0", generator.calls)
	}

	if got == nil {
		t.Fatal("expected non-nil PostProcessResult")
	}

	if len(got.Metadata) != 1 {
		t.Fatalf("expected exactly 1 metadata record, got %d", len(got.Metadata))
	}

	meta := got.Metadata[0]
	if meta.Title != inputTitle {
		t.Errorf("expected Title %q, got %q", inputTitle, meta.Title)
	}
	if meta.Description != inputDescription {
		t.Errorf("expected Description %q, got %q", inputDescription, meta.Description)
	}
	if len(meta.Tags) != len(inputTags) || meta.Tags[0] != inputTags[0] || meta.Tags[1] != inputTags[1] {
		t.Errorf("expected Tags %v, got %v", inputTags, meta.Tags)
	}
}

func TestMetadataProcessor_UsesAIFallbackWhenManualMetadataAbsent(t *testing.T) {
	t.Parallel()
	generator := &fallbackMetadataGenerator{}
	p := NewMetadataProcessor(generator)
	plan := &scriptpkg.ResolvedGenerationPlan{
		Title:    "Script title",
		Language: "en",
		Model:    "test-model",
	}

	got, err := p.Process(context.Background(), plan, ProcessInput{Text: "Script body"})
	if err != nil {
		t.Fatalf("AI fallback returned an error: %v", err)
	}
	if generator.calls != 1 {
		t.Fatalf("AI generator calls = %d, want 1", generator.calls)
	}
	if got == nil || len(got.Metadata) != 1 || got.Metadata[0].Title != "AI title" {
		t.Fatalf("unexpected AI fallback result: %#v", got)
	}
}

func TestMetadataProcessor_NoManualMetadataWithoutGeneratorFailsClosed(t *testing.T) {
	t.Parallel()
	p := NewMetadataProcessor(nil)
	plan := &scriptpkg.ResolvedGenerationPlan{
		Title:    "Script title",
		Language: "en",
	}

	got, err := p.Process(context.Background(), plan, ProcessInput{Text: "Script body"})
	if got != nil {
		t.Fatalf("expected nil result on missing AI backend, got %#v", got)
	}
	if !errors.Is(err, scriptpkg.ErrPostprocessFailed) {
		t.Fatalf("expected ErrPostprocessFailed, got %v", err)
	}
}

type fallbackMetadataGenerator struct {
	calls int
}

func (g *fallbackMetadataGenerator) GenerateMetadata(
	context.Context,
	scriptpkg.MetadataGenerationRequest,
) ([]scriptpkg.VideoMetadata, error) {
	g.calls++
	return []scriptpkg.VideoMetadata{{Language: "en", Title: "AI title"}}, nil
}

type successfulMetadataGenerator struct {
	calls int
}

func (g *successfulMetadataGenerator) GenerateMetadata(
	ctx context.Context,
	req scriptpkg.MetadataGenerationRequest,
) ([]scriptpkg.VideoMetadata, error) {
	g.calls++

	return []scriptpkg.VideoMetadata{
		{
			Language:    "it",
			Title:       "Titolo generato",
			Description: "Descrizione generata",
			Tags:        []string{"generato"},
		},
	}, nil
}

func TestMetadataProcessor_ManualMetadataBypassesGenerator(t *testing.T) {
	generator := &countingMetadataGenerator{}
	processor := NewMetadataProcessor(generator)

	plan := &scriptpkg.ResolvedGenerationPlan{
		Language: "it",
		VideoMetadata: &scriptpkg.VideoMetadata{
			Language:    "it",
			Title:       "Titolo inserito da me",
			Description: "Descrizione inserita da me",
			Tags:        []string{"tag1", "tag2"},
		},
	}

	result, err := processor.Process(
		context.Background(),
		plan,
		ProcessInput{
			Text: "Testo dello script",
		},
	)

	require.NoError(t, err)
	require.Equal(t, 0, generator.calls)
	require.Len(t, result.Metadata, 1)

	actual := result.Metadata[0]
	require.Equal(t, "Titolo inserito da me", actual.Title)
	require.Equal(t, "Descrizione inserita da me", actual.Description)
	require.Equal(t, []string{"tag1", "tag2"}, actual.Tags)
}

func TestMetadataProcessor_NoManualMetadataUsesGenerator(t *testing.T) {
	generator := &successfulMetadataGenerator{}
	processor := NewMetadataProcessor(generator)

	result, err := processor.Process(
		context.Background(),
		&scriptpkg.ResolvedGenerationPlan{
			Title:    "Mike Tyson",
			Language: "it",
		},
		ProcessInput{
			Text: "Testo dello script",
		},
	)

	require.NoError(t, err)
	require.Equal(t, 1, generator.calls)
	require.Len(t, result.Metadata, 1)
	require.Equal(t, "Titolo generato", result.Metadata[0].Title)
}

func TestMetadataProcessor_ManualMetadataWorksWithoutAIBackend(t *testing.T) {
	processor := NewMetadataProcessor(nil)

	result, err := processor.Process(
		context.Background(),
		&scriptpkg.ResolvedGenerationPlan{
			Language: "it",
			VideoMetadata: &scriptpkg.VideoMetadata{
				Title: "Titolo manuale",
			},
		},
		ProcessInput{},
	)

	require.NoError(t, err)
	require.Len(t, result.Metadata, 1)
	require.Equal(t, "Titolo manuale", result.Metadata[0].Title)
}

func TestMetadataProcessor_AIRequestedWithoutBackendFails(t *testing.T) {
	processor := NewMetadataProcessor(nil)

	_, err := processor.Process(
		context.Background(),
		&scriptpkg.ResolvedGenerationPlan{
			Title:    "Titolo",
			Language: "it",
		},
		ProcessInput{
			Text: "Testo",
		},
	)

	require.Error(t, err)
}
