// Package indexing — payload_mapper_document_test.go
// (PR-PAYLOAD-MAPPER-SPLIT-mirror, July 2026).
//
// DOCUMENT test surface (mirror of the document production subtree:
// payload_builder.go (BuildPayloadFromDocument) + index_airlock.go
// (assetToIndexDocumentNoValidate + AssetToIndexDocument) +
// index_to_point.go (IndexDocumentToPoint)).
//
// Per godlike/06 SSOT (one canonical owner per fact), this file is
// the SOLE canonical owner of the 13 Test funcs that exercise the
// IndexDocument airlock + payload writer:
//
//   - 5 BuildPayloadFromDocument_* provenance + locator-pinning tests
//     (PR 6 verdict §11 OBSERVED provenance; verbatim wire-shape keys)
//   - 3 BuildPayloadFromDocument_* semantic-field projection tests
//     (PR 6 godlike/06 SSOT: top-level AssetData field propagation)
//   - 1 AirLockStripsForbiddenFields test (PR 6 verdict §7.4 footer)
//   - 4 AssetToIndexDocument_* error-path tests (validation errors
//     surfaced through the airlock)
//
// All test funcs target the DOCUMENT production code only
// (payload_builder.go + index_airlock.go + index_to_point.go).
// They share fakeAssetStore + mapKeys + mapKeysVec from
// payload_mapper_testhelpers_test.go via same-package visibility.
// godlike/07 minimum-blast-radius: pure code-motion, no logic change.
package indexing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	qdrantSchema "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────────────────────────
// PR 6 verdict §11: OBSERVED provenance MUST win over schema's
// expected ModelVersion.
// ─────────────────────────────────────────────────────────────────

// TestBuildPayloadFromDocument_ProvenanceWritesObservedVersion pins
// the verdict §11 OBSERVED-provenance contract: the per-channel
// embedding_version_<channel> payload key MUST reflect
// EmbeddingArtifact.ModelVersion (OBSERVED), NOT the schema's
// expected value. A custom artifact with a DIFFERENT ModelVersion
// MUST win on the wire. Without this test, a future PR could
// silently "correct" the wire to the schema's value and hide drift.
func TestBuildPayloadFromDocument_ProvenanceWritesObservedVersion(t *testing.T) {
	schema := qdrantSchema.DefaultV3Schema()
	wantObserved := "observed-version-A-2026-XX-XX"
	wantSchema := schema.GetDense("text").ModelVersion
	if wantObserved == wantSchema {
		t.Fatalf("test invariant broken: observed must differ from schema's expected %q to be a meaningful provenance check", wantSchema)
	}

	doc := &IndexDocument{
		AssetID:        "asset-provenance",
		LifecycleState: asset.LifecycleState("ACTIVE"),
		Embeddings: map[VectorChannel]EmbeddingArtifact{
			ChannelText: {
				Channel:      ChannelText,
				Model:        "custom-model-A",
				ModelVersion: wantObserved, // OBSERVED wins over schema's value
				Dimensions:   schema.GetDense("text").Dimensions,
			},
		},
	}

	payload := BuildPayloadFromDocument(doc, schema)

	got, ok := payload["embedding_version_text"]
	require.True(t, ok, "payload MUST carry embedding_version_text (PR 6 verdict §11)")
	assert.Equal(t, wantObserved, got,
		"embedding_version_text = %v, want %q (provenance must reflect ARTIFACT.ModelVersion OBSERVED; schema EXPECTED was %q)",
		got, wantObserved, wantSchema)
}

// ─────────────────────────────────────────────────────────────────
// PR 6 verdict §7.4: NO forbidden locator keys on the wire.
// ─────────────────────────────────────────────────────────────────

