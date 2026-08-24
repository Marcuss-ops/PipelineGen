package collections

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/indexing"
	qdrantSchema "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/verification"
	"go.uber.org/zap"
)

func TestProjectionManager_ValidateRejectsMissingPoints(t *testing.T) {
	server := projectionValidationServer(t, "candidate-1", 0, "")
	defer server.Close()

	cm := NewProjectionManager(transport.NewClient(&qdrantSchema.Config{BaseURL: server.URL, Timeout: 5}, zap.NewNop()), projectionTestSchema(), zap.NewNop())
	cm.SetReindexVerifier(verification.NewReindexVerifier(cm.client, &projectionAssetStore{ids: []string{"asset-1"}}, nil, projectionTestSchema(), nil, zap.NewNop()))
	mustBeginReadyBuild(t, cm, "build-missing", "candidate-1")

	report, err := cm.ValidateProjection(context.Background(), "build-missing", 0, 1)
	if err == nil || report == nil {
		t.Fatalf("missing points must fail validation, report=%v err=%v", report, err)
	}
	projection, _ := cm.Projection("build-missing")
	if projection.Status != string(capregistry.ProjectionFailed) {
		t.Fatalf("missing points must transition to FAILED, got %s", projection.Status)
	}
}

func TestProjectionManager_ValidateRejectsOrphanPoints(t *testing.T) {
	server := projectionValidationServer(t, "candidate-1", 1, projectionPointPayload("orphan-1", "model-v1"))
	defer server.Close()

	schema := projectionTestSchema()
	cm := NewProjectionManager(transport.NewClient(&qdrantSchema.Config{BaseURL: server.URL, Timeout: 5}, zap.NewNop()), schema, zap.NewNop())
	cm.SetReindexVerifier(verification.NewReindexVerifier(cm.client, &projectionAssetStore{ids: []string{"asset-1"}}, nil, schema, nil, zap.NewNop()))
	mustBeginReadyBuild(t, cm, "build-orphan", "candidate-1")

	report, err := cm.ValidateProjection(context.Background(), "build-orphan", 0, 1)
	if err == nil || report == nil || report.OrphanCount != 1 {
		t.Fatalf("orphan point must fail with one orphan, report=%+v err=%v", report, err)
	}
	projection, _ := cm.Projection("build-orphan")
	if projection.Status != string(capregistry.ProjectionFailed) {
		t.Fatalf("orphan points must transition to FAILED, got %s", projection.Status)
	}
}

func TestProjectionManager_ValidateRejectsWrongEmbeddingModel(t *testing.T) {
	server := projectionValidationServer(t, "candidate-1", 1, projectionPointPayload("asset-1", "wrong-model"))
	defer server.Close()

	schema := projectionTestSchema()
	cm := NewProjectionManager(transport.NewClient(&qdrantSchema.Config{BaseURL: server.URL, Timeout: 5}, zap.NewNop()), schema, zap.NewNop())
	cm.SetReindexVerifier(verification.NewReindexVerifier(cm.client, &projectionAssetStore{ids: []string{"asset-1"}}, nil, schema, nil, zap.NewNop()))
	mustBeginReadyBuild(t, cm, "build-model", "candidate-1")

	report, err := cm.ValidateProjection(context.Background(), "build-model", 0, 1)
	if err == nil || report == nil || report.VersionMismatchPerChannel["text"] == 0 {
		t.Fatalf("wrong embedding model must fail validation, report=%+v err=%v", report, err)
	}
}

func TestProjectionManager_ValidateRejectsWrongDimensions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/collections/candidate-1" {
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{
				"status":       "green",
				"points_count": 1,
				"config": map[string]any{"params": map[string]any{"vectors": map[string]any{
					"text": map[string]any{"size": 512, "distance": "Cosine"},
				}}},
				"payload_schema": map[string]any{},
			}})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	schema := projectionTestSchema()
	cm := NewProjectionManager(transport.NewClient(&qdrantSchema.Config{BaseURL: server.URL, Timeout: 5}, zap.NewNop()), schema, zap.NewNop())
	mustBeginReadyBuild(t, cm, "build-dimensions", "candidate-1")

	_, err := cm.ValidateProjection(context.Background(), "build-dimensions", 0, 1)
	if err == nil {
		t.Fatal("wrong vector dimensions must fail schema validation")
	}
	projection, _ := cm.Projection("build-dimensions")
	if projection.Status != string(capregistry.ProjectionFailed) {
		t.Fatalf("wrong dimensions must transition to FAILED, got %s", projection.Status)
	}
}

