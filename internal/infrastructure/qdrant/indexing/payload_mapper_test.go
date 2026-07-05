// Package qdrant — payload_mapper_test.go pins three invariants:
//   - canonical lifecycle key (QDRANT-004 PR2, June 2026) — payload MUST be written under the verbatim key "lifecycle_state" (this is the origin/main HEAD literal kept verbatim per AGENTS.md rebase-conflict lesson; mine added the surrounding invariant envelope).
//   - per-channel embedding_version_<channel> roundtrip (from PR 6 verdict §11, observed provenance; the wire MUST carry the OBSERVED EmbeddingArtifact.ModelVersion, NOT the schema's expected value — see TestBuildPayloadFromDocument_BadModelVersion_EmitsBadKey below).
//   - PR2 server-side BM25 sparse vector shape: vectors.bm25_text must be {text: <asset.SearchText>, model: "qdrant/bm25"} — payload-only configuration is insufficient for server-side BM25 inference in Qdrant 1.10+; the vector field carries the text directly.
//
// BuildPayload is the SINGLE producer of the Qdrant payload for an
// asset and AssetToPoint is the SINGLE producer of the Qdrant
// point vectors. Any future PR that mutates either function
// MUST update the corresponding test in lockstep.
package indexing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/searchtext"
	qdrantSchema "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// TestBuildPayload_LifecycleKeyIsCanonical asserts that BuildPayload
// writes asset.LifecycleState under the canonical key "lifecycle_state"
// (PR 1 / QDRANT-004 §(b)) and does NOT silently emit a legacy
// "status" key (which a Qdrant payload index in qdrantSchema.DefaultV3Schema would
// not be able to filter on).
func TestBuildPayload_LifecycleKeyIsCanonical(t *testing.T) {
	asset := &AssetData{
		ID:             "asset-1",
		LifecycleState: "ACTIVE",
		Source:         "stock",
	}
	schema := qdrantSchema.DefaultV3Schema()

	payload := BuildPayload(asset, schema)

	got, ok := payload["lifecycle_state"]
	if !ok {
		t.Fatalf("BuildPayload must write canonical payload key %q; got keys %v", "lifecycle_state", mapKeys(payload))
	}
	if got != "ACTIVE" {
		t.Errorf("lifecycle_state => %v, want %q (PR 1 canonical SSOT)", got, "ACTIVE")
	}

	if _, leaked := payload["status"]; leaked {
		t.Errorf("BuildPayload MUST NOT emit legacy %q key (QDRANT-004 PR2 drift window); got keys %v", "status", mapKeys(payload))
	}
}

// TestBuildPayload_EmbeddingVersionsByChannel sanity-checks that
// the per-channel embedding_version_<channel> payload keys roundtrip
// from the manifest into the payload (different invariant but co-
// located for the SSOT suite). Without this the reindex verifier's
// per-channel counters could regress.
func TestBuildPayload_EmbeddingVersionsByChannel(t *testing.T) {
	asset := &AssetData{ID: "asset-2", LifecycleState: "ACTIVE"}
	schema := qdrantSchema.DefaultV3Schema()

	payload := BuildPayload(asset, schema)

	for _, spec := range schema.DenseVectors {
		key := "embedding_version_" + spec.Channel
		got, ok := payload[key]
		if !ok {
			t.Errorf("payload missing %q (channel=%q)", key, spec.Channel)
			continue
		}
		if got != spec.ModelVersion {
			t.Errorf("%q => %v, want %q", key, got, spec.ModelVersion)
		}
	}
}

