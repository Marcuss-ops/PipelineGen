// collection_wire.go — JSON wire-shape decoder for CollectionInfo (PR3 split).
//
// PR3 mechanical split (June 2026): relocated from types.go without
// signature or behaviour changes. The decoder handles two distinct
// envelope shapes Qdrant has shipped over time:
//
//  1. Canonical: `{"result": {...}}` — what real Qdrant 1.10+ returns
//     for /collections/{n}. Schema fields are nested under
//     result.config.params.vectors / result.payload_schema.
//  2. Legacy leaf: `{"name": "...", "vectors_count": N, "config": {...}}`
//     — what pre-PR1 test mocks (qdrant_test.go) emit. Production
//     never sends this shape but raw cached payloads may still carry
//     it during the migration window.
//
// The discriminator is a presence probe on the `result` top-level
// key with leading-whitespace trims (Qdrant emits formatted JSON in
// some surfaces). Both shapes decode into the same public CollectionInfo
// fields so consumers (CompareSchema, CollectionManager, readiness,
// admin CLI) keep their call sites.
package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// UnmarshalJSON consumes Qdrant's nested `result.*` envelope.
//
// Reliability note: the outer object may itself be the leaf (test mocks)
// OR the Qdrant envelope `{"result": {...}}`. We treat both shapes as
// valid because the existing test surface in qdrant_test.go mocked the
// flat shape; production never sends that, but pre-PR1 callers may
// have raw payloads cached. The decoder picks the right shape via a
// presence probe on the "result" key.
func (c *CollectionInfo) UnmarshalJSON(data []byte) error {
	// Probe: do we have an envelope (`{"result":{...}}`) or are we
	// looking at the leaf (`{...}` directly)? The leaf shape is what
	// pre-PR1 tests/mocks emitted; the envelope is what real Qdrant
	// returns. The discriminator is the presence of `result` as a
	// top-level object key.
	var probe struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}

	// If `result` is a JSON object, unwrap it; otherwise treat the
	// whole payload as the leaf (legacy/mock shape). Booleans /
	// numbers / strings inside `result` are an error.
	//
	// PR1 fix (reviewer feedback): leading whitespace is legal per
	// RFC 8259 and Qdrant emits formatted JSON in some surfaces, so
	// probe.Result[0] could be a space/tab/newline, not '{'. Trim
	// the leading whitespace before the byte compare.
	trimmed := bytes.TrimLeft(probe.Result, " \t\r\n")
	if len(trimmed) > 0 && trimmed[0] == '{' {
		return c.unmarshalQdrantEnvelope(probe.Result)
	}
	return c.unmarshalLegacyLeaf(data)
}

// Compile-time assertion: CollectionInfo honours json.Unmarshaler so
// the canonical wire-shape decoding cannot drift from what callers
// expect at runtime.
var _ json.Unmarshaler = (*CollectionInfo)(nil)

// unmarshalQdrantEnvelope consumes the canonical `result.*` shape
// Qdrant returns:
//
//	{ "result": {
//	    "status": "green",
//	    "vectors_count": N,
//	    "points_count":  N,
//	    "config": { "params": {
//	        "vectors":        {<ch>: {"size": I, "distance": "Cosine"}},
//	        "sparse_vectors": {<ch>: {"modifier": "bm25"}}
//	    }},
//	    "payload_schema": {<field>: {"data_type": "..."}}
//	}}
func (c *CollectionInfo) unmarshalQdrantEnvelope(result json.RawMessage) error {
	// Re-marshal probe: contents of `result` may themselves contain a
	// nested `result` (defence in depth). We unwrap until reaching the
	// non-`result` wrapper.
	type sparseShape struct {
		Modifier string `json:"modifier,omitempty"`
		Model    string `json:"model,omitempty"`
	}
	type paramsShape struct {
		Vectors       map[string]VectorConfig `json:"vectors,omitempty"`
		SparseVectors map[string]sparseShape  `json:"sparse_vectors,omitempty"`
	}
	type configShape struct {
		Params paramsShape `json:"params"`
	}
	type payloadSchemaField struct {
		DataType string `json:"data_type,omitempty"`
	}
	type resultShape struct {
		Name          string                        `json:"name"`
		Status        string                        `json:"status"`
		VectorsCount  int                           `json:"vectors_count"`
		PointsCount   int                           `json:"points_count"`
		Config        configShape                   `json:"config"`
		PayloadSchema map[string]payloadSchemaField `json:"payload_schema"`
	}
	var r resultShape
	if err := json.Unmarshal(result, &r); err != nil {
		return fmt.Errorf("decode qdrant collection envelope: %w", err)
	}

	c.Name = r.Name
	c.Status = r.Status
	c.VectorsCount = r.VectorsCount
	c.PointTotal = r.PointsCount
	c.VectorConfigs = r.Config.Params.Vectors
	if c.VectorConfigs == nil {
		c.VectorConfigs = make(map[string]VectorConfig)
	}

	// Sparse Vectors: map modifier/model onto the public struct.
	c.SparseConfigs = make(map[string]SparseConfig, len(r.Config.Params.SparseVectors))
	for ch, sv := range r.Config.Params.SparseVectors {
		c.SparseConfigs[ch] = SparseConfig{Modifier: sv.Modifier, Model: sv.Model}
	}

	// payload_schema is a map keyed by field name; flatten to a list of
	// PayloadIndexInfo so CompareSchema can range over it unchanged.
	c.PayloadIndexes = make([]PayloadIndexInfo, 0, len(r.PayloadSchema))
	for field, info := range r.PayloadSchema {
		c.PayloadIndexes = append(c.PayloadIndexes, PayloadIndexInfo{
			FieldName: field,
			FieldType: info.DataType,
		})
	}
	// Stable order for deterministic diff output in CompareSchema.
	sortPayloadIndexes(c.PayloadIndexes)
	return nil
}

// unmarshalLegacyLeaf consumes the pre-PR1 / mock flat shape used by
// the existing test surface (qdrant_test.go). It is documented as
// legacy and removed once test fixtures migrate; callers should keep
// emitting the canonical Qdrant envelope.
func (c *CollectionInfo) unmarshalLegacyLeaf(data []byte) error {
	type alias struct {
		Name           string                  `json:"name"`
		Status         string                  `json:"status"`
		VectorsCount   int                     `json:"vectors_count"`
		VectorConfigs  map[string]VectorConfig `json:"config,omitempty"`
		PayloadIndexes []PayloadIndexInfo      `json:"payload_indexes,omitempty"`
	}
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return fmt.Errorf("decode qdrant collection (legacy leaf): %w", err)
	}
	c.Name = a.Name
	c.Status = a.Status
	c.VectorsCount = a.VectorsCount
	c.VectorConfigs = a.VectorConfigs
	if c.VectorConfigs == nil {
		c.VectorConfigs = make(map[string]VectorConfig)
	}
	c.PayloadIndexes = a.PayloadIndexes
	c.PointTotal = 0 // the legacy shape did not include points_count
	return nil
}
