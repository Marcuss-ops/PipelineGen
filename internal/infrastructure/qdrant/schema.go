package qdrant

import (
	"fmt"
	"strings"
)

// DefaultV3Schema returns the canonical v3 index schema.
//
// v3 represents the QDRANT-003 schema with real embedding models:
//   - text: multilingual-e5-base, 768 dims, Cosine, normalized
//   - transcript: same model, distinct vector name
//   - visual: SigLIP so400m patch14-384, 768 dims, Cosine, normalized (real model, no fake)
//   - audio: CLAP HTSAT, 512 dims, Cosine (optional, only when model available)
//   - bm25_text: BM25 sparse vector for lexical search
func DefaultV3Schema() *IndexSchema {
	return &IndexSchema{
		Version:      "v3",
		PhysicalName: "media_assets_v3_e5_768_siglip_768",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []EmbeddingSpec{
			{
				Channel:       "text",
				Model:         "multilingual-e5-base",
				ModelVersion:  "2026-06-16-v1",
				Dimensions:    768,
				Distance:      "Cosine",
				Normalized:    true,
				QueryPrefix:   "query: ",
				IndexPrefix:   "passage: ",
				PreprocessVer: "v1",
			},
			{
				Channel:       "transcript",
				Model:         "multilingual-e5-base",
				ModelVersion:  "2026-06-16-v1",
				Dimensions:    768,
				Distance:      "Cosine",
				Normalized:    true,
				QueryPrefix:   "query: ",
				IndexPrefix:   "passage: ",
				PreprocessVer: "v1-transcript",
			},
			{
				Channel:      "visual",
				Model:        "siglip-so400m-patch14-384",
				ModelVersion: "2026-06-16-v1",
				Dimensions:   768,
				Distance:     "Cosine",
				Normalized:   true,
			},
			{
				Channel:      "audio",
				Model:        "clap-htsat-fused",
				ModelVersion: "2026-06-16-v1",
				Dimensions:   512,
				Distance:     "Cosine",
				Normalized:   true,
			},
		},
		SparseVectors: []SparseSpec{
			// PR2 (fix/qdrant-bm25-indexing): Model name is now
			// explicit. Pre-PR2 the spec only carried Modifier, which
			// made Qdrant silently reject sparse_vectors creation
			// because server-side BM25 inference requires a model
			// pinned in the channel config. DefaultSparseModel is the
			// single source of truth for the inference model name.
			{Channel: "bm25_text", Modifier: "bm25", Model: DefaultSparseModel},
		},		PayloadIndexes: []PayloadIndexSpec{
		{FieldName: "workspace_id", FieldType: "keyword"},
		// QDRANT-004 PR2 (June 2026): lifecycle_state is the SINGLE
		// canonical lifecycle key. The legacy `status` field is
		// forbidden — search_adapter, clip_search_adapter,
		// mediasearch.Service and BuildPayload all read/write this
		// name exclusively. A CI gate (rg 'payload\[..status..\]' over
		// qdrant infra, plus a reindex-qdrant startup assert)
		// keeps the drift window closed. Historical points are
		// mutated via cmd/admin/qdrant_backfill_lifecycle.go.
		{FieldName: "lifecycle_state", FieldType: "keyword"},
			{FieldName: "source", FieldType: "keyword"},
			{FieldName: "media_type", FieldType: "keyword"},
			{FieldName: "language", FieldType: "keyword"},
			{FieldName: "category", FieldType: "keyword"},
			{FieldName: "style", FieldType: "keyword"},
			{FieldName: "channel_id", FieldType: "keyword"},
			{FieldName: "license", FieldType: "keyword"},
			{FieldName: "index_version", FieldType: "keyword"},
			{FieldName: "embedding_version_text", FieldType: "keyword"},
			{FieldName: "embedding_version_visual", FieldType: "keyword"},
			{FieldName: "duration_ms", FieldType: "integer"},
			{FieldName: "created_at", FieldType: "datetime"},
			{FieldName: "updated_at", FieldType: "datetime"},
			{FieldName: "deleted_at", FieldType: "datetime"},
		},
	}
}