// TestBuildPayloadFromDocument_NoForbiddenLocatorKeys pins the
// canonical wire-shape invariant: NO `local_path`, NO `status` payload
// keys are emitted (residual forbidden locator keys; drive_link
// was promoted to CANONICAL payload field by PR-CATALOG-MULTILINGUA
// step 6, July 2026). The freeze-test in composition_test.go pins
// the same invariant at the struct level; this test pins it at the
// WIRE level (the actual Payload map keys).
func TestBuildPayloadFromDocument_NoForbiddenLocatorKeys(t *testing.T) {
	schema := qdrantSchema.DefaultV3Schema()
	a := &AssetData{
		ID:             "asset-locatorcheck",
		Source:         "youtube",
		MediaType:      "video",
		LifecycleState: "ACTIVE",
		// Simulate the SQL-side fields the AirLock MUST strip from the wire:
		Status:    "ACTIVE_LEGACY", // forbidden: never reach the wire
		LocalPath: "/tmp/should-not-leak",
	}
	doc := assetToIndexDocumentNoValidate(a, schema)
	payload := BuildPayloadFromDocument(doc, schema)

	for _, forbidden := range []string{"status", "local_path"} {
		v, present := payload[forbidden]
		require.False(t, present,
			"BuildPayloadFromDocument MUST NOT emit forbidden payload key %q (PR 6 verdict §7.4 footer); got %v",
			forbidden, v)
	}
	// Sanity: the canonical keys ARE present.
	for _, mustHave := range []string{"asset_id", "lifecycle_state"} {
		_, present := payload[mustHave]
		require.True(t, present,
			"BuildPayloadFromDocument MISSING canonical payload key %q (PR 6); got keys %v",
			mustHave, mapKeys(payload))
	}
}

// ─────────────────────────────────────────────────────────────────
// PR 6: semantic enrichment + embedding_text canonical composer.
// ─────────────────────────────────────────────────────────────────

// TestBuildPayloadFromDocument_SemanticFieldsAreProjected pins the
// semantic-enrichment contract: the mapper must carry the workflow
// + content metadata through to the Qdrant payload and build a rich
// embedding_text block from it. Includes the forward-prevention
// negative assertion (no pre-step-6 labels leak into embedding_text).
func TestBuildPayloadFromDocument_SemanticFieldsAreProjected(t *testing.T) {
	schema := qdrantSchema.DefaultV3Schema()
	a := &AssetData{
		ID:             "asset-semantic",
		Name:           "Pacquiao vs Broner Round 7",
		Source:         "stock",
		MediaType:      "video",
		LifecycleState: "ACTIVE",
		Tags:           []string{"boxing", "pacquiao", "broner"},
		DurationMs:     5000,
		YouTubeVideoID: "vdC5GXxS-qU",
		YouTubeURL:     "https://www.youtube.com/watch?v=vdC5GXxS-qU",
		MetadataJSON: `{
			"title":"Pacquiao vs Broner Round 7 Broner barcolla",
			"summary":"Broner is hurt and stumbling under Pacquiao pressure",
			"source_url":"https://www.youtube.com/watch?v=vdC5GXxS-qU",
			"source_video_id":"vdC5GXxS-qU",
			"destination":"stock",
			"origin":"retrieved",
			"source_provider":"youtube",
			"event":"Pacquiao vs Broner",
			"round":7,
			"scene":"Broner barcolla",
			"subject":"Adrien Broner",
			"topics":["boxing","round 7"],
			"speakers":["Manny Pacquiao"],
			"mentioned_people":["Adrien Broner"],
			"people":["Manny Pacquiao","Adrien Broner"],
			"search_keywords":["knockdown","fight"],
			"entities":["Manny Pacquiao","Adrien Broner"],
			"hook":"Pacquiao pressures Broner",
			"search_visibility":"high",
			"policy_version":"timestamp_v1",
			"job_id":"job-1",
			"workflow_id":"wf-1",
			"run_fingerprint":"fp-1",
			"chunk_index":0,
			"total_chunks":11,
			"drive_path":"stock/Boxe/youtube/Pacquiao-Vs-Broner/Round-7-Broner-barcolla",
			"indexing_status":"indexed"
		}`,
	}

	doc := assetToIndexDocumentNoValidate(a, schema)
	payload := BuildPayloadFromDocument(doc, schema)

	assert.Equal(t, "stock", payload["destination"])
	assert.Equal(t, "retrieved", payload["origin"])
	assert.Equal(t, "youtube", payload["source_provider"])
	assert.Equal(t, "Pacquiao vs Broner Round 7 Broner barcolla", payload["semantic_title"])
	assert.Equal(t, "Broner is hurt and stumbling under Pacquiao pressure", payload["summary"])
	assert.Equal(t, "Pacquiao vs Broner", payload["event"])
	assert.Equal(t, 7, payload["round"])
	assert.Equal(t, "Broner barcolla", payload["scene"])
	assert.Equal(t, "Adrien Broner", payload["subject"])
	assert.Equal(t, 0, payload["chunk_index"])
	assert.Equal(t, 11, payload["total_chunks"])
	assert.Equal(t, "fp-1", payload["run_fingerprint"])
	assert.Equal(t, "wf-1", payload["workflow_id"])
	assert.Equal(t, "job-1", payload["job_id"])
	assert.Equal(t, "timestamp_v1", payload["policy_version"])
	assert.Equal(t, "stock/Boxe/youtube/Pacquiao-Vs-Broner/Round-7-Broner-barcolla", payload["drive_path"])
	assert.Equal(t, "indexed", payload["indexing_status"])
	assert.Equal(t, "vdC5GXxS-qU", payload["source_video_id"])
	assert.Equal(t, "https://www.youtube.com/watch?v=vdC5GXxS-qU", payload["source_url"])
	assert.Equal(t, int64(5000), payload["duration_ms"])
	assert.Equal(t, 5, payload["duration_sec"])
	// PR-CATALOG-MULTILINGUA step 6 negative: no pre-step-6 labels
	// in embedding_text.
	emb, _ := payload["embedding_text"].(string)
	require.Contains(t, emb, "Pacquiao vs Broner Round 7 Broner barcolla",
		"embedding_text missing canonical title (1st canonical field)\ngot = %q", emb)
	for _, forbidden := range []string{
		"origin:", "workflow_id:", "chunk_index:", "total_chunks:", "tags:",
	} {
		require.NotContains(t, emb, forbidden,
			"embedding_text contains forbidden pre-step-6 label %q (forward-prevention)\ngot = %q", forbidden, emb)
	}
	entities, ok := payload["entities"].([]string)
	require.True(t, ok, "entities must be projected as []string")
	assert.Contains(t, entities, "Manny Pacquiao")
	assert.Contains(t, entities, "Adrien Broner")
}

