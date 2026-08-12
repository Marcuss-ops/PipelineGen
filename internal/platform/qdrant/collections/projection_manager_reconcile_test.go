package collections

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	qdrantSchema "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
	"go.uber.org/zap"
)

func TestProjectionManager_ReconcileCrashAfterAliasSwitch(t *testing.T) {
	schema := projectionTestSchema()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/aliases":
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"aliases": []map[string]string{{
				"alias_name": "media_assets_current", "collection_name": "candidate-1",
			}}}})
		case r.Method == http.MethodGet && r.URL.Path == "/collections/candidate-1":
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{
				"status":       "green",
				"points_count": 1,
				"config": map[string]any{"params": map[string]any{"vectors": map[string]any{
					"text": map[string]any{"size": 768, "distance": "Cosine"},
				}}},
				"payload_schema": map[string]any{},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cm := NewProjectionManager(transport.NewClient(&qdrantSchema.Config{BaseURL: server.URL, Timeout: 5}, zap.NewNop()), schema, zap.NewNop())
	if err := cm.BeginProjection(context.Background(), "candidate-build", "candidate-1", 0); err != nil {
		t.Fatal(err)
	}
	if err := cm.transitionProjection(context.Background(), "candidate-build", capregistry.ProjectionValidating); err != nil {
		t.Fatal(err)
	}
	if err := cm.transitionProjection(context.Background(), "candidate-build", capregistry.ProjectionReady); err != nil {
		t.Fatal(err)
	}

	if err := cm.ReconcileProjection(context.Background()); err != nil {
		t.Fatalf("reconcile should repair READY state after alias switch: %v", err)
	}
	status, err := cm.GetStatus("candidate-build")
	if err != nil {
		t.Fatal(err)
	}
	if status != capregistry.ProjectionActive {
		t.Fatalf("reconciled status=%s, want ACTIVE", status)
	}
}