// Validate checks the IndexSchema for correctness.
// Returns nil if valid, or an error describing the first violation found.
func (s *IndexSchema) Validate() error {
	if s == nil {
		return fmt.Errorf("schema is nil")
	}
	if strings.TrimSpace(s.Version) == "" {
		return fmt.Errorf("schema version must not be empty")
	}
	if s.RuntimeAlias == "" {
		return fmt.Errorf("runtime alias must not be empty")
	}
	if s.PhysicalName == s.RuntimeAlias {
		return fmt.Errorf("physical name %q must differ from runtime alias %q", s.PhysicalName, s.RuntimeAlias)
	}
	if len(s.DenseVectors) == 0 {
		return fmt.Errorf("at least one dense vector must be defined")
	}

	names := make(map[string]bool)
	for i, v := range s.DenseVectors {
		if strings.TrimSpace(v.Channel) == "" {
			return fmt.Errorf("dense vector[%d]: channel name must not be empty", i)
		}
		lower := strings.ToLower(v.Channel)
		if names[lower] {
			return fmt.Errorf("duplicate dense vector channel %q", v.Channel)
		}
		names[lower] = true

		if v.Dimensions <= 0 {
			return fmt.Errorf("dense vector %q: dimensions must be positive, got %d", v.Channel, v.Dimensions)
		}
		if !isValidDistance(v.Distance) {
			return fmt.Errorf("dense vector %q: unsupported distance metric %q", v.Channel, v.Distance)
		}
	}

	for i, v := range s.SparseVectors {
		if strings.TrimSpace(v.Channel) == "" {
			return fmt.Errorf("sparse vector[%d]: channel name must not be empty", i)
		}
		lower := strings.ToLower(v.Channel)
		if names[lower] {
			return fmt.Errorf("duplicate vector channel %q (sparse)", v.Channel)
		}
		names[lower] = true
	}

	for i, idx := range s.PayloadIndexes {
		if idx.FieldName == "" {
			return fmt.Errorf("payload index[%d]: field_name must not be empty", i)
		}
		if !isValidFieldType(idx.FieldType) {
			return fmt.Errorf("payload index %q: unsupported field_type %q", idx.FieldName, idx.FieldType)
		}
	}

	return nil
}

// CompareSchema compares the expected schema against the actual collection info
// and returns a detailed diff. Compatible is true only when all vectors match
// in name, dimensions, and distance, and all required payload indexes are present.
func CompareSchema(expected *IndexSchema, actual *CollectionInfo) *SchemaDiff {
	diff := &SchemaDiff{Compatible: true}

	// Build lookup maps for expected vectors.
	expectedVecs := make(map[string]EmbeddingSpec)
	for _, v := range expected.DenseVectors {
		expectedVecs[v.Channel] = v
	}
	for _, v := range expected.SparseVectors {
		expectedVecs[v.Channel] = EmbeddingSpec{Channel: v.Channel, Dimensions: -1}
	}

	// Check actual vectors against expectations.
	if actual.VectorConfigs != nil {
		for name, cfg := range actual.VectorConfigs {
			exp, ok := expectedVecs[name]
			if !ok {
				diff.ExtraVectors = append(diff.ExtraVectors, name)
				// Extra vectors don't make the schema incompatible per se.
				continue
			}
			delete(expectedVecs, name)

			if exp.Dimensions > 0 && cfg.Size != exp.Dimensions {
				diff.DimensionMismatches = append(diff.DimensionMismatches, DimensionDiff{
					Channel: name, Expected: exp.Dimensions, Actual: cfg.Size,
				})
				diff.Compatible = false
			}
			if exp.Distance != "" && cfg.Distance != exp.Distance {
				diff.DistanceMismatches = append(diff.DistanceMismatches, DistanceDiff{
					Channel: name, Expected: exp.Distance, Actual: cfg.Distance,
				})
				diff.Compatible = false
			}
		}
	}

	for name := range expectedVecs {
		diff.MissingVectors = append(diff.MissingVectors, name)
		diff.Compatible = false
	}

	// Build lookup for expected payload indexes.
	expectedIdx := make(map[string]string)
	for _, idx := range expected.PayloadIndexes {
		expectedIdx[idx.FieldName] = idx.FieldType
	}

	// Check actual payload indexes.
	for _, info := range actual.PayloadIndexes {
		expType, ok := expectedIdx[info.FieldName]
		if !ok {
			diff.ExtraIndexes = append(diff.ExtraIndexes, info.FieldName)
			continue
		}
		delete(expectedIdx, info.FieldName)
		if expType != info.FieldType {
			diff.Compatible = false
		}
	}

	for name := range expectedIdx {
		diff.MissingIndexes = append(diff.MissingIndexes, name)
		diff.Compatible = false
	}

	return diff
}

// HasChannel returns true if the schema includes the given vector channel.
func (s *IndexSchema) HasChannel(channel string) bool {
	for _, v := range s.DenseVectors {
		if v.Channel == channel {
			return true
		}
	}
	for _, v := range s.SparseVectors {
		if v.Channel == channel {
			return true
		}
	}
	return false
}

// GetDense returns the EmbeddingSpec for a dense channel, or nil.
func (s *IndexSchema) GetDense(channel string) *EmbeddingSpec {
	for i := range s.DenseVectors {
		if s.DenseVectors[i].Channel == channel {
			return &s.DenseVectors[i]
		}
	}
	return nil
}

func isValidDistance(d string) bool {
	switch d {
	case "Cosine", "Euclid", "Dot":
		return true
	}
	return false
}

func isValidFieldType(t string) bool {
	switch t {
	case "keyword", "integer", "float", "datetime", "geo", "text", "bool":
		return true
	}
	return false
}