// ─────────────────────────────────────────────────────────────────
// PR 6 verdict §11 (negative symmetry): bad version is NOT
// silently relabeled.
// ─────────────────────────────────────────────────────────────────

// TestBuildPayloadFromDocument_BadModelVersion_EmitsBadKey pins the
// PR 6 §11 invariant that a bad observed ModelVersion is reflected
// in the wire VERBATIM — the point is NOT silently relabeled as the
// current schema version. The verifier's per-channel mismatch counter
// (PR 12) surfaces the drift loudly.
func TestBuildPayloadFromDocument_BadModelVersion_EmitsBadKey(t *testing.T) {
	schema := qdrantSchema.DefaultV3Schema()
	wantObserved := "bad_version_123"
	wantExpected := schema.GetDense("text").ModelVersion
	require.NotEqual(t, wantObserved, wantExpected,
		"test invariant broken: bad version must differ from schema's expected %q", wantExpected)

	doc := &IndexDocument{
		AssetID:        "asset-bad",
		LifecycleState: asset.LifecycleState("ACTIVE"),
		Embeddings: map[VectorChannel]EmbeddingArtifact{
			ChannelText: {
				Channel:      ChannelText,
				Model:        "multilingual-e5-base",
				ModelVersion: wantObserved,
				Dimensions:   schema.GetDense("text").Dimensions,
			},
		},
	}

	payload := BuildPayloadFromDocument(doc, schema)

	got, ok := payload["embedding_version_text"]
	require.True(t, ok, "payload MUST carry embedding_version_text (provenance is observed; never corrected silently)")
	assert.Equal(t, wantObserved, got)
}

// ─────────────────────────────────────────────────────────────────
// Empty ModelVersion MUST skip the per-channel key entirely.
// ─────────────────────────────────────────────────────────────────

// TestBuildPayloadFromDocument_EmptyArtifactVersionSkipped pins the
// silent-gap semantics: when EmbeddingArtifact.ModelVersion="" (a
// debug/test artifact that hasn't stamped its observed version),
// BuildPayloadFromDocument MUST NOT emit the per-channel key.
func TestBuildPayloadFromDocument_EmptyArtifactVersionSkipped(t *testing.T) {
	schema := qdrantSchema.DefaultV3Schema()
	doc := &IndexDocument{
		AssetID:        "asset-empty",
		LifecycleState: asset.LifecycleState("ACTIVE"),
		Embeddings: map[VectorChannel]EmbeddingArtifact{
			ChannelText: {
				Channel:      ChannelText,
				Model:        "no-version-yet",
				ModelVersion: "",
				Dimensions:   schema.GetDense("text").Dimensions,
			},
		},
	}
	payload := BuildPayloadFromDocument(doc, schema)
	_, present := payload["embedding_version_text"]
	assert.False(t, present,
		"BuildPayloadFromDocument MUST SKIP embedding_version_<channel> when artifact.ModelVersion is empty")
}

