package indexing

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
)

func TestTransportProjectionWriter_NilClientFailsClosed(t *testing.T) {
	w := NewTransportProjectionWriter(nil)
	ctx := context.Background()
	if err := w.UpsertProjection(ctx, "c", []schema.Point{{ID: "p1"}}); !errors.Is(err, ErrProjectionUnavailable) {
		t.Fatalf("UpsertProjection nil client = %v, want ErrProjectionUnavailable", err)
	}
	if err := w.DeleteProjection(ctx, "c", []string{"p1"}); !errors.Is(err, ErrProjectionUnavailable) {
		t.Fatalf("DeleteProjection nil client = %v, want ErrProjectionUnavailable", err)
	}
}

func TestTransportProjectionWriter_DelegatesToTransport(t *testing.T) {
	var mu sync.Mutex
	var upserts, deletes []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/points"):
			upserts = append(upserts, r.URL.Path)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/points/delete"):
			deletes = append(deletes, r.URL.Path)
		default:
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"result":{"status":"ok"}}`)
	}))
	defer srv.Close()

	client := transport.NewClient(&schema.Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())
	writer := NewTransportProjectionWriter(client)
	ctx := context.Background()

	if err := writer.UpsertProjection(ctx, "media_concepts", []schema.Point{{ID: "concept-1", Payload: map[string]any{"concept_id": "1"}}}); err != nil {
		t.Fatalf("UpsertProjection: %v", err)
	}
	if err := writer.DeleteProjection(ctx, "media_concepts", []string{"concept-1"}); err != nil {
		t.Fatalf("DeleteProjection: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(upserts) != 1 || !strings.Contains(upserts[0], "media_concepts") {
		t.Fatalf("upsert paths = %v, want one media_concepts upsert", upserts)
	}
	if len(deletes) != 1 || !strings.Contains(deletes[0], "media_concepts") {
		t.Fatalf("delete paths = %v, want one media_concepts delete", deletes)
	}
}

func TestTransportProjectionWriter_EmptyBatchNoOp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("transport must not be called for an empty batch")
		http.NotFound(w, r)
	}))
	defer srv.Close()

	client := transport.NewClient(&schema.Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())
	writer := NewTransportProjectionWriter(client)
	if err := writer.UpsertProjection(context.Background(), "c", nil); err != nil {
		t.Fatalf("empty upsert: %v", err)
	}
	if err := writer.DeleteProjection(context.Background(), "c", nil); err != nil {
		t.Fatalf("empty delete: %v", err)
	}
}
