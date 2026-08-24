// blue_green_test.go — lifecycle regression tests for the blue-green
// projection flow. These pin two invariants discovered during the Qdrant
// ENOMEM / projection-lag incident:
//
//  1. A build that fails during populate must NEVER change the runtime
//     alias (the failed candidate is left un-promoted and marked FAILED).
//  2. A FAILED projection's physical collection is cleanup-eligible: the
//     retention sweep drops it while keeping the active target and the
//     known-good rollback.
package collections

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	qdrantSchema "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
	"go.uber.org/zap"
)

// blueGreenServer is a minimal Qdrant mock for the blue-green lifecycle
// tests. It serves the surface the CollectionManager touches during a
// build + retention sweep: create collection / payload index (PUT), the
// global alias surface (GET /aliases + POST /collections/aliases), list
// collections (GET /collections) and delete (DELETE /collections/{name}).
type blueGreenServer struct {
	ts            *httptest.Server
	colls         []string
	aliasTarget   string
	aliasSwitches int
	deletedColls  []string
}

func newBlueGreenServer(colls []string, aliasTarget string) *blueGreenServer {
	s := &blueGreenServer{colls: colls, aliasTarget: aliasTarget}
	mux := http.NewServeMux()

	mux.HandleFunc("/aliases", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": map[string]any{"aliases": []map[string]string{
				{"alias_name": "media_assets_current", "collection_name": s.aliasTarget},
			}},
		})
	})

	mux.HandleFunc("/collections", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		type collection struct {
			Name string `json:"name"`
		}
		out := struct {
			Result struct {
				Collections []collection `json:"collections"`
			} `json:"result"`
		}{}
		for _, c := range s.colls {
			out.Result.Collections = append(out.Result.Collections, collection{Name: c})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})

	mux.HandleFunc("/collections/aliases", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.aliasSwitches++
		_ = json.NewEncoder(w).Encode(map[string]any{"result": true})
	})

	mux.HandleFunc("/collections/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/collections/")
		switch r.Method {
		case http.MethodPut:
			// PUT /collections/{name} (create) and
			// PUT /collections/{name}/index (payload index) both succeed.
			if name != "" {
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, `{"result":true}`)
				return
			}
			http.NotFound(w, r)
		case http.MethodDelete:
			if name == "" || strings.HasSuffix(name, "/aliases") {
				http.NotFound(w, r)
				return
			}
			s.deletedColls = append(s.deletedColls, name)
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"result":true,"status":"acknowledged"}`)
		default:
			http.NotFound(w, r)
		}
	})

	s.ts = httptest.NewServer(mux)
	return s
}

func (s *blueGreenServer) Close() {
	if s.ts != nil {
		s.ts.Close()
	}
}

// TestBlueGreen_FailedPopulateNeverChangesAlias pins the blue-green safety
// invariant: when the populate step of a candidate build fails, the runtime
// alias is left untouched and the projection is terminal FAILED.
func TestBlueGreen_FailedPopulateNeverChangesAlias(t *testing.T) {
	const active = "active-collection"
	const candidate = "candidate-1"

	server := newBlueGreenServer([]string{active}, active)
	defer server.Close()

	schema := qdrantSchema.DefaultV3Schema()
	client := transport.NewClient(&qdrantSchema.Config{BaseURL: server.ts.URL, Timeout: 5}, zap.NewNop())
	cm := NewProjectionManager(client, schema, zap.NewNop())
	if err := cm.SetRegistryLedger(context.Background(), &projectionLedgerStub{sequence: 41}); err != nil {
		t.Fatal(err)
	}

	err := cm.BuildProjectionWith(context.Background(), "build-1", candidate, 41, func(context.Context, string) error {
		return errors.New("injected populate failure")
	})
	if err == nil {
		t.Fatal("BuildProjectionWith must fail when populate fails")
	}

	if server.aliasSwitches != 0 {
		t.Fatalf("failed populate attempted %d alias switch(es), want 0", server.aliasSwitches)
	}
	if server.aliasTarget != active {
		t.Fatalf("alias changed to %q after failed populate, want %q", server.aliasTarget, active)
	}

	projection, ok := cm.Projection("build-1")
	if !ok || projection.Status != string(capregistry.ProjectionFailed) {
		t.Fatalf("projection after failed populate: ok=%v status=%q, want FAILED", ok, projection.Status)
	}
}

// TestBlueGreen_FailedProjectionIsCleanupEligible pins the second half of the
// lifecycle: the physical collection of a FAILED projection is dropped by the
// retention sweep (cleanup-eligible), while the active target and the
// known-good rollback survive.
func TestBlueGreen_FailedProjectionIsCleanupEligible(t *testing.T) {
	schema := qdrantSchema.DefaultV3Schema()
	prefix := schema.CanonicalName()
	active := prefix + "_20260814_071358_active"
	rollback := prefix + "_20260813_184719_rollback"
	candidate := prefix + "_20260814_070758_candidate"

	server := newBlueGreenServer([]string{active, rollback, candidate}, active)
	defer server.Close()

	client := transport.NewClient(&qdrantSchema.Config{BaseURL: server.ts.URL, Timeout: 5}, zap.NewNop())
	cm := NewProjectionManager(client, schema, zap.NewNop())

	ledger := &projectionLedgerStub{
		sequence: 291,
		projections: []capregistry.Projection{
			retentionProjection("active", active, string(capregistry.ProjectionActive), 291),
			retentionProjection("rollback", rollback, string(capregistry.ProjectionRetired), 283),
		},
	}
	if err := cm.SetRegistryLedger(context.Background(), ledger); err != nil {
		t.Fatal(err)
	}

	err := cm.BuildProjectionWith(context.Background(), "build-1", candidate, 291, func(context.Context, string) error {
		return errors.New("injected populate failure")
	})
	if err == nil {
		t.Fatal("BuildProjectionWith must fail when populate fails")
	}
	if projection, ok := cm.Projection("build-1"); !ok || projection.Status != string(capregistry.ProjectionFailed) {
		t.Fatalf("projection after failed populate: ok=%v status=%q, want FAILED", ok, projection.Status)
	}

	res, err := cm.CleanupWithConfig(context.Background(), RetentionConfig{RetentionDays: 1, KeepLastN: 2})
	if err != nil {
		t.Fatalf("CleanupWithConfig: %v", err)
	}

	dropped := func(name string) bool {
		for _, d := range res.DroppedNames {
			if d == name {
				return true
			}
		}
		return false
	}
	if !dropped(candidate) {
		t.Fatalf("failed candidate %q must be cleanup-eligible; DroppedNames=%v", candidate, res.DroppedNames)
	}
	if dropped(active) || dropped(rollback) {
		t.Fatalf("active alias target / known-good rollback must never be dropped; DroppedNames=%v", res.DroppedNames)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("expected zero errors on a clean sweep, got %v", res.Errors)
	}
}