func TestProjectionManager_BuildOfflineFailsProjection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "qdrant offline", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	cm := NewProjectionManager(transport.NewClient(&qdrantSchema.Config{BaseURL: server.URL, Timeout: 5}, zap.NewNop()), projectionTestSchema(), zap.NewNop())
	err := cm.BuildProjection(context.Background(), "build-offline", "candidate-1", 0)
	if err == nil {
		t.Fatal("offline Qdrant must fail build")
	}
	projection, ok := cm.Projection("build-offline")
	if !ok || projection.Status != string(capregistry.ProjectionFailed) {
		t.Fatalf("offline build must be durably terminal FAILED, ok=%v status=%q", ok, projection.Status)
	}
}

func TestProjectionManager_RebuildUsesFreshIdentityAfterFailure(t *testing.T) {
	createOK := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && r.URL.Path == "/collections/candidate-1" && !createOK {
			http.Error(w, "temporary outage", http.StatusBadGateway)
			createOK = true
			return
		}
		if r.Method == http.MethodPut || r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{"result": true})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	schema := projectionTestSchema()
	cm := NewProjectionManager(transport.NewClient(&qdrantSchema.Config{BaseURL: server.URL, Timeout: 5}, zap.NewNop()), schema, zap.NewNop())
	if err := cm.BuildProjection(context.Background(), "failed-build", "candidate-1", 0); err == nil {
		t.Fatal("first build must fail")
	}
	if err := cm.RebuildProjection(context.Background(), "fresh-build", "candidate-2", 0, nil); err != nil {
		t.Fatalf("fresh rebuild should be allowed: %v", err)
	}
	failed, _ := cm.Projection("failed-build")
	fresh, _ := cm.Projection("fresh-build")
	if failed.Status != string(capregistry.ProjectionFailed) || fresh.Status != string(capregistry.ProjectionBuilding) {
		t.Fatalf("rebuild must preserve failed identity and create BUILDING identity: failed=%s fresh=%s", failed.Status, fresh.Status)
	}
}

func mustBeginReadyBuild(t *testing.T, cm *CollectionManager, id, collection string) {
	t.Helper()
	if err := cm.BeginProjection(context.Background(), id, collection, 0); err != nil {
		t.Fatal(err)
	}
}

func projectionTestSchema() *qdrantSchema.IndexSchema {
	return &qdrantSchema.IndexSchema{
		Version: "test", PhysicalName: "candidate-1", RuntimeAlias: "media_assets_current",
		DenseVectors: []qdrantSchema.EmbeddingSpec{{Channel: "text", Model: "test-model", ModelVersion: "model-v1", Dimensions: 768, Distance: "Cosine"}},
	}
}

func projectionPointPayload(assetID, modelVersion string) string {
	return fmt.Sprintf(`{"id":%q,"payload":{"asset_id":%q,"name":"asset","source":"youtube","embedding_version_text":%q}}`,
		qdrantSchema.AssetIDToQdrantPointID(assetID), assetID, modelVersion)
}

func projectionValidationServer(t *testing.T, collection string, pointCount int, rawPoint string) *httptest.Server {
	t.Helper()
	var point map[string]any
	if rawPoint != "" && json.Unmarshal([]byte(rawPoint), &point) != nil {
		t.Fatalf("invalid test point: %s", rawPoint)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/collections/"+collection:
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"points_count": pointCount}})
		case r.Method == http.MethodPost && r.URL.Path == "/collections/"+collection+"/points/scroll":
			points := []any{}
			if point != nil {
				points = append(points, point)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"points": points, "next_page_offset": nil}})
		default:
			http.NotFound(w, r)
		}
	}))
}

type projectionAssetStore struct{ ids []string }

func (s *projectionAssetStore) FetchAsset(context.Context, string) (*indexing.AssetData, error) {
	return nil, nil
}
func (s *projectionAssetStore) ListAllAssetIDs(context.Context) ([]string, error) {
	return append([]string(nil), s.ids...), nil
}
func (s *projectionAssetStore) FetchAssetBatch(context.Context, string, int) ([]*indexing.AssetData, error) {
	return nil, errors.New("not used by projection verifier")
}
