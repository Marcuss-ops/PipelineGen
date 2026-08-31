package collections

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	qdrantSchema "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
	"go.uber.org/zap"
)

type projectionLedgerStub struct {
	sequence    int64
	projections []capregistry.Projection
}

func (s *projectionLedgerStub) AppendEvent(context.Context, capregistry.Event) (int64, error) {
	return 0, nil
}
func (s *projectionLedgerStub) StartRun(context.Context, capregistry.Run) error  { return nil }
func (s *projectionLedgerStub) FinishRun(context.Context, capregistry.Run) error { return nil }
func (s *projectionLedgerStub) RegisterProjection(_ context.Context, projection capregistry.Projection) error {
	for i := range s.projections {
		if s.projections[i].ProjectionID == projection.ProjectionID {
			s.projections[i] = projection
			return nil
		}
	}
	s.projections = append(s.projections, projection)
	return nil
}
func (s *projectionLedgerStub) RegisterBackup(context.Context, capregistry.Backup) error { return nil }
func (s *projectionLedgerStub) LatestEventSequence(context.Context) (int64, error) {
	return s.sequence, nil
}
func (s *projectionLedgerStub) ListProjections(context.Context) ([]capregistry.Projection, error) {
	return append([]capregistry.Projection(nil), s.projections...), nil
}

func TestProjectionManager_HydratesDurableStateAndUsesCanonicalSequence(t *testing.T) {
	ledger := &projectionLedgerStub{
		sequence: 7,
		projections: []capregistry.Projection{{
			ProjectionID:      "old-build",
			ProjectionType:    "qdrant",
			CollectionName:    "media_assets_v3_old",
			AliasName:         "media_assets_current",
			Status:            string(capregistry.ProjectionRetired),
			SourceRegistrySeq: 7,
			CreatedAt:         "2026-08-12T00:00:00Z",
		}},
	}
	cm := NewProjectionManager(nil, qdrantSchema.DefaultV3Schema(), zap.NewNop())
	if err := cm.SetRegistryLedger(context.Background(), ledger); err != nil {
		t.Fatal(err)
	}
	if _, ok := cm.Projection("old-build"); !ok {
		t.Fatal("durable projection was not hydrated")
	}
	if err := cm.BeginProjection(context.Background(), "new-build", "media_assets_v3_new", 999); err != nil {
		t.Fatal(err)
	}
	projection, ok := cm.Projection("new-build")
	if !ok || projection.SourceRegistrySeq != 7 {
		t.Fatalf("BeginProjection must use canonical sequence, got ok=%v seq=%d", ok, projection.SourceRegistrySeq)
	}
}

func TestProjectionManager_ActivationSequenceAdvanceFailsProjection(t *testing.T) {
	ledger := &projectionLedgerStub{sequence: 7}
	cm := NewProjectionManager(nil, qdrantSchema.DefaultV3Schema(), zap.NewNop())
	if err := cm.SetRegistryLedger(context.Background(), ledger); err != nil {
		t.Fatal(err)
	}
	if err := cm.BeginProjection(context.Background(), "build-1", "candidate-1", 7); err != nil {
		t.Fatal(err)
	}
	if err := cm.transitionProjection(context.Background(), "build-1", capregistry.ProjectionValidating); err != nil {
		t.Fatal(err)
	}
	if err := cm.transitionProjection(context.Background(), "build-1", capregistry.ProjectionReady); err != nil {
		t.Fatal(err)
	}
	ledger.sequence = 8
	if err := cm.ActivateProjection(context.Background(), "build-1", 7); !errors.Is(err, capregistry.ErrProjectionSequenceLag) {
		t.Fatalf("activation error=%v, want sequence lag", err)
	}
	projection, _ := cm.Projection("build-1")
	if projection.Status != string(capregistry.ProjectionFailed) {
		t.Fatalf("stale activation must be terminal FAILED, got %q", projection.Status)
	}
}

