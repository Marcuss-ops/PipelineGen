// Package indexing — payload_mapper_searchtext_test.go
// (PR-PAYLOAD-MAPPER-SPLIT-mirror, July 2026).
//
// SEARCHTEXT test surface (mirror of payload_mapper_searchtext.go
// production split). Per godlike/06 SSOT (one canonical owner per
// fact), this file is the SOLE canonical owner of the 12 Test funcs
// that exercise the search-text wiring + ctx-propagation contracts:
//
//   - 2 TestAssetToPoint_SparseVector_* sparse-vector wire-shape tests
//     (PR2 server-side BM25 + empty-SearchText graceful-degradation)
//   - 1 TestAssetToPoint_VisualVector_1152ResamplesToSchema legacy
//     compatibility shim (1152-d → 768-d)
//   - 2 TestAssetToPoint_TranscriptChannel_* PR2 dropped-vs-preserved
//     contract pins
//   - 1 TestAssetToPoint_NilSearchTextBuilder_BitForBitPassThrough
//     byte-for-byte pass-through contract (no `SetSearchTextBuilder`)
//   - 2 TestAssetToPoint_SearchTextBuilder_* wiring tests
//     (YouTube strategy + empty-builder fallback)
//   - 3 TestResolveSearchText_* precedence + ctx-propagation tests
//     (nil → AssetSearchText, cancelled ctx graceful-degradation,
//     caller-ctx forwarded verbatim, Background forwarded verbatim)
//   - 1 TestAssetToIndexDocument_CancelledContext_DegradesGracefully
//     AZIONE 1 ctx-propagation pino (cancelled ctx → log Warn +
//     fall through to asset.SearchText)
//
// All test funcs target the SEARCHTEXT production code only
// (payload_mapper_searchtext.go::parseMetadataJSON +
// ::buildSearchTextInput + ::resolveSearchText + the AssetToIndexDocument /
// AssetToPoint call sites that drive them). They share makeFloat32Slice,
// requirePointID, mapKeys, mapKeysVec, fakeAssetStore, and
// ctxRecordingBuilder from payload_mapper_testhelpers_test.go via
// same-package visibility. godlike/07 minimum-blast-radius: pure
// code-motion, no logic change.
package indexing

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"context"
	"strings"
	"testing"

	qdrantSchema "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/searchtext"
)

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
	point, err := mapper.AssetToPoint(context.Background(), asset, schema)
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
	point, err := mapper.AssetToPoint(context.Background(), asset, schema)
	if err != nil {
		t.Fatalf("AssetToPoint error: %v", err)
	}
	if _, present := point.Vectors["bm25_text"]; present {
		t.Errorf("PR2: empty SearchText MUST drop the bm25_text channel; got %v", point.Vectors["bm25_text"])
	}
}

