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
