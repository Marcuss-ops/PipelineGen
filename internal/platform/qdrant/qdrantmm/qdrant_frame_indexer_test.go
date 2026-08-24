// Package qdrantmm — qdrant_frame_indexer_test.go pins the
// Fase 4.1 KeyframeVisualIndexer contract.
//
//  1. IndexKeyframe writes a canonical `frame-{videoID}-{tsMs}`
//     point to pipelinegen_media_frames with a 768d visual vector.
//  2. Empty videoID / empty vector / wrong dim → typed-sentinel
//     envelope (ErrInvalidBindingInput / ErrLinkerEmbeddingFailed
//     / ErrSemanticNotConfigured).
//  3. Deterministic point ID: same (videoID, tsMs) → same point
//     ID across calls (Upsert idempotence).
package qdrantmm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediamemory"
	qdrantschema "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
)

// mockFrameQdrantServer emulates /collections/{name}/points PUT
// (UpsertPoints) for the frame collection. Records upserts so
// tests can assert point ID + payload shape.
type mockFrameQdrantServer struct {
	t        *testing.T
	srv      *httptest.Server
	mu       sync.Mutex
	upserted map[string]map[string]any // collection → pointID → payload
}

func newMockFrameQdrantServer(t *testing.T) *mockFrameQdrantServer {
	mq := &mockFrameQdrantServer{
		t:        t,
		upserted: make(map[string]map[string]any),
	}
	mq.srv = httptest.NewServer(http.HandlerFunc(mq.handle))
	return mq
}

func (mq *mockFrameQdrantServer) URL() string { return mq.srv.URL }
func (mq *mockFrameQdrantServer) Close()      { mq.srv.Close() }

func (mq *mockFrameQdrantServer) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPut {
		http.NotFound(w, r)
		return
	}
	if !strings.HasSuffix(r.URL.Path, "/points") {
		http.NotFound(w, r)
		return
	}
	coll := collectionFromPath(r.URL.Path)
	var req struct {
		Points []struct {
			ID      string         `json:"id"`
			Vector  map[string]any `json:"vector"`
			Payload map[string]any `json:"payload"`
		} `json:"points"`
	}
	body, _ := readAllHelper(r.Body)
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	mq.mu.Lock()
	defer mq.mu.Unlock()
	bucket, ok := mq.upserted[coll]
	if !ok {
		bucket = make(map[string]any)
		mq.upserted[coll] = bucket
	}
	for _, p := range req.Points {
		bucket[p.ID] = p.Payload
	}
	fmt.Fprint(w, `{"result":{"status":"ok"}}`)
}

func readAllHelper(r interface {
	Read(p []byte) (n int, err error)
}) ([]byte, error) {
	var buf bytes.Buffer
	tmp := make([]byte, 4096)
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			buf.Write(tmp[:n])
		}
		if err != nil {
			if err.Error() == "EOF" {
				return buf.Bytes(), nil
			}
			return buf.Bytes(), err
		}
	}
}

// Test 1: IndexKeyframe writes canonical point with 768d
// visual-channel vector.
func TestFrameQdrantIndexer_IndexKeyframeWritesCanonicalPoint(t *testing.T) {
	mq := newMockFrameQdrantServer(t)
	defer mq.Close()

	client := transport.NewClient(
		&qdrantschema.Config{BaseURL: mq.URL(), Timeout: 5},
		zap.NewNop(),
	)
	idx := NewFrameQdrantIndexer(client, zap.NewNop())

	vec := make([]float32, FrameVectorDim)
	for i := range vec {
		vec[i] = float32(i+1) / float32(FrameVectorDim)
	}

	if err := idx.IndexKeyframe(
		context.Background(),
		"video-abc-123",
		4500,
		"asset-abc-001",
		"it",
		vec,
		"siglip-so400m-patch14-384",
	); err != nil {
		t.Fatalf("IndexKeyframe: %v", err)
	}

	mq.mu.Lock()
	defer mq.mu.Unlock()
	bucket, ok := mq.upserted[qdrantschema.FrameCollectionName]
	if !ok {
		t.Fatalf("expected writes to %q, got none", qdrantschema.FrameCollectionName)
	}
	wantID := qdrantschema.FramePointID("video-abc-123", 4500)
	point, ok := bucket[wantID]
	if !ok {
		t.Fatalf("expected point ID %q in bucket, got keys=%v", wantID, keysOf(bucket))
	}
	pmap, ok := point.(map[string]any)
	if !ok {
		t.Fatalf("payload is %T, want map[string]any", point)
	}
	if pmap["video_id"] != "video-abc-123" {
		t.Fatalf("payload.video_id=%v, want video-abc-123", pmap["video_id"])
	}
	if pmap["asset_id"] != "asset-abc-001" {
		t.Fatalf("payload.asset_id=%v, want asset-abc-001", pmap["asset_id"])
	}
	if pmap["language"] != "it" {
		t.Fatalf("payload.language=%v, want it", pmap["language"])
	}
	if pmap["embedding_version"] != "siglip-so400m-patch14-384" {
		t.Fatalf("payload.embedding_version=%v, want model name", pmap["embedding_version"])
	}
}

