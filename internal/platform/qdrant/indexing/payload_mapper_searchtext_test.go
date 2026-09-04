// Package indexing — SEARCHTEXT payload-mapper tests.
package indexing

import (
	"context"
	"strings"
	"testing"

	qdrantSchema "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/searchtext"
)

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
		t.Fatalf("PR2: vectors.bm25_text MUST be present (server-side BM25 inference requires the vector field, not the payload); got channels %v", mapKeys(point.Vectors))
	}
	m, ok := bm25.(map[string]interface{})
	if !ok {
		t.Fatalf("PR2: vectors.bm25_text must be {text, model} shape; got %T", bm25)
	}
	if m["text"] != asset.SearchText {
		t.Errorf("vectors.bm25_text.text = %v, want %q", m["text"], asset.SearchText)
	}
	if m["model"] != qdrantSchema.DefaultSparseModel {
		t.Errorf("vectors.bm25_text.model = %v, want %q (qdrantSchema.DefaultSparseModel)", m["model"])
	}
}

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

func TestAssetToPoint_VisualVector_CanonicalDimensions(t *testing.T) {
	asset := &AssetData{
		ID:             "asset-visual-canonical",
		SearchText:     "canonical visual clip",
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
		t.Fatalf("expected visual channel to be present")
	}
	vec, ok := visual.([]float32)
	if !ok {
		t.Fatalf("visual channel has unexpected type %T", visual)
	}
	if got := len(vec); got != 1152 {
		t.Fatalf("visual vector length = %d, want 1152", got)
	}
}

func TestAssetToPoint_TranscriptChannel_DroppedWhenAbsent(t *testing.T) {
	asset := &AssetData{
		ID:             "asset-tx",
		SearchText:     "transcript text",
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
	if _, present := point.Vectors["transcript"]; present {
		t.Errorf("PR2 (verdetto §10): transcript channel MUST be dropped when absent; pre-PR2 fell back to TextVector. Got %v", point.Vectors["transcript"])
	}
}

func TestAssetToPoint_TranscriptChannel_PreservedWhenPresent(t *testing.T) {
	textVec := makeFloat32Slice(768)
	transcriptVec := makeFloat32Slice(768)
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
	mustContainAll := func(haystack string, needles ...string) {
		t.Helper()
		for _, n := range needles {
			if !strings.Contains(haystack, n) {
				t.Errorf("YouTube strategy output missing %q in %q", n, haystack)
			}
		}
	}
	mustContainAll(text,
		"Cinematic Drone Footage",
		"Welcome to my drone channel",
		"ChannelAlpha",
		"Beautiful drone footage over mountains at sunrise",
	)
}

func TestAssetToPoint_SearchTextBuilder_FallbackToAssetSearchText(t *testing.T) {
	const wantSearchText = "fallback search text from DB"
	asset := &AssetData{
		ID:             "asset-fallback",
		Name:           "",
		Source:         "unknown_future_source",
		MediaType:      "video",
		LifecycleState: "ACTIVE",
		Tags:           nil,
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

func TestResolveSearchText_NilBuilder_ReturnsAssetSearchText(t *testing.T) {
	const want = "asset fallback"
	m := NewPayloadMapper(&fakeAssetStore{}, nil)
	if got := m.resolveSearchText(context.Background(), &AssetData{SearchText: want}); got != want {
		t.Errorf("nil builder: got %q, want %q", got, want)
	}
}

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
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	recorder := &ctxRecordingBuilder{}
	mapper := NewPayloadMapper(&fakeAssetStore{asset: asset, ids: []string{asset.ID}}, nil)
	mapper.SetSearchTextBuilder(recorder)

	doc, err := mapper.AssetToIndexDocument(ctx, asset, schema)
	if err != nil {
		t.Fatalf("AssetToIndexDocument with cancelled ctx must not error (graceful degradation); got %v", err)
	}
	if doc == nil {
		t.Fatal("AssetToIndexDocument must return a non-nil IndexDocument even on cancelled ctx")
	}
	if doc.SearchText != recorder.capturedText {
		t.Errorf("SearchText: got %q, want %q (builder output when ctx not cancelled)", doc.SearchText, recorder.capturedText)
	}
	if recorder.capturedCtx == nil {
		t.Error("SearchTextBuilder.Build was NEVER called — ctx was not propagated (possible context.Background() bypass)")
		return
	}
	select {
	case <-recorder.capturedCtx.Done():
	default:
		t.Error("SearchTextBuilder.Build received a ctx that is NOT cancelled — possible context.Background() bypass")
	}
}

func TestResolveSearchText_PassesCallerContext_NotBackground(t *testing.T) {
	type ctxKey struct{}
	ctxKeySentinel := ctxKey{}
	ctx := context.WithValue(context.Background(), ctxKeySentinel, "marker-value")
	asset := &AssetData{ID: "asset-ctx-prop", SearchText: "fallback"}

	recorder := &ctxRecordingBuilder{}
	mapper := NewPayloadMapper(&fakeAssetStore{}, nil)
	mapper.SetSearchTextBuilder(recorder)
	got := mapper.resolveSearchText(ctx, asset)

	if recorder.capturedCtx == nil {
		t.Error("SearchTextBuilder.Build was NEVER called — ctx was not propagated")
		return
	}
	if recorder.capturedCtx != ctx {
		t.Errorf("SearchTextBuilder.Build received a DIFFERENT ctx than the caller's — possible context.Background() or context.WithoutCancel bypass")
	}
	if v := recorder.capturedCtx.Value(ctxKeySentinel); v != "marker-value" {
		t.Errorf("captured ctx lost the marker value — possible context.Background() replacement: got %v", v)
	}
	_ = got
}

func TestResolveSearchText_BackgroundContext_NotReplaced(t *testing.T) {
	ctx := context.Background()
	asset := &AssetData{ID: "asset-bg-ctx", SearchText: "fallback"}

	recorder := &ctxRecordingBuilder{}
	mapper := NewPayloadMapper(&fakeAssetStore{}, nil)
	mapper.SetSearchTextBuilder(recorder)
	got := mapper.resolveSearchText(ctx, asset)

	if recorder.capturedCtx == nil {
		t.Error("SearchTextBuilder.Build was NEVER called")
		return
	}
	if recorder.capturedCtx != ctx {
		t.Error("SearchTextBuilder.Build received a different ctx — even context.Background() must be forwarded verbatim")
	}
	_ = got
}