func TestProjectionManager_ActivationAndRollbackUpdateAliasAndLifecycle(t *testing.T) {
	aliasTarget := "media_assets_v3_old"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/aliases":
			aliases := []map[string]string{}
			if aliasTarget != "" {
				aliases = append(aliases, map[string]string{"alias_name": "media_assets_current", "collection_name": aliasTarget})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"aliases": aliases}})
		case r.Method == http.MethodPost && r.URL.Path == "/collections/aliases":
			var request struct {
				Actions []map[string]json.RawMessage `json:"actions"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			for _, action := range request.Actions {
				if _, ok := action["delete_alias"]; ok {
					aliasTarget = ""
				}
				if raw, ok := action["create_alias"]; ok {
					var create struct {
						CollectionName string `json:"collection_name"`
					}
					if err := json.Unmarshal(raw, &create); err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					aliasTarget = create.CollectionName
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	schema := qdrantSchema.DefaultV3Schema()
	client := transport.NewClient(&qdrantSchema.Config{BaseURL: server.URL, Timeout: 5}, zap.NewNop())
	cm := NewProjectionManager(client, schema, zap.NewNop())
	for _, projection := range []struct {
		id         string
		collection string
		status     capregistry.ProjectionStatus
	}{
		{"old-build", "media_assets_v3_old", capregistry.ProjectionActive},
		{"new-build", "media_assets_v3_new", capregistry.ProjectionReady},
	} {
		if err := cm.BeginProjection(context.Background(), projection.id, projection.collection, 41); err != nil {
			t.Fatal(err)
		}
		for _, next := range []capregistry.ProjectionStatus{capregistry.ProjectionValidating, capregistry.ProjectionReady} {
			if err := cm.transitionProjection(context.Background(), projection.id, next); err != nil {
				t.Fatal(err)
			}
		}
		if projection.status == capregistry.ProjectionActive {
			if err := cm.transitionProjection(context.Background(), projection.id, capregistry.ProjectionActive); err != nil {
				t.Fatal(err)
			}
		}
	}

	if err := cm.ActivateProjection(context.Background(), "new-build", 41); err != nil {
		t.Fatalf("activation failed: %v", err)
	}
	if aliasTarget != "media_assets_v3_new" {
		t.Fatalf("alias after activation=%q, want media_assets_v3_new", aliasTarget)
	}
	oldProjection, _ := cm.Projection("old-build")
	newProjection, _ := cm.Projection("new-build")
	if oldProjection.Status != string(capregistry.ProjectionRetired) || newProjection.Status != string(capregistry.ProjectionActive) {
		t.Fatalf("activation lifecycle old=%s new=%s", oldProjection.Status, newProjection.Status)
	}

	if err := cm.RollbackProjection(context.Background(), "new-build", "media_assets_v3_old"); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	if aliasTarget != "media_assets_v3_old" {
		t.Fatalf("alias after rollback=%q, want media_assets_v3_old", aliasTarget)
	}
	oldProjection, _ = cm.Projection("old-build")
	newProjection, _ = cm.Projection("new-build")
	if oldProjection.Status != string(capregistry.ProjectionActive) || newProjection.Status != string(capregistry.ProjectionRetired) {
		t.Fatalf("rollback lifecycle old=%s new=%s", oldProjection.Status, newProjection.Status)
	}
}

func TestProjectionManager_SequenceMismatchFailsClosed(t *testing.T) {
	cm := NewProjectionManager(nil, qdrantSchema.DefaultV3Schema(), zap.NewNop())
	if err := cm.BeginProjection(context.Background(), "build-1", "candidate-1", 41); err != nil {
		t.Fatal(err)
	}

	_, err := cm.ValidateProjection(context.Background(), "build-1", 42, 1)
	if !errors.Is(err, capregistry.ErrProjectionSequenceLag) {
		t.Fatalf("ValidateProjection error=%v, want sequence lag", err)
	}
	projection, ok := cm.Projection("build-1")
	if !ok || projection.Status != string(capregistry.ProjectionFailed) {
		t.Fatalf("sequence mismatch must fail projection, got ok=%v status=%q", ok, projection.Status)
	}
}

func TestProjectionManager_CannotActivateBeforeValidation(t *testing.T) {
	cm := NewProjectionManager(nil, qdrantSchema.DefaultV3Schema(), zap.NewNop())
	if err := cm.BeginProjection(context.Background(), "build-1", "candidate-1", 41); err != nil {
		t.Fatal(err)
	}
	if err := cm.ActivateProjection(context.Background(), "build-1", 41); !errors.Is(err, ErrProjectionNotReady) {
		t.Fatalf("ActivateProjection error=%v, want ErrProjectionNotReady", err)
	}
}

func TestProjectionManager_AliasFailureKeepsReadyState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/aliases":
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"aliases": []map[string]string{{
				"alias_name": "media_assets_current", "collection_name": "media_assets_v3_old",
			}}}})
		case r.Method == http.MethodPost && r.URL.Path == "/collections/aliases":
			http.Error(w, "injected alias outage", http.StatusBadGateway)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	schema := qdrantSchema.DefaultV3Schema()
	client := transport.NewClient(&qdrantSchema.Config{BaseURL: server.URL, Timeout: 5}, zap.NewNop())
	cm := NewProjectionManager(client, schema, zap.NewNop())
	if err := cm.BeginProjection(context.Background(), "build-1", "candidate-1", 41); err != nil {
		t.Fatal(err)
	}
	if err := cm.transitionProjection(context.Background(), "build-1", capregistry.ProjectionValidating); err != nil {
		t.Fatal(err)
	}
	if err := cm.transitionProjection(context.Background(), "build-1", capregistry.ProjectionReady); err != nil {
		t.Fatal(err)
	}

	if err := cm.ActivateProjection(context.Background(), "build-1", 41); err == nil {
		t.Fatal("ActivateProjection must fail when Qdrant alias switch fails")
	}
	projection, ok := cm.Projection("build-1")
	if !ok || projection.Status != string(capregistry.ProjectionReady) {
		t.Fatalf("alias failure must retain READY for retry, got ok=%v status=%q", ok, projection.Status)
	}
}

func TestProjectionManager_RollbackFailureKeepsActiveState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/aliases":
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"aliases": []map[string]string{{
				"alias_name": "media_assets_current", "collection_name": "candidate-1",
			}}}})
		case r.Method == http.MethodPost && r.URL.Path == "/collections/aliases":
			http.Error(w, "injected rollback outage", http.StatusServiceUnavailable)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	schema := qdrantSchema.DefaultV3Schema()
	client := transport.NewClient(&qdrantSchema.Config{BaseURL: server.URL, Timeout: 5}, zap.NewNop())
	cm := NewProjectionManager(client, schema, zap.NewNop())
	if err := cm.BeginProjection(context.Background(), "build-1", "candidate-1", 41); err != nil {
		t.Fatal(err)
	}
	if err := cm.transitionProjection(context.Background(), "build-1", capregistry.ProjectionValidating); err != nil {
		t.Fatal(err)
	}
	if err := cm.transitionProjection(context.Background(), "build-1", capregistry.ProjectionReady); err != nil {
		t.Fatal(err)
	}
	if err := cm.transitionProjection(context.Background(), "build-1", capregistry.ProjectionActive); err != nil {
		t.Fatal(err)
	}

	if err := cm.RollbackProjection(context.Background(), "build-1", "media_assets_v3_old"); err == nil {
		t.Fatal("RollbackProjection must fail when Qdrant rejects the switch")
	}
	projection, ok := cm.Projection("build-1")
	if !ok || projection.Status != string(capregistry.ProjectionActive) {
		t.Fatalf("rollback failure must retain ACTIVE state, got ok=%v status=%q", ok, projection.Status)
	}
}