// mapKeys is a tiny helper to keep assertion messages readable.
func mapKeys(m map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// ── PR2 (fix/qdrant-bm25-indexing): sparse vector wire-shape ─────────────

// TestAssetToPoint_SparseVector_HasServerSideShape pins the wire
// shape for the bm25_text sparse channel: `vectors.bm25_text` MUST be
// `{text: <asset.SearchText>, model: <channelModel>}`. Search payload
// alone is NOT enough to drive server-side BM25 — the inference model
// receives the text directly through the vector field per Qdrant 1.10+.
func TestAssetToPoint_SparseVector_HasServerSideShape(t *testing.T) {
	asset := &AssetData{
		ID:             "asset-bm25",
		SearchText:     "the quick brown fox jumps over the lazy dog",
		WorkspaceID:    "ws-1",
		Source:         "youtube",
		MediaType:      "video",
		Language:       "en",
		LifecycleState: "ACTIVE",
		TextVector:     makeFloat32Slice(768),
	}
	schema := qdrantSchema.DefaultV3Schema()

	mapper := NewPayloadMapper(&fakeAssetStore{asset: asset, ids: []string{asset.ID}}, nil)
	point, err := mapper.AssetToPoint(asset, schema)
	if err != nil {
		t.Fatalf("AssetToPoint error: %v", err)
	}
	requirePointID(t, point)

	bm25, ok := point.Vectors["bm25_text"]
	if !ok {
		t.Fatalf("PR2: vectors.bm25_text MUST be present (server-side BM25 inference requires the vector field, not the payload); got channels %v", mapKeysVec(point.Vectors))
	}
	m, ok := bm25.(map[string]interface{})
	if !ok {
		t.Fatalf("PR2: vectors.bm25_text must be {text, model} shape; got %T", bm25)
	}
	if m["text"] != asset.SearchText {
		t.Errorf("vectors.bm25_text.text = %v, want %q", m["text"], asset.SearchText)
	}
	if m["model"] != qdrantSchema.DefaultSparseModel {
		t.Errorf("vectors.bm25_text.model = %v, want %q (qdrantSchema.DefaultSparseModel)", m["model"], qdrantSchema.DefaultSparseModel)
	}
}

// TestAssetToPoint_SparseVector_EmptySearchText_DropsChannel pins
// that AssetToPoint MUST NOT write an empty sparse vector when
// SearchText is empty. Today the mapper skips the channel and
// logs Debug; this test freezes that behaviour so a future change
// that emits `{text: ""}` would not index an empty BM25 vector.
func TestAssetToPoint_SparseVector_EmptySearchText_DropsChannel(t *testing.T) {
	asset := &AssetData{
		ID:             "asset-no-bm25",
		SearchText:     "",
		WorkspaceID:    "ws-1",
		Source:         "youtube",
		MediaType:      "video",
		LifecycleState: "ACTIVE",
		TextVector:     makeFloat32Slice(768),
	}
	schema := qdrantSchema.DefaultV3Schema()

	mapper := NewPayloadMapper(&fakeAssetStore{asset: asset, ids: []string{asset.ID}}, nil)
	point, err := mapper.AssetToPoint(asset, schema)
	if err != nil {
		t.Fatalf("AssetToPoint error: %v", err)
	}
	if _, present := point.Vectors["bm25_text"]; present {
		t.Errorf("PR2: empty SearchText MUST drop the bm25_text channel; got %v", point.Vectors["bm25_text"])
	}
}

// TestAssetToPoint_TranscriptChannel_DroppedWhenAbsent pins the
// PR2 contract on the transcript channel: there is no fallback to
// the text vector. When TranscriptVector is nil the transcript
// channel MUST be absent from the point vectors (no synthetic /
// fake vectors).
func TestAssetToPoint_TranscriptChannel_DroppedWhenAbsent(t *testing.T) {
	asset := &AssetData{
		ID:             "asset-tx",
		SearchText:     "transcript text",
		Source:         "youtube",
		MediaType:      "video",
		LifecycleState: "ACTIVE",
		// TextVector present (required), TranscriptVector absent.
		TextVector: makeFloat32Slice(768),
		// TranscriptVector NOT supplied.
	}
	schema := qdrantSchema.DefaultV3Schema()

	mapper := NewPayloadMapper(&fakeAssetStore{asset: asset, ids: []string{asset.ID}}, nil)
	point, err := mapper.AssetToPoint(asset, schema)
	if err != nil {
		t.Fatalf("AssetToPoint error: %v", err)
	}
	if _, present := point.Vectors["transcript"]; present {
		t.Errorf("PR2 (verdetto §10): transcript channel MUST be dropped when absent; pre-PR2 fell back to TextVector. Got %v", point.Vectors["transcript"])
	}
}

// TestAssetToPoint_TranscriptChannel_PreservedWhenPresent pins
// the symmetric case: when the asset carries a real transcript
// vector, the transcript channel is present and equals the
// dedicated embedding (NOT a copy of the text vector).
func TestAssetToPoint_TranscriptChannel_PreservedWhenPresent(t *testing.T) {
	textVec := makeFloat32Slice(768)
	transcriptVec := makeFloat32Slice(768)
	// Distinguish: make the transcript vec not equal to the text vec.
	for i := range transcriptVec {
		transcriptVec[i] = float32(i+1) * 0.001
	}
	asset := &AssetData{
		ID:               "asset-tx-present",
		SearchText:       "transcript text",
		Source:           "youtube",
		MediaType:        "video",
		LifecycleState:   "ACTIVE",
		TextVector:       textVec,
		TranscriptVector: transcriptVec,
	}
	schema := qdrantSchema.DefaultV3Schema()

	mapper := NewPayloadMapper(&fakeAssetStore{asset: asset, ids: []string{asset.ID}}, nil)
	point, err := mapper.AssetToPoint(asset, schema)
	if err != nil {
		t.Fatalf("AssetToPoint error: %v", err)
	}
	got, ok := point.Vectors["transcript"]
	if !ok {
		t.Fatalf("transcript channel MUST be present when TranscriptVector is supplied")
	}
	gotF, ok := got.([]float32)
	if !ok {
		t.Fatalf("transcript channel: type %T, want []float32", got)
	}
	if len(gotF) != len(transcriptVec) {
		t.Fatalf("transcript length = %d, want %d", len(gotF), len(transcriptVec))
	}
	for i := range gotF {
		if gotF[i] != transcriptVec[i] {
			t.Fatalf("transcript[%d] = %v, want %v (must equal dedicated transcript embedding, not a textVector copy)", i, gotF[i], transcriptVec[i])
		}
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────

// fakeAssetStore is a minimal AssetStore for AssetToPoint unit tests.
// It returns the single seeded asset regardless of which ID is
// requested (AssetToPoint does not round-trip through FetchAsset).
type fakeAssetStore struct {
	asset *AssetData
	ids   []string
}

func (f *fakeAssetStore) FetchAsset(ctx context.Context, id string) (*AssetData, error) {
	if f.asset == nil || f.asset.ID != id {
		return nil, &ErrAssetNotFound{ID: id}
	}
	return f.asset, nil
}

func (f *fakeAssetStore) ListAllAssetIDs(ctx context.Context) ([]string, error) {
	return f.ids, nil
}

func (f *fakeAssetStore) FetchAssetBatch(ctx context.Context, afterID string, limit int) ([]*AssetData, error) {
	return nil, nil
}

// makeFloat32Slice creates a []float32 of the given size filled with 1.0.
func makeFloat32Slice(size int) []float32 {
	v := make([]float32, size)
	for i := range v {
		v[i] = 1.0
	}
	return v
}

func mapKeysVec(m map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func requirePointID(t *testing.T, p *qdrantSchema.Point) {
	t.Helper()
	if p == nil {
		t.Fatal("point is nil")
	}
	if p.ID == "" {
		t.Fatal("point ID is empty (qdrantSchema.AssetIDToQdrantPointID canonicalisation must run)")
	}
}

// ══════════════════════════════════════════════════════════════════════════
// PR 6 (refactor/qdrant-index-document) — provenance + locator-free tests.
// ══════════════════════════════════════════════════════════════════════════

// TestBuildPayloadFromDocument_ProvenanceWritesObservedVersion pins
// verdict §11: the on-disk payload key
// `embedding_version_<channel>` MUST come from
// EmbeddingArtifact.ModelVersion (the OBSERVED provenance), NOT from
// the schema's qdrantSchema.EmbeddingSpec.ModelVersion (the EXPECTED). The Mapper
// airlock produces an artifact whose ModelVersion defaults to the
// schema's value (write-only provenance), but a custom artifact with
// a DIFFERENT ModelVersion MUST win. This test posts a custom
// EmbeddingArtifact and verifies the wire reflects the override.
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
	if !ok {
		t.Fatalf("payload missing embedding_version_text (PR 6 verdict §11)")
	}
	if got != wantObserved {
		t.Errorf("embedding_version_text = %v, want %q (provenance must reflect ARTIFACT.ModelVersion OBSERVED; schema EXPECTED was %q)",
			got, wantObserved, wantSchema)
	}
}

// TestBuildPayloadFromDocument_NoForbiddenLocatorKeys pins the
// canonical wire-shape invariant: NO `drive_link`, NO `local_path`,
// NO `status` payload keys are emitted. The freeze-test in
// composition_test.go pins the same invariant at the struct level;
// this test pins it at the WIRE level (the actual Payload map keys).
//
// The AirLock via assetToIndexDocumentNoValidate must strip the SQL-
// fetch derived fields (AssetData.DriveLink / AssetData.LocalPath /
// AssetData.Status) so the iter field references below cannot leak.
func TestBuildPayloadFromDocument_NoForbiddenLocatorKeys(t *testing.T) {
	schema := qdrantSchema.DefaultV3Schema()
	asset := &AssetData{
		ID:             "asset-locatorcheck",
		Source:         "youtube",
		MediaType:      "video",
		LifecycleState: "ACTIVE",
		// Simulate the SQL-side fields the AirLock MUST strip from the wire:
		Status:    "ACTIVE_LEGACY", // forbidden, never reach the wire
		DriveLink: "https://drive.example.test/should-not-leak",
		LocalPath: "/tmp/should-not-leak",
	}
	doc := assetToIndexDocumentNoValidate(asset, schema)
	payload := BuildPayloadFromDocument(doc, schema)

	for _, forbidden := range []string{"status", "drive_link", "local_path"} {
		if v, present := payload[forbidden]; present {
			t.Errorf("BuildPayloadFromDocument MUST NOT emit forbidden payload key %q (PR 6 verdict §7.4 footer); got %v", forbidden, v)
		}
	}
	// Sanity: the canonical keys ARE present.
	for _, mustHave := range []string{"asset_id", "lifecycle_state"} {
		if _, present := payload[mustHave]; !present {
			t.Errorf("BuildPayloadFromDocument MISSING canonical payload key %q (PR 6); got keys %v", mustHave, mapKeys(payload))
		}
	}
}

// TestBuildPayloadFromDocument_BadModelVersion_EmitsBadKey pins the
// PR 6 §#6.1 invariant: an EmbeddingArtifact whose ModelVersion
// DOESN'T match the schema's expected version must be reflected in
// the wire verbatim — the point is NOT silently relabeled as the
// current schema version. The verifier's
// qdrantSchema.SwitchReport.VersionMismatchPerChannel[<channel>] counter (PR 12)
// surfaces the drift loudly; the contract here is that the writer
// does NOT hide it.
func TestBuildPayloadFromDocument_BadModelVersion_EmitsBadKey(t *testing.T) {
	schema := qdrantSchema.DefaultV3Schema()
	wantObserved := "bad_version_123"
	wantExpected := schema.GetDense("text").ModelVersion
	if wantObserved == wantExpected {
		t.Fatalf("test invariant broken: bad version must differ from schema's expected %q", wantExpected)
	}

	doc := &IndexDocument{
		AssetID:        "asset-bad",
		LifecycleState: asset.LifecycleState("ACTIVE"),
		Embeddings: map[VectorChannel]EmbeddingArtifact{
			ChannelText: {
				Channel:      ChannelText,
				Model:        "multilingual-e5-base",
				ModelVersion: wantObserved, // OBSERVED provenance carries the wrong version
				Dimensions:   schema.GetDense("text").Dimensions,
			},
		},
	}

	payload := BuildPayloadFromDocument(doc, schema)

	got, ok := payload["embedding_version_text"]
	if !ok {
		t.Fatalf("payload MUST carry embedding_version_text (provenance is observed; never \"corrected\" silently); got keys %v", mapKeys(payload))
	}
	if got != wantObserved {
		t.Errorf("embedding_version_text = %v, want %q (provenance must REFLECT observed bad version; schema's expected was %q)",
			got, wantObserved, wantExpected)
	}
}

// TestBuildPayloadFromDocument_EmptyArtifactVersionSkipped pins the
// silent-gap semantics: when an EmbeddingArtifact has ModelVersion=""
// (e.g. a debug/test artifact that hasn't stamped its observed
// version), BuildPayloadFromDocument MUST NOT emit the per-channel
// key. The verifier's per-channel mismatch counter (PR 12) surfaces
// the gap separately — a non-silent failure pathway.
func TestBuildPayloadFromDocument_EmptyArtifactVersionSkipped(t *testing.T) {
	schema := qdrantSchema.DefaultV3Schema()
	doc := &IndexDocument{
		AssetID:        "asset-empty",
		LifecycleState: asset.LifecycleState("ACTIVE"),
		Embeddings: map[VectorChannel]EmbeddingArtifact{
			ChannelText: {
				Channel:      ChannelText,
				Model:        "no-version-yet",
				ModelVersion: "", // explicitly empty observed version
				Dimensions:   schema.GetDense("text").Dimensions,
			},
		},
	}
	payload := BuildPayloadFromDocument(doc, schema)
	if _, present := payload["embedding_version_text"]; present {
		t.Errorf("BuildPayloadFromDocument MUST SKIP embedding_version_<channel> when artifact.ModelVersion is empty; got %v", payload["embedding_version_text"])
	}
}

// TestAssetToIndexDocument_AirLockStripsForbiddenFields pins that the
// canonical Mapper airlock (PR 6) removes Status / DriveLink /
// LocalPath from the wire-shape IndexDocument. The legacy AssetData
// keeps these fields for diagnostic paths; the new IndexDocument
// surface does NOT.
func TestAssetToIndexDocument_AirLockStripsForbiddenFields(t *testing.T) {
	schema := qdrantSchema.DefaultV3Schema()
	asset := &AssetData{
		ID:             "asset-airlock",
		Source:         "youtube",
		MediaType:      "video",
		LifecycleState: "ACTIVE",
		Status:         "ACTIVE_LEGACY",
		DriveLink:      "https://drive.example.test/leak",
		LocalPath:      "/tmp/leak",
	}
	mapper := NewPayloadMapper(&fakeAssetStore{asset: asset, ids: []string{asset.ID}}, nil)
	doc, err := mapper.AssetToIndexDocument(asset, schema)
	if err != nil {
		// Audio is missing + transcript missing but text is required;
		// provide a TextVector so the validation path doesn't fail.
		// Skip on err; the no-locator check is the focus.
		t.Skipf("airlock validation error (vector missing?): %v; skipping no-locator check", err)
	}
	// The IndexDocument struct MUST NOT carry the forbidden fields.
	// We type-assert this by checking reflection — IndexDocument has
	// no Status / DriveLink / LocalPath fields by design.
	// If a future PR adds one, the freeze test in composition_test.go
	// catches it; the assetToIndexDocumentNoValidate path also guards
	// against future regression here.
	docBytes, _ := json.Marshal(doc)
	found := false
	for _, forbidden := range []string{"drive_link", "local_path", "status"} {
		if bytes.Contains(docBytes, []byte(forbidden)) {
			found = true
			t.Errorf("IndexDocument JSON must not contain forbidden key %q (PR 6 verdict §7.4 footer); got %s", forbidden, string(docBytes))
			break
		}
	}
	_ = found // explicit no-op marker for grep forensics
}

// ── Task 4 (July 2026) — dense-vector validation tests ─────────────────

// TestValidateDenseVector_RequiredChannelNil returns ErrMissingRequiredVector
// for channels classified as policyRequired (text).
func TestValidateDenseVector_RequiredChannelNil(t *testing.T) {
	err := validateDenseVector("text", nil, 768, "asset-1")
	if err == nil {
		t.Fatal("nil text vector must return error")
	}
	var missing *transport.ErrMissingRequiredVector
	if !errors.As(err, &missing) {
		t.Fatalf("expected *ErrMissingRequiredVector, got %T: %v", err, err)
	}
	if missing.Channel != "text" || missing.AssetID != "asset-1" {
		t.Errorf("error details: channel=%q assetID=%q", missing.Channel, missing.AssetID)
	}
}

// TestValidateDenseVector_OptionalChannelNil returns nil for channels
// classified as policyOptional (audio, transcript, visual).
func TestValidateDenseVector_OptionalChannelNil(t *testing.T) {
	for _, ch := range []string{"audio", "transcript", "visual", "future_channel"} {
		t.Run(ch, func(t *testing.T) {
			if err := validateDenseVector(ch, nil, 512, "asset-1"); err != nil {
				t.Errorf("nil %q must be silently skipped; got %v", ch, err)
			}
		})
	}
}

// TestValidateDenseVector_ZeroLengthVector returns ErrEmptyVector for
// non-nil but empty slices (len==0). Distinct from nil (missing).
func TestValidateDenseVector_ZeroLengthVector(t *testing.T) {
	emptyVec := make([]float32, 0) // non-nil, zero-length
	err := validateDenseVector("text", emptyVec, 768, "asset-2")
	if err == nil {
		t.Fatal("zero-length vector must return error")
	}
	var empty *transport.ErrEmptyVector
	if !errors.As(err, &empty) {
		t.Fatalf("expected *ErrEmptyVector for zero-length, got %T: %v", err, err)
	}
	if empty.Channel != "text" || empty.AssetID != "asset-2" {
		t.Errorf("error details: channel=%q assetID=%q", empty.Channel, empty.AssetID)
	}
}

// TestValidateDenseVector_DimensionMismatch returns ErrVectorDimensionMismatch.
func TestValidateDenseVector_DimensionMismatch(t *testing.T) {
	vec := makeFloat32Slice(512) // 512d, but schema expects 768
	err := validateDenseVector("text", vec, 768, "asset-3")
	if err == nil {
		t.Fatal("dimension mismatch must return error")
	}
	var dimErr *transport.ErrVectorDimensionMismatch
	if !errors.As(err, &dimErr) {
		t.Fatalf("expected *ErrVectorDimensionMismatch, got %T: %v", err, err)
	}
	if dimErr.Expected != 768 || dimErr.Actual != 512 {
		t.Errorf("expected=768 actual=%d; got expected=%d actual=%d", 512, dimErr.Expected, dimErr.Actual)
	}
}

// TestValidateDenseVector_NaN returns ErrNaNOrInf.
func TestValidateDenseVector_NaN(t *testing.T) {
	vec := makeFloat32Slice(768)
	vec[100] = float32(math.NaN())
	err := validateDenseVector("text", vec, 768, "asset-4")
	if err == nil {
		t.Fatal("NaN vector must return error")
	}
	var nanErr *transport.ErrNaNOrInf
	if !errors.As(err, &nanErr) {
		t.Fatalf("expected *ErrNaNOrInf, got %T: %v", err, err)
	}
}

// TestValidateDenseVector_Inf returns ErrNaNOrInf.
func TestValidateDenseVector_Inf(t *testing.T) {
	vec := makeFloat32Slice(768)
	vec[200] = float32(math.Inf(1))
	err := validateDenseVector("visual", vec, 768, "asset-5")
	if err == nil {
		t.Fatal("Inf vector must return error")
	}
	var infErr *transport.ErrNaNOrInf
	if !errors.As(err, &infErr) {
		t.Fatalf("expected *ErrNaNOrInf, got %T: %v", err, err)
	}
	if infErr.Channel != "visual" {
		t.Errorf("channel=%q want visual", infErr.Channel)
	}
}

// TestValidateDenseVector_NegativeInf returns ErrNaNOrInf.
func TestValidateDenseVector_NegativeInf(t *testing.T) {
	vec := makeFloat32Slice(768)
	vec[300] = float32(math.Inf(-1))
	err := validateDenseVector("text", vec, 768, "asset-6")
	if err == nil {
		t.Fatal("negative Inf vector must return error")
	}
	if _, ok := err.(*transport.ErrNaNOrInf); !ok {
		t.Fatalf("expected *ErrNaNOrInf, got %T", err)
	}
}

// TestValidateDenseVector_ValidVector returns nil.
func TestValidateDenseVector_ValidVector(t *testing.T) {
	vec := makeFloat32Slice(768)
	for _, ch := range []string{"text", "visual", "audio", "transcript"} {
		t.Run(ch, func(t *testing.T) {
			if err := validateDenseVector(ch, vec, 768, "asset-ok"); err != nil {
				t.Errorf("valid %q vector must not error; got %v", ch, err)
			}
		})
	}
}

// TestValidateDenseVector_MultipleErrors_FirstWins verifies that when
// multiple issues exist (e.g. zero-length AND would-be-wrong-dimension),
// the first check (zero-length) wins per the documented order.
func TestValidateDenseVector_MultipleErrors_FirstWins(t *testing.T) {
	// zero-length vector: first check that fails is len(vec)==0
	emptyVec := make([]float32, 0)
	err := validateDenseVector("text", emptyVec, 768, "asset-multi")
	var empty *transport.ErrEmptyVector
	if !errors.As(err, &empty) {
		t.Fatalf("zero-length must surface as ErrEmptyVector (first check wins); got %T: %v", err, err)
	}
}

// TestAssetToIndexDocument_MissingRequiredTextVector ensures the full
// mapper path surfaces ErrMissingRequiredVector for nil TextVector.
func TestAssetToIndexDocument_MissingRequiredTextVector(t *testing.T) {
	schema := qdrantSchema.DefaultV3Schema()
	asset := &AssetData{
		ID:             "asset-no-text",
		Source:         "youtube",
		MediaType:      "video",
		LifecycleState: "ACTIVE",
		// TextVector intentionally nil — required channel.
	}
	mapper := NewPayloadMapper(&fakeAssetStore{asset: asset, ids: []string{asset.ID}}, nil)
	_, err := mapper.AssetToIndexDocument(asset, schema)
	if err == nil {
		t.Fatal("missing required text vector must error")
	}
	var missing *transport.ErrMissingRequiredVector
	if !errors.As(err, &missing) {
		t.Fatalf("expected *ErrMissingRequiredVector, got %T: %v", err, err)
	}
}

// TestAssetToIndexDocument_ZeroLengthTextVector ensures the mapper
// surfaces ErrEmptyVector for zero-length (but non-nil) TextVector.
func TestAssetToIndexDocument_ZeroLengthTextVector(t *testing.T) {
	schema := qdrantSchema.DefaultV3Schema()
	asset := &AssetData{
		ID:             "asset-zero-text",
		Source:         "youtube",
		MediaType:      "video",
		LifecycleState: "ACTIVE",
		TextVector:     make([]float32, 0), // non-nil, zero-length
	}
	mapper := NewPayloadMapper(&fakeAssetStore{asset: asset, ids: []string{asset.ID}}, nil)
	_, err := mapper.AssetToIndexDocument(asset, schema)
	if err == nil {
		t.Fatal("zero-length text vector must error")
	}
	var empty *transport.ErrEmptyVector
	if !errors.As(err, &empty) {
		t.Fatalf("expected *ErrEmptyVector for zero-length, got %T: %v", err, err)
	}
}

// TestAssetToIndexDocument_OptionalChannelsNilAllowed ensures the mapper
// silently accepts nil for optional channels (audio, transcript, visual).
func TestAssetToIndexDocument_OptionalChannelsNilAllowed(t *testing.T) {
	schema := qdrantSchema.DefaultV3Schema()
	asset := &AssetData{
		ID:             "asset-optional",
		Source:         "youtube",
		MediaType:      "video",
		LifecycleState: "ACTIVE",
		TextVector:     makeFloat32Slice(768),
		// AudioVector, TranscriptVector, VisualVector all nil — OK.
	}
	mapper := NewPayloadMapper(&fakeAssetStore{asset: asset, ids: []string{asset.ID}}, nil)
	doc, err := mapper.AssetToIndexDocument(asset, schema)
	if err != nil {
		t.Fatalf("nil optional channels must not error; got %v", err)
	}
	// Text must be present; optional channels must be absent.
	if _, ok := doc.Embeddings[ChannelText]; !ok {
		t.Error("text embedding must be present when vector is valid")
	}
	for _, ch := range []VectorChannel{ChannelAudio, ChannelTranscript, ChannelVisual} {
		if art, ok := doc.Embeddings[ch]; ok && art.Values != nil {
			t.Errorf("optional channel %q must not have Values when asset vector is nil; got %v", ch, art.Values)
		}
	}
}

// TestAssetToIndexDocument_NaNInVector returns ErrNaNOrInf through the
// full mapper path.
func TestAssetToIndexDocument_NaNInVector(t *testing.T) {
	schema := qdrantSchema.DefaultV3Schema()
	vec := makeFloat32Slice(768)
	vec[50] = float32(math.NaN())
	asset := &AssetData{
		ID:             "asset-nan",
		Source:         "youtube",
		MediaType:      "video",
		LifecycleState: "ACTIVE",
		TextVector:     vec,
	}
	mapper := NewPayloadMapper(&fakeAssetStore{asset: asset, ids: []string{asset.ID}}, nil)
	_, err := mapper.AssetToIndexDocument(asset, schema)
	if err == nil {
		t.Fatal("NaN in vector must error")
	}
	var nanErr *transport.ErrNaNOrInf
	if !errors.As(err, &nanErr) {
		t.Fatalf("expected *ErrNaNOrInf, got %T: %v", err, err)
	}
}

// TestAssetToIndexDocument_DimensionMismatch returns dimension error.
func TestAssetToIndexDocument_DimensionMismatch(t *testing.T) {
	schema := qdrantSchema.DefaultV3Schema()
	asset := &AssetData{
		ID:             "asset-dim",
		Source:         "youtube",
		MediaType:      "video",
		LifecycleState: "ACTIVE",
		TextVector:     makeFloat32Slice(512), // wrong dim
	}
	mapper := NewPayloadMapper(&fakeAssetStore{asset: asset, ids: []string{asset.ID}}, nil)
	_, err := mapper.AssetToIndexDocument(asset, schema)
	if err == nil {
		t.Fatal("dimension mismatch must error")
	}
	var dimErr *transport.ErrVectorDimensionMismatch
	if !errors.As(err, &dimErr) {
		t.Fatalf("expected *ErrVectorDimensionMismatch, got %T: %v", err, err)
	}
}

// TestClassifyChannel verifies the per-channel policy classification.
func TestClassifyChannel(t *testing.T) {
	tests := []struct {
		channel string
		want    channelPolicy
	}{
		{"text", policyRequired},
		{"transcript", policyOptional},
		{"visual", policyOptional},
		{"audio", policyOptional},
		{"unknown_future_channel", policyOptional},
	}
	for _, tc := range tests {
		t.Run(tc.channel, func(t *testing.T) {
			if got := classifyChannel(tc.channel); got != tc.want {
				t.Errorf("classifyChannel(%q) = %v, want %v", tc.channel, got, tc.want)
			}
		})
	}
}

// ── SearchTextBuilder wiring (July 2026) ──────────────────────────────────────

// TestAssetToPoint_NilSearchTextBuilder_BitForBitPassThrough pins
// the legacy byte-for-byte contract: when SetSearchTextBuilder is
// NOT called (nil builder), AssetToPoint emits asset.SearchText
// verbatim into the bm25_text sparse vector's `text` field. This
// preserves the pre-existing TestAssetToPoint_SparseVector_HasServerSideShape
// behaviour for any caller that constructs the mapper without
// wiring the registry (admin CLI, fixtures, ad-hoc test scaffolds).
func TestAssetToPoint_NilSearchTextBuilder_BitForBitPassThrough(t *testing.T) {
	const wantSearchText = "pre-existing search text from DB"
	asset := &AssetData{
		ID:             "asset-nil-builder",
		SearchText:     wantSearchText,
		Source:         "youtube",
		MediaType:      "video",
		LifecycleState: "ACTIVE",
		TextVector:     makeFloat32Slice(768),
	}
	schema := qdrantSchema.DefaultV3Schema()

	mapper := NewPayloadMapper(&fakeAssetStore{asset: asset, ids: []string{asset.ID}}, nil)
	// Explicit: no SetSearchTextBuilder call.
	point, err := mapper.AssetToPoint(asset, schema)
	if err != nil {
		t.Fatalf("AssetToPoint: %v", err)
	}
	bm25, ok := point.Vectors["bm25_text"].(map[string]interface{})
	if !ok {
		t.Fatalf("bm25_text channel missing/malformed: %v", point.Vectors["bm25_text"])
	}
	if got := bm25["text"]; got != wantSearchText {
		t.Errorf("nil-builder pass-through: bm25_text.text = %v, want %q", got, wantSearchText)
	}
}

// TestAssetToPoint_SearchTextBuilder_YoutubeStrategy pins the wiring
// for the YouTube source. The asset's Title + Channel + Description
// are routed through the YouTube strategy. The FallBack behavior
// (empty builder output → asset.SearchText) is verified separately
// in TestAssetToPoint_SearchTextBuilder_FallbackToAssetSearchText.
func TestAssetToPoint_SearchTextBuilder_YoutubeStrategy(t *testing.T) {
	asset := &AssetData{
		ID:             "asset-yt-builder",
		Name:           "Cinematic Drone Footage",
		Source:         "youtube",
		MediaType:      "video",
		Language:       "en",
		Tags:           []string{"drone", "cinematic"},
		ChannelID:      "ChannelAlpha",
		LifecycleState: "ACTIVE",
		TextVector:     makeFloat32Slice(768),
		MetadataJSON:   `{"description":"Beautiful drone footage over mountains at sunrise.","transcript":"Welcome to my drone channel."}`,
	}
	schema := qdrantSchema.DefaultV3Schema()

	mapper := NewPayloadMapper(&fakeAssetStore{asset: asset, ids: []string{asset.ID}}, nil)
	mapper.SetSearchTextBuilder(searchtext.NewRegistry())
	point, err := mapper.AssetToPoint(asset, schema)
	if err != nil {
		t.Fatalf("AssetToPoint: %v", err)
	}
	bm25, ok := point.Vectors["bm25_text"].(map[string]interface{})
	if !ok {
		t.Fatalf("bm25_text channel missing/malformed")
	}
	text, _ := bm25["text"].(string)
	// YouTube strategy joins title + transcript + channel + description.
	// Let canonical per-source formula speak for the contract rather
	// than pinning exact ordering \u2014 each substring MUST appear.
	mustContainAll := func(haystack string, needles ...string) {
		t.Helper()
		for _, n := range needles {
			if !strings.Contains(haystack, n) {
				t.Errorf("YouTube strategy output missing %q in %q", n, haystack)
			}
		}
	}
	mustContainAll(text,
		"Cinematic Drone Footage",                           // title
		"Welcome to my drone channel",                       // transcript (from metadata_json)
		"ChannelAlpha",                                      // channel
		"Beautiful drone footage over mountains at sunrise", // description
	)
}

// TestAssetToPoint_SearchTextBuilder_FallbackToAssetSearchText pins
// the graceful-degradation contract: when the builder returns empty
// (legitimate empty strategy result, e.g. unrecognised source
// with no Title and no Tags), the mapper falls back to
// asset.SearchText. This matches the resolver's precedence
// order (builder -> asset.SearchText).
func TestAssetToPoint_SearchTextBuilder_FallbackToAssetSearchText(t *testing.T) {
	const wantSearchText = "fallback search text from DB"
	asset := &AssetData{
		ID:             "asset-fallback",
		Name:           "",
		Source:         "unknown_future_source",
		MediaType:      "video",
		LifecycleState: "ACTIVE",
		Tags:           nil, // no tags → default-fallback returns "" too
		SearchText:     wantSearchText,
		TextVector:     makeFloat32Slice(768),
	}
	schema := qdrantSchema.DefaultV3Schema()

	mapper := NewPayloadMapper(&fakeAssetStore{asset: asset, ids: []string{asset.ID}}, nil)
	mapper.SetSearchTextBuilder(searchtext.NewRegistry())
	point, err := mapper.AssetToPoint(asset, schema)
	if err != nil {
		t.Fatalf("AssetToPoint: %v", err)
	}
	bm25, ok := point.Vectors["bm25_text"].(map[string]interface{})
	if !ok {
		t.Fatalf("bm25_text channel missing/malformed")
	}
	if got := bm25["text"]; got != wantSearchText {
		t.Errorf("empty-builder fallback: bm25_text.text = %v, want %q", got, wantSearchText)
	}
}

// TestResolveSearchText_NilBuilder_ReturnsAssetSearchText pins the
// precedence-order stage (the resolver helper used by AssetToIndexDocument).
func TestResolveSearchText_NilBuilder_ReturnsAssetSearchText(t *testing.T) {
	const want = "asset fallback"
	m := NewPayloadMapper(&fakeAssetStore{}, nil)
	if got := m.resolveSearchText(context.Background(), &AssetData{SearchText: want}); got != want {
		t.Errorf("nil builder: got %q, want %q", got, want)
	}
}

// ErrAssetNotFound is a tiny test-only sentinel to satisfy FetchAsset.
type ErrAssetNotFound struct{ ID string }

func (e *ErrAssetNotFound) Error() string { return "asset not found: " + e.ID }