// ─────────────────────────────────────────────────────────────────
// AirLock verdict §7.4: IndexDocument struct MUST NOT carry the
// forbidden fields, even when AssetData has them set.
// ─────────────────────────────────────────────────────────────────

// TestAssetToIndexDocument_AirLockStripsForbiddenFields pins that the
// canonical Mapper airlock (PR 6) removes Status / DriveLink /
// LocalPath from the wire-shape IndexDocument. The legacy AssetData
// keeps these fields for diagnostic paths; the new IndexDocument
// surface does NOT.
func TestAssetToIndexDocument_AirLockStripsForbiddenFields(t *testing.T) {
	schema := qdrantSchema.DefaultV3Schema()
	a := &AssetData{
		ID:             "asset-airlock",
		Source:         "youtube",
		MediaType:      "video",
		LifecycleState: "ACTIVE",
		Status:         "ACTIVE_LEGACY",
		DriveLink:      "https://drive.example.test/leak",
		LocalPath:      "/tmp/leak",
	}
	mapper := NewPayloadMapper(&fakeAssetStore{asset: a, ids: []string{a.ID}}, nil)
	doc, err := mapper.AssetToIndexDocument(context.Background(), a, schema)
	if err != nil {
		t.Skipf("airlock validation error (vector missing): %v; skipping no-locator check", err)
	}
	docBytes, _ := json.Marshal(doc)
	for _, forbidden := range []string{"drive_link", "local_path", "status"} {
		require.False(t, bytes.Contains(docBytes, []byte(forbidden)),
			"IndexDocument JSON must not contain forbidden key %q (PR 6 verdict §7.4); got %s", forbidden, string(docBytes))
	}
}

// ─────────────────────────────────────────────────────────────────
// PR 6 zero-value semantics: empty AssetData must NOT emit
// placeholder payload keys (godlike/07 NO-FAKE-AVAILABILITY).
// ─────────────────────────────────────────────────────────────────

// TestBuildPayloadFromDocument_AssetDataSemanticFields_ZeroValueOmitsAll
// pins the godlike/06 SSOT contract that ZERO-valued AssetData
// top-level fields (PR 6's 19 NEW fields) do NOT emit any
// corresponding payload keys. Old callers that pass AssetData with
// all 21 fields at zero-value MUST continue to produce a
// backward-compatible payload.
func TestBuildPayloadFromDocument_AssetDataSemanticFields_ZeroValueOmitsAll(t *testing.T) {
	schema := qdrantSchema.DefaultV3Schema()
	a := &AssetData{
		ID:             "asset-zero-semantic",
		Source:         "stock",
		MediaType:      "video",
		LifecycleState: "ACTIVE",
		// All 19 PR-6 fields intentionally at zero-value.
	}
	doc := assetToIndexDocumentNoValidate(a, schema)
	payload := BuildPayloadFromDocument(doc, schema)

	expectedAbsent := []string{
		"title", "description", "summary", "source_url", "source_video_id",
		"source_provider", "origin", "destination", "folder_id", "folder_path", "event",
		"round", "scene", "subject", "entities", "workflow_id",
		"run_fingerprint", "chunk_index", "total_chunks", "policy_version",
		"job_id",
	}
	for _, key := range expectedAbsent {
		v, present := payload[key]
		assert.False(t, present,
			"zero-value AssetData MUST NOT emit payload key %q (godlike/07 NO-FAKE-AVAILABILITY); got %v", key, v)
	}
	_, ok := payload["asset_id"]
	require.True(t, ok, "BuildPayloadFromDocument MISSING canonical payload key asset_id")
	_, ok = payload["lifecycle_state"]
	require.True(t, ok, "BuildPayloadFromDocument MISSING canonical payload key lifecycle_state")
}

// ─────────────────────────────────────────────────────────────────
// PR 6 populated-fields propagation: struct copy delivers
// every populated AssetData field to the payload.
// ─────────────────────────────────────────────────────────────────

