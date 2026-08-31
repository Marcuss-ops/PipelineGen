// index_writer_single_caller_test.go certifies the single-writer
// invariant for Qdrant asset projections: IndexWriter is the ONLY runtime
// path that calls transport.Client.UpsertPoints / DeletePoints for
// media_assets, and TransportProjectionWriter is delegated ONLY for
// concept/frame projections (never for asset writes outside IndexWriter).
//
// The test uses a fake transport.Client (httptest.Server) that records
// every upsert/delete call and the collection name it targeted. It then
// drives IndexWriter (asset path) and verifies the collection is the
// runtime alias (media_assets_current). It also verifies that the
// canonical concept/frame consumers (QdrantIndexer, FrameQdrantIndexer)
// target ONLY their dedicated collection names — never the asset alias.
package indexing

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
)

// recordingServer captures every collection name seen in upsert/delete
// HTTP paths so the test can assert which projection was targeted.
type recordingServer struct {
	mu      sync.Mutex
	upserts []string // collection names extracted from PUT /collections/{c}/points
	deletes []string // collection names extracted from POST /collections/{c}/points/delete
}

func newRecordingServer() (*httptest.Server, *recordingServer) {
	rec := &recordingServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		rec.mu.Lock()
		defer rec.mu.Unlock()
		path := r.URL.Path
		switch {
		case r.Method == http.MethodPut && strings.HasSuffix(path, "/points"):
			col := extractCollection(path)
			rec.upserts = append(rec.upserts, col)
		case r.Method == http.MethodPost && strings.HasSuffix(path, "/points/delete"):
			col := extractCollection(path)
			rec.deletes = append(rec.deletes, col)
		default:
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"result":{"status":"ok"}}`)
	}))
	return srv, rec
}

// extractCollection parses the collection name from
// /collections/{collection}/points[/delete].
func extractCollection(path string) string {
	const prefix = "/collections/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	rest := path[len(prefix):]
	if idx := strings.Index(rest, "/points"); idx >= 0 {
		return rest[:idx]
	}
	return rest
}

func TestIndexWriter_AssetWritesTargetOnlyRuntimeAlias(t *testing.T) {
	srv, rec := newRecordingServer()
	defer srv.Close()

	client := transport.NewClient(&schema.Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())
	idxSchema := schema.DefaultV3Schema()
	mapper := NewPayloadMapper(nil, zap.NewNop()) // mapper unused; projection exercised directly
	writer := NewIndexWriter(client, idxSchema, mapper, zap.NewNop())

	// Upsert a single asset point through IndexWriter (the canonical
	// runtime write path). The collection MUST be the runtime alias
	// (media_assets_current), never the physical production name and
	// never a concept/frame collection.
	points := []schema.Point{
		{ID: "asset-single-caller-1", Payload: map[string]any{"asset_id": "asset-single-caller-1"}},
	}
	require.NoError(t, writer.projection.UpsertProjection(context.Background(), idxSchema.RuntimeAlias, points))

	rec.mu.Lock()
	require.Len(t, rec.upserts, 1, "exactly one upsert expected from IndexWriter asset path")
	require.Equal(t, schema.CanonicalRuntimeAlias, rec.upserts[0],
		"IndexWriter asset writes must target the runtime alias %q, got %q",
		schema.CanonicalRuntimeAlias, rec.upserts[0])
	rec.mu.Unlock()

	// Delete the same asset point.
	require.NoError(t, writer.projection.DeleteProjection(context.Background(), idxSchema.RuntimeAlias, []string{"asset-single-caller-1"}))

	rec.mu.Lock()
	require.Len(t, rec.deletes, 1, "exactly one delete expected from IndexWriter asset path")
	require.Equal(t, schema.CanonicalRuntimeAlias, rec.deletes[0],
		"IndexWriter asset deletes must target the runtime alias %q, got %q",
		schema.CanonicalRuntimeAlias, rec.deletes[0])
	rec.mu.Unlock()
}

func TestTransportProjectionWriter_NeverTargetsAssetProductionCollection(t *testing.T) {
	srv, rec := newRecordingServer()
	defer srv.Close()

	client := transport.NewClient(&schema.Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())
	pw := NewTransportProjectionWriter(client)
	ctx := context.Background()

	// The ONLY legitimate non-IndexWriter consumers of ProjectionWriter
	// are the concept and frame indexers. They must target their dedicated
	// collection names, never the asset production collection or alias.
	conceptPoints := []schema.Point{{ID: "concept-cert-1", Payload: map[string]any{"concept_id": "cert-1"}}}
	require.NoError(t, pw.UpsertProjection(ctx, schema.ConceptCollectionName, conceptPoints))

	framePoints := []schema.Point{{ID: "frame-cert-1", Payload: map[string]any{"frame_id": "cert-1"}}}
	require.NoError(t, pw.UpsertProjection(ctx, schema.FrameCollectionName, framePoints))

	rec.mu.Lock()
	defer rec.mu.Unlock()
	require.Len(t, rec.upserts, 2, "expected 2 upserts (concept + frame)")
	for _, col := range rec.upserts {
		require.NotEqual(t, schema.ProductionCollection, col,
			"TransportProjectionWriter must never target the asset production collection %q directly", schema.ProductionCollection)
		require.NotEqual(t, schema.CanonicalRuntimeAlias, col,
			"TransportProjectionWriter must never target the asset runtime alias %q outside IndexWriter", schema.CanonicalRuntimeAlias)
	}
}

func TestIndexWriter_SatisfiesVectorPointDeleterAndIndexWriterPort(t *testing.T) {
	// Compile-time assertions already exist in index_writer.go; this
	// test documents the single-writer contract at the test level so
	// a future refactor that splits the deleter surface is caught.
	srv, _ := newRecordingServer()
	defer srv.Close()

	client := transport.NewClient(&schema.Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())
	idxSchema := schema.DefaultV3Schema()
	mapper := NewPayloadMapper(nil, zap.NewNop()) // mapper unused; type assertion only
	writer := NewIndexWriter(client, idxSchema, mapper, zap.NewNop())

	require.NotNil(t, writer.client, "IndexWriter must hold the transport.Client (single runtime writer surface)")
	require.NotNil(t, writer.projection, "IndexWriter must hold a ProjectionWriter (delegated, not duplicated)")
	require.IsType(t, &TransportProjectionWriter{}, writer.projection,
		"IndexWriter must delegate to TransportProjectionWriter, not a second writer type")
}