// Test 2: deterministic point ID across calls (idempotence).
func TestFrameQdrantIndexer_DeterministicPointID(t *testing.T) {
	id1 := qdrantschema.FramePointID("vid-1", 1000)
	id2 := qdrantschema.FramePointID("vid-1", 1000)
	if id1 != id2 {
		t.Fatalf("expected same point ID for same (vid, ts), got %q vs %q", id1, id2)
	}
	id3 := qdrantschema.FramePointID("vid-1", 2000)
	if id1 == id3 {
		t.Fatalf("expected different point ID for different ts, got %q", id1)
	}
	if !strings.HasPrefix(id1, "frame-") {
		t.Fatalf("expected prefix frame-, got %q", id1)
	}
}

// Test 3: empty videoID → ErrInvalidBindingInput.
func TestFrameQdrantIndexer_EmptyVideoIDReturnsInvalidBindingInput(t *testing.T) {
	mq := newMockFrameQdrantServer(t)
	defer mq.Close()
	client := transport.NewClient(
		&qdrantschema.Config{BaseURL: mq.URL(), Timeout: 5},
		zap.NewNop(),
	)
	idx := NewFrameQdrantIndexer(client, zap.NewNop())
	err := idx.IndexKeyframe(context.Background(), "", 1000, "asset", "it", makeVec(), "")
	if !errors.Is(err, mediamemory.ErrInvalidBindingInput) {
		t.Fatalf("expected ErrInvalidBindingInput, got %v", err)
	}
}

// Test 4: empty vector → ErrLinkerEmbeddingFailed.
func TestFrameQdrantIndexer_EmptyVectorReturnsLinkerEmbeddingFailed(t *testing.T) {
	mq := newMockFrameQdrantServer(t)
	defer mq.Close()
	client := transport.NewClient(
		&qdrantschema.Config{BaseURL: mq.URL(), Timeout: 5},
		zap.NewNop(),
	)
	idx := NewFrameQdrantIndexer(client, zap.NewNop())
	err := idx.IndexKeyframe(context.Background(), "v", 1000, "a", "it", nil, "")
	if !errors.Is(err, mediamemory.ErrLinkerEmbeddingFailed) {
		t.Fatalf("expected ErrLinkerEmbeddingFailed, got %v", err)
	}
}

// Test 5: wrong dim → ErrSemanticNotConfigured.
func TestFrameQdrantIndexer_WrongDimReturnsSemanticNotConfigured(t *testing.T) {
	mq := newMockFrameQdrantServer(t)
	defer mq.Close()
	client := transport.NewClient(
		&qdrantschema.Config{BaseURL: mq.URL(), Timeout: 5},
		zap.NewNop(),
	)
	idx := NewFrameQdrantIndexer(client, zap.NewNop())
	wrong := make([]float32, 512)
	err := idx.IndexKeyframe(context.Background(), "v", 1000, "a", "it", wrong, "")
	if !errors.Is(err, mediamemory.ErrSemanticNotConfigured) {
		t.Fatalf("expected ErrSemanticNotConfigured, got %v", err)
	}
}

// Test 6: nil transport returns ErrSemanticNotConfigured
// (godlike/07 fail-closed).
func TestFrameQdrantIndexer_NilTransportReturnsSemanticNotConfigured(t *testing.T) {
	idx := NewFrameQdrantIndexer(nil, zap.NewNop())
	err := idx.IndexKeyframe(context.Background(), "v", 1000, "a", "it", makeVec(), "")
	if !errors.Is(err, mediamemory.ErrSemanticNotConfigured) {
		t.Fatalf("expected ErrSemanticNotConfigured, got %v", err)
	}
}

// Test 7: FrameIndexSchema exposes canonical 768d visual vector
// + canonical payload indexes.
func TestFrameIndexSchema_CanonicalShape(t *testing.T) {
	sch := qdrantschema.FrameIndexSchema()
	if sch.PhysicalName != qdrantschema.FrameCollectionName {
		t.Fatalf("PhysicalName=%q, want %q", sch.PhysicalName, qdrantschema.FrameCollectionName)
	}
	if sch.Version != qdrantschema.FrameEmbeddingVersion {
		t.Fatalf("Version=%q, want %q", sch.Version, qdrantschema.FrameEmbeddingVersion)
	}
	if !sch.HasChannel(qdrantschema.FrameVectorName) {
		t.Fatalf("expected %q channel", qdrantschema.FrameVectorName)
	}
	if len(sch.DenseVectors) != 1 {
		t.Fatalf("expected 1 dense vector, got %d", len(sch.DenseVectors))
	}
	v := sch.DenseVectors[0]
	if v.Dimensions != 768 {
		t.Fatalf("expected 768d visual, got %d", v.Dimensions)
	}
	if v.Distance != "Cosine" || !v.Normalized {
		t.Fatalf("expected Cosine+normalized, got distance=%q normalized=%v", v.Distance, v.Normalized)
	}
	// Payload index keys present.
	requiredKeys := []string{"frame_id", "video_id", "asset_id", "ts_ms", "language", "embedding_version"}
	for _, k := range requiredKeys {
		found := false
		for _, p := range sch.PayloadIndexes {
			if p.FieldName == k {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing payload index %q", k)
		}
	}
}

func makeVec() []float32 {
	v := make([]float32, FrameVectorDim)
	for i := range v {
		v[i] = float32(i+1) / float32(FrameVectorDim)
	}
	return v
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