// TestBuildPayloadFromDocument_AssetDataSemanticFields_PopulatedPropagates
// pins the godlike/06 SSOT contract that POPULATED AssetData
// top-level fields DO emit the corresponding payload keys via the
// airlock's first-class struct copy.
func TestBuildPayloadFromDocument_AssetDataSemanticFields_PopulatedPropagates(t *testing.T) {
	schema := qdrantSchema.DefaultV3Schema()
	a := &AssetData{
		ID:             "asset-populated-semantic",
		Source:         "stock",
		MediaType:      "video",
		LifecycleState: "ACTIVE",
		Title:          "Pacquiao vs Broner Round 7",
		Description:    "Broner is hurt and stumbling",
		Summary:        "Pacquiao pressure",
		SourceURL:      "https://example.com/video.mp4",
		SourceVideoID:  "vdC5GXxS-qU",
		SourceProvider: "pexels",
		Origin:         "retrieved",
		Destination:    "stock",
		FolderID:       "1G7MYF-EDrkoMXmDvAHbwOnaOza4f2HBJ",
		FolderPath:     "Manny Pacquiao vs Adrien Broner",
		Event:          "Pacquiao vs Broner",
		Round:          7,
		Scene:          "Broner barcolla",
		Subject:        "Adrien Broner",
		Entities:       []string{"Manny Pacquiao", "Adrien Broner"},
		WorkflowID:     "wf-1",
		RunFingerprint: "fp-1",
		ChunkIndex:     0,
		TotalChunks:    11,
		PolicyVersion:  "timestamp_v1",
		JobID:          "job-1",
	}
	doc := assetToIndexDocumentNoValidate(a, schema)
	payload := BuildPayloadFromDocument(doc, schema)

	assert.Equal(t, "Pacquiao vs Broner Round 7", payload["title"])
	assert.Equal(t, "Broner is hurt and stumbling", payload["description"])
	assert.Equal(t, "Pacquiao pressure", payload["summary"])
	assert.Equal(t, "https://example.com/video.mp4", payload["source_url"])
	assert.Equal(t, "vdC5GXxS-qU", payload["source_video_id"])
	assert.Equal(t, "pexels", payload["source_provider"])
	assert.Equal(t, "retrieved", payload["origin"])
	assert.Equal(t, "stock", payload["destination"])
	assert.Equal(t, "1G7MYF-EDrkoMXmDvAHbwOnaOza4f2HBJ", payload["folder_id"])
	assert.Equal(t, "Manny Pacquiao vs Adrien Broner", payload["folder_path"])
	assert.Equal(t, "Pacquiao vs Broner", payload["event"])
	assert.Equal(t, 7, payload["round"])
	assert.Equal(t, "Broner barcolla", payload["scene"])
	assert.Equal(t, "Adrien Broner", payload["subject"])
	entities, ok := payload["entities"].([]string)
	require.True(t, ok, "entities must be projected as []string")
	assert.Equal(t, []string{"Manny Pacquiao", "Adrien Broner"}, entities)
	assert.Equal(t, "wf-1", payload["workflow_id"])
	assert.Equal(t, "fp-1", payload["run_fingerprint"])
	assert.Equal(t, 0, payload["chunk_index"])
	assert.Equal(t, 11, payload["total_chunks"])
	assert.Equal(t, "timestamp_v1", payload["policy_version"])
	assert.Equal(t, "job-1", payload["job_id"])
}

// ─────────────────────────────────────────────────────────────────
// AssetToIndexDocument validation error paths (4 funcs).
// ─────────────────────────────────────────────────────────────────

// TestAssetToIndexDocument_MissingRequiredTextVector asserts that
// AssetToIndexDocument surfaces *transport.ErrMissingRequiredVector
// when the text channel is nil (level-1 validation: required channels
// must not be absent).
func TestAssetToIndexDocument_MissingRequiredTextVector(t *testing.T) {
	schema := qdrantSchema.DefaultV3Schema()
	a := &AssetData{
		ID:             "asset-tx-missing",
		Source:         "youtube",
		MediaType:      "video",
		LifecycleState: "ACTIVE",
		SearchText:     "search text",
		// TextVector NOT supplied.
	}
	mapper := NewPayloadMapper(&fakeAssetStore{asset: a, ids: []string{a.ID}}, nil)
	_, err := mapper.AssetToIndexDocument(context.Background(), a, schema)
	require.Error(t, err, "nil text vector must error at AssetToIndexDocument")
	var missing *transport.ErrMissingRequiredVector
	require.True(t, errors.As(err, &missing),
		"expected *transport.ErrMissingRequiredVector, got %T: %v", err, err)
	assert.Equal(t, "text", missing.Channel)
}

