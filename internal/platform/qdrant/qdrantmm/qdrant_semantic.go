// Package qdrantmm — qdrant_semantic.go is the canonical concrete
// mediamemory.SemanticLookup that bridges the mediamemory
// capability to the existing Qdrant semantic stack.
//
// godlike/06 SSOT (one canonical owner per fact): the semantic
// adapter owns the concept → asset projection. The Qdrant
// pipelinegen_media_concepts collection ships concept_ids;
// the canonical MediaBindingRepository owns the concept_id →
// media_assets mapping. The adapter joins these two stores
// with a SINGLE batched round-trip via ListApprovedByConcepts.
//
// godlike/07 NO-FAKE-AVAILABILITY: a nil dependency at any
// slot is a literal ErrSemanticNotConfigured. A HybridSearch
// envelope error surfaces as wrapped ErrSemanticBackendFailed.
package qdrantmm

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/application/search"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
)

const (
	conceptPayloadConceptID   = "concept_id"
	conceptPayloadLanguage    = "language"
	conceptPayloadConceptType = "concept_type"
)

// QdrantSemanticLookup is the canonical concrete for
// mediamemory.SemanticLookup.
type QdrantSemanticLookup struct {
	client      *transport.Client
	embedder    search.EmbeddingChannelRegistry
	concepts    mediamemory.ConceptRepository
	bindings    mediamemory.BindingRepository
	log         *zap.Logger
	vectorLimit int
}

// NewQdrantSemanticLookup constructs the canonical adapter.
func NewQdrantSemanticLookup(
	client *transport.Client,
	embedder search.EmbeddingChannelRegistry,
	concepts mediamemory.ConceptRepository,
	bindings mediamemory.BindingRepository,
	log *zap.Logger,
) *QdrantSemanticLookup {
	return &QdrantSemanticLookup{
		client:      client,
		embedder:    embedder,
		concepts:    concepts,
		bindings:    bindings,
		log:         log,
		vectorLimit: 64,
	}
}

// Compile-time assertion.
var _ mediamemory.SemanticLookup = (*QdrantSemanticLookup)(nil)

// LookupByConcept performs a Qdrant HybridSearch over
// pipelinegen_media_concepts and projects each hit to a
// MediaCandidate[] populated from the canonical binding graph.
func (s *QdrantSemanticLookup) LookupByConcept(
	ctx context.Context,
	conceptType mediamemory.ConceptType,
	text string,
	language string,
	limit int,
) ([]mediamemory.MediaCandidate, error) {
	if s == nil || s.client == nil || s.embedder == nil || s.concepts == nil || s.bindings == nil {
		return nil, fmt.Errorf("mediamemory: QdrantSemanticLookup not wired: %w",
			mediamemory.ErrSemanticNotConfigured)
	}
	if text == "" || language == "" {
		return nil, fmt.Errorf("mediamemory: LookupByConcept text + language required: %w",
			mediamemory.ErrInvalidPhrase)
	}
	if limit <= 0 {
		limit = s.vectorLimit
	}

	dense, err := s.embedder.EmbedQuery(ctx, search.ChannelText, text)
	if err != nil {
		if errors.Is(err, search.ErrChannelNotConfigured) {
			return nil, fmt.Errorf("mediamemory: text-channel encoder not configured: %w",
				mediamemory.ErrSemanticNotConfigured)
		}
		return nil, fmt.Errorf("mediamemory: QdrantSemanticLookup embed: %w",
			mediamemory.ErrSemanticBackendFailed)
	}
	if len(dense) == 0 {
		return nil, nil
	}

	filterMust := []map[string]any{
		{"key": conceptPayloadLanguage, "match": map[string]any{"value": language}},
	}
	if mediamemory.IsKnownConceptType(conceptType) {
		filterMust = append(filterMust, map[string]any{
			"key":   conceptPayloadConceptType,
			"match": map[string]any{"value": string(conceptType)},
		})
	}
	filter := map[string]any{"must": filterMust}

	hits, err := s.client.HybridSearchPoints(ctx, schema.ConceptCollectionName, schema.HybridSearchRequest{
		DenseVector:      dense,
		DenseVectorName:  search.ChannelText,
		SparseVectorName: search.ChannelSparse,
		SparseText:       text,
		SparseModel:      schema.DefaultSparseModel,
		Limit:            limit,
		Filter:           filter,
	})
	if err != nil {
		return nil, fmt.Errorf("mediamemory: hybrid search: %w",
			mediamemory.ErrSemanticBackendFailed)
	}
	if len(hits) == 0 {
		return nil, nil
	}

	hitConceptIDs := make([]string, 0, len(hits))
	for _, hit := range hits {
		if cid := payloadStringField(hit.Payload, conceptPayloadConceptID); cid != "" {
			hitConceptIDs = append(hitConceptIDs, cid)
		}
	}
	if len(hitConceptIDs) == 0 {
		return nil, nil
	}

	bindingMap, berr := s.bindings.ListApprovedByConcepts(ctx, hitConceptIDs, nil, 1)
	if berr != nil {
		return nil, fmt.Errorf("mediamemory: batched bindings: %w",
			mediamemory.ErrSemanticBackendFailed)
	}

	out := make([]mediamemory.MediaCandidate, 0, len(hits))
	for _, hit := range hits {
		conceptID := payloadStringField(hit.Payload, conceptPayloadConceptID)
		if conceptID == "" {
			continue
		}
		if mediamemory.IsKnownConceptType(conceptType) {
			payed := mediamemory.ConceptType(payloadStringField(hit.Payload, conceptPayloadConceptType))
			if payed != "" && payed != conceptType {
				continue
			}
		}
		conceptBindings, ok := bindingMap[conceptID]
		if !ok || len(conceptBindings) == 0 {
			continue
		}
		for _, b := range conceptBindings {
			if b.AssetID == "" {
				continue
			}
			out = append(out, mediamemory.MediaCandidate{
				Provider:              mediamemory.ProviderSemanticIndex,
				MediaType:             "",
				CandidateScore:        hit.Score,
				DiscoveryStatus:       mediamemory.DiscoveryIndexed,
				MaterializationStatus: mediamemory.MaterializationCold,
				AssetID:               b.AssetID,
			})
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// payloadStringField reads a string-typed payload field.
func payloadStringField(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	v, ok := payload[key]
	if !ok {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