// TestAssetToPoint_VisualVector_1152ResamplesToSchema pins the legacy
// compatibility shim for older visual embeddings. Some historical YouTube
// assets still carry 1152-d vectors; the mapper must normalize them to the
// current 768-d schema instead of rejecting the point outright.
func TestAssetToPoint_VisualVector_1152ResamplesToSchema(t *testing.T) {
	asset := &AssetData{
		ID:             "asset-visual-legacy",
		SearchText:     "legacy visual clip",
		Source:         "youtube",
		MediaType:      "video",
		LifecycleState: "ACTIVE",
		TextVector:     makeFloat32Slice(768),
		VisualVector:   makeFloat32Slice(1152),
	}
	schema := qdrantSchema.DefaultV3Schema()

	mapper := NewPayloadMapper(&fakeAssetStore{asset: asset, ids: []string{asset.ID}}, nil)
	point, err := mapper.AssetToPoint(context.Background(), asset, schema)
	if err != nil {
		t.Fatalf("AssetToPoint error: %v", err)
	}
	visual, ok := point.Vectors["visual"]
	if !ok {
		t.Fatalf("expected visual channel to be present after resampling")
	}
	vec, ok := visual.([]float32)
	if !ok {
		t.Fatalf("visual channel has unexpected type %T", visual)
	}
	if got := len(vec); got != 768 {
		t.Fatalf("visual vector length = %d, want 768", got)
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
	point, err := mapper.AssetToPoint(context.Background(), asset, schema)
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
	point, err := mapper.AssetToPoint(context.Background(), asset, schema)
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
	point, err := mapper.AssetToPoint(context.Background(), asset, schema)
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
	point, err := mapper.AssetToPoint(context.Background(), asset, schema)
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
	// than pinning exact ordering — each substring MUST appear.
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
	point, err := mapper.AssetToPoint(context.Background(), asset, schema)
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

// ══════════════════════════════════════════════════════════════════════════
// AZIONE 7 (July 2026) — context propagation TDD tests.
// ══════════════════════════════════════════════════════════════════════════

// TestAssetToIndexDocument_CancelledContext_DegradesGracefully verifies
// that AssetToIndexDocument passes the caller's ctx to the SearchTextBuilder
// (AZIONE 1 fix: ctx, NOT context.Background()). A cancelled context causes
// the builder to return context.Canceled; resolveSearchText logs Warn and
// falls through to asset.SearchText — the key contract is that the real ctx
// IS propagated, not silently replaced.
func TestAssetToIndexDocument_CancelledContext_DegradesGracefully(t *testing.T) {
	schema := qdrantSchema.DefaultV3Schema()
	asset := &AssetData{
		ID:             "asset-cancelled",
		Source:         "youtube",
		MediaType:      "video",
		LifecycleState: "ACTIVE",
		TextVector:     makeFloat32Slice(768),
		SearchText:     "fallback search text",
	}

	// Create a context that is ALREADY cancelled.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	recorder := &ctxRecordingBuilder{}
	mapper := NewPayloadMapper(&fakeAssetStore{asset: asset, ids: []string{asset.ID}}, nil)
	mapper.SetSearchTextBuilder(recorder)

	doc, err := mapper.AssetToIndexDocument(ctx, asset, schema)
	// Contract: AssetToIndexDocument MUST NOT panic on a cancelled context.
	// resolveSearchText logs Warn + falls through to asset.SearchText.
	// The doc.SearchText field is populated from the fallback.
	if err != nil {
		t.Fatalf("AssetToIndexDocument with cancelled ctx must not error (graceful degradation); got %v", err)
	}
	if doc == nil {
		t.Fatal("AssetToIndexDocument must return a non-nil IndexDocument even on cancelled ctx")
	}
	if doc.SearchText != recorder.capturedText {
		t.Errorf("SearchText: got %q, want %q (builder output when ctx not cancelled)", doc.SearchText, recorder.capturedText)
	}
	// The real contract: the ctx passed to Build MUST be the cancelled ctx,
	// NOT context.Background(). The recording builder captures the ctx identity.
	if recorder.capturedCtx == nil {
		t.Error("SearchTextBuilder.Build was NEVER called — ctx was not propagated (possible context.Background() bypass)")
		return
	}
	// Verify the captured ctx is CANCELLED (proves the real ctx was passed,
	// not a fresh context.Background()).
	select {
	case <-recorder.capturedCtx.Done():
		// PASS: the real cancelled ctx was propagated.
	default:
		t.Error("SearchTextBuilder.Build received a ctx that is NOT cancelled — possible context.Background() bypass")
	}
}

// TestResolveSearchText_PassesCallerContext_NotBackground verifies that
// resolveSearchText passes the caller's ctx to SearchTextBuilder.Build,
// NOT a fresh context.Background() (the AZIONE 1 fix). A mock builder
// records the received ctx and the test asserts it is the SAME ctx value
// passed by the caller.
func TestResolveSearchText_PassesCallerContext_NotBackground(t *testing.T) {
	type ctxKey struct{}
	ctxKeySentinel := ctxKey{}
	ctx := context.WithValue(context.Background(), ctxKeySentinel, "marker-value")

	asset := &AssetData{
		ID:         "asset-ctx-prop",
		SearchText: "fallback",
	}

	recorder := &ctxRecordingBuilder{}
	mapper := NewPayloadMapper(&fakeAssetStore{}, nil)
	mapper.SetSearchTextBuilder(recorder)

	got := mapper.resolveSearchText(ctx, asset)

	// The resolved text is the recorder's return value (or fallback).
	// The key invariant: the ctx passed to Build MUST be the caller's ctx.
	if recorder.capturedCtx == nil {
		t.Error("SearchTextBuilder.Build was NEVER called — ctx was not propagated")
		return
	}
	if recorder.capturedCtx != ctx {
		t.Errorf("SearchTextBuilder.Build received a DIFFERENT ctx than the caller's — possible context.Background() or context.WithoutCancel bypass")
	}
	// Sanity: Build DID get the caller's ctx with the marker value.
	if v := recorder.capturedCtx.Value(ctxKeySentinel); v != "marker-value" {
		t.Errorf("captured ctx lost the marker value — possible context.Background() replacement: got %v", v)
	}
	_ = got // explicit marker for grep forensics
}

// TestResolveSearchText_BackgroundContext_NotReplaced verifies that even
// when the caller passes context.Background() itself (legitimate, e.g.
// composition roots, admin CLI), resolveSearchText forwards it verbatim
// — it does NOT create a fresh copy.
func TestResolveSearchText_BackgroundContext_NotReplaced(t *testing.T) {
	ctx := context.Background()
	asset := &AssetData{
		ID:         "asset-bg-ctx",
		SearchText: "fallback",
	}

	recorder := &ctxRecordingBuilder{}
	mapper := NewPayloadMapper(&fakeAssetStore{}, nil)
	mapper.SetSearchTextBuilder(recorder)

	got := mapper.resolveSearchText(ctx, asset)

	if recorder.capturedCtx == nil {
		t.Error("SearchTextBuilder.Build was NEVER called")
		return
	}
	// context.Background() is intentionally forwarded — the caller owns
	// the ctx decision; the mapper must NOT second-guess.
	if recorder.capturedCtx != ctx {
		t.Error("SearchTextBuilder.Build received a different ctx — even context.Background() must be forwarded verbatim")
	}
	_ = got
}