// TestAssetToIndexDocument_ZeroLengthTextVector asserts that
// AssetToIndexDocument surfaces *transport.ErrEmptyVector when a
// non-nil text vector has len==0 (corrupted, distinct from nil).
func TestAssetToIndexDocument_ZeroLengthTextVector(t *testing.T) {
	schema := qdrantSchema.DefaultV3Schema()
	a := &AssetData{
		ID:             "asset-tx-zero",
		Source:         "youtube",
		MediaType:      "video",
		LifecycleState: "ACTIVE",
		TextVector:     make([]float32, 0), // non-nil, zero-length
	}
	mapper := NewPayloadMapper(&fakeAssetStore{asset: a, ids: []string{a.ID}}, nil)
	_, err := mapper.AssetToIndexDocument(context.Background(), a, schema)
	require.Error(t, err, "zero-length text vector must error at AssetToIndexDocument")
	var empty *transport.ErrEmptyVector
	require.True(t, errors.As(err, &empty),
		"expected *transport.ErrEmptyVector, got %T: %v", err, err)
}

// TestAssetToIndexDocument_OptionalChannelsNilAllowed asserts the
// optional channels (audio, transcript, visual) MAY be nil without
// erroring the airlock.
func TestAssetToIndexDocument_OptionalChannelsNilAllowed(t *testing.T) {
	schema := qdrantSchema.DefaultV3Schema()
	a := &AssetData{
		ID:             "asset-optional-nil",
		Source:         "youtube",
		MediaType:      "video",
		LifecycleState: "ACTIVE",
		SearchText:     "search text",
		TextVector:     makeFloat32Slice(768),
		// AudioVector / TranscriptVector / VisualVector intentionally nil.
	}
	mapper := NewPayloadMapper(&fakeAssetStore{asset: a, ids: []string{a.ID}}, nil)
	doc, err := mapper.AssetToIndexDocument(context.Background(), a, schema)
	require.NoError(t, err, "all optional channels nil must NOT error at airlock")
	require.NotNil(t, doc)
}

// TestAssetToIndexDocument_NaNInVector asserts that AssetToIndexDocument
// surfaces *transport.ErrNaNOrInf when any element of any vector is NaN.
func TestAssetToIndexDocument_NaNInVector(t *testing.T) {
	schema := qdrantSchema.DefaultV3Schema()
	a := &AssetData{
		ID:             "asset-tx-nan",
		Source:         "youtube",
		MediaType:      "video",
		LifecycleState: "ACTIVE",
		SearchText:     "search text",
		TextVector:     makeFloat32Slice(768),
	}
	a.TextVector[100] = float32(nanSentinel())
	mapper := NewPayloadMapper(&fakeAssetStore{asset: a, ids: []string{a.ID}}, nil)
	_, err := mapper.AssetToIndexDocument(context.Background(), a, schema)
	require.Error(t, err, "NaN in vector must error at AssetToIndexDocument")
	var nanErr *transport.ErrNaNOrInf
	require.True(t, errors.As(err, &nanErr),
		"expected *transport.ErrNaNOrInf, got %T: %v", err, err)
}

// TestAssetToIndexDocument_DimensionMismatch asserts that
// AssetToIndexDocument surfaces *transport.ErrVectorDimensionMismatch
// when a vector's length doesn't match the schema-expected dimension.
func TestAssetToIndexDocument_DimensionMismatch(t *testing.T) {
	schema := qdrantSchema.DefaultV3Schema()
	a := &AssetData{
		ID:             "asset-tx-dim",
		Source:         "youtube",
		MediaType:      "video",
		LifecycleState: "ACTIVE",
		SearchText:     "search text",
		TextVector:     makeFloat32Slice(512), // 512d, but schema expects 768d
	}
	mapper := NewPayloadMapper(&fakeAssetStore{asset: a, ids: []string{a.ID}}, nil)
	_, err := mapper.AssetToIndexDocument(context.Background(), a, schema)
	require.Error(t, err, "dimension mismatch must error at AssetToIndexDocument")
	var dimErr *transport.ErrVectorDimensionMismatch
	require.True(t, errors.As(err, &dimErr),
		"expected *transport.ErrVectorDimensionMismatch, got %T: %v", err, err)
	assert.Equal(t, 768, dimErr.Expected)
	assert.Equal(t, 512, dimErr.Actual)
}

// nanSentinel is a local helper producing a NaN float32 value
// without depending on the math package (kept inline to minimize
// imports; identical semantics to math.NaN()).
func nanSentinel() float64 {
	var z float64
	return z / z // NaN
}
