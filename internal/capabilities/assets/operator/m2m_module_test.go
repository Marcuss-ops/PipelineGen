// Tests for the M2M media-read module.
//
// Why these tests matter: M2MModule is the surface a remote computer with
// a media.read-scoped M2M key uses to SEE the elements in the Master's
// media SSOT (and the control-plane state projected alongside them) so it
// can build a job payload from real rows. The load-bearing invariants
// pinned here:
//
//   - The routes mount read-only: only GET /assets, GET /assets/:id and
//     GET /facets — no mutation can reach the remote principal.
//   - The group carries the M2M auth guard: a missing/wrong secret is 401.
//   - The per-route scope check requires `media.read`: a client holding
//     only jobs.read/jobs.submit is 403, a client with media.read is 200.
//   - EnableM2M()==false passes through (dev/test), matching the job
//     surface, so fixtures without a provisioned m2m_clients row work.
//   - A nil read model fails closed with 503 (godlike/07 — never serve a
//     divergent/empty catalog as if it were the truth).
package operator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	mw "github.com/Marcuss-ops/PipelineGen/internal/capabilities/middleware"
	apimw "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver/middleware"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// fakeM2MSecurity is the minimal M2MSecurityPort used by these tests. It
// hashes like the production store so the digest round-trips.
type fakeM2MSecurity struct {
	enabled bool
	clients map[string]*mw.M2MClient
}

func (f *fakeM2MSecurity) EnableM2M() bool { return f.enabled }

func (f *fakeM2MSecurity) HashClientSecret(plaintext string) string {
	// Deterministic, matches nothing in production — the port is the only
	// contract these tests need.
	return "h:" + plaintext
}

func (f *fakeM2MSecurity) LookupClient(_ context.Context, secretHash string) (*mw.M2MClient, error) {
	if f.clients == nil {
		return nil, nil
	}
	return f.clients[secretHash], nil
}

func newFakeM2MSecurity(enabled bool, scopes []string, secret string) *fakeM2MSecurity {
	sec := &fakeM2MSecurity{enabled: enabled, clients: map[string]*mw.M2MClient{}}
	sec.clients["h:"+secret] = &mw.M2MClient{ClientID: "remote-01", Scopes: scopes, Enabled: true}
	return sec
}

// fakeInventoryReader is the read-model double: every arm returns a fixed,
// non-empty payload so a 200 proves the handler reached the reader.
type fakeInventoryReader struct{}

func (fakeInventoryReader) List(context.Context, AssetInventoryQuery) (AssetInventoryPage, error) {
	return AssetInventoryPage{
		Items: []*AssetInventoryItem{{ID: "asset-1", Name: "clip_001.mp4", Source: "stock"}},
		Total: 1,
	}, nil
}

func (fakeInventoryReader) Get(_ context.Context, assetID string) (*AssetInspection, error) {
	return &AssetInspection{
		AssetInventoryItem: AssetInventoryItem{ID: assetID, Name: "clip_001.mp4", Source: "stock"},
	}, nil
}

func (fakeInventoryReader) Facets(context.Context) (*AssetInventoryFacets, error) {
	return &AssetInventoryFacets{}, nil
}

// mountM2MMedia mirrors the server composition: the module is mounted on a
// /api/v1/media group that already carries JobClientAuthMiddleware.
func mountM2MMedia(t *testing.T, module *M2MModule, sec mw.M2MSecurityPort) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	group := engine.Group("/api/v1/media")
	group.Use(apimw.JobClientAuthMiddleware(sec, zap.NewNop()))
	module.RegisterRoutes(group)
	return engine
}

func doGET(engine *gin.Engine, path, secret string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w
}

// TestM2MModule_MediaReadScopeIsEnforced pins the scope gate: jobs.read is
// NOT enough to read the catalog, media.read is.
func TestM2MModule_MediaReadScopeIsEnforced(t *testing.T) {
	module := NewM2MModule(fakeInventoryReader{}, zap.NewNop(), func() bool { return true })

	t.Run("media.read passes", func(t *testing.T) {
		engine := mountM2MMedia(t, module, newFakeM2MSecurity(true, []string{"media.read"}, "s1"))
		w := doGET(engine, "/api/v1/media/assets", "s1")
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 with media.read, got %d (body=%q)", w.Code, w.Body.String())
		}
		var page AssetInventoryPage
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatalf("List body is not an AssetInventoryPage: %v", err)
		}
		if page.Total != 1 {
			t.Fatalf("expected the reader's page to reach the client, got total=%d", page.Total)
		}
	})

	t.Run("jobs.read alone is 403", func(t *testing.T) {
		engine := mountM2MMedia(t, module, newFakeM2MSecurity(true, []string{"jobs.read", "jobs.submit"}, "s2"))
		for _, path := range []string{"/api/v1/media/assets", "/api/v1/media/assets/asset-1", "/api/v1/media/facets"} {
			w := doGET(engine, path, "s2")
			if w.Code != http.StatusForbidden {
				t.Errorf("GET %s: expected 403 without media.read, got %d (body=%q)", path, w.Code, w.Body.String())
			}
		}
	})

	t.Run("no bearer is 401", func(t *testing.T) {
		engine := mountM2MMedia(t, module, newFakeM2MSecurity(true, []string{"media.read"}, "s3"))
		if w := doGET(engine, "/api/v1/media/assets", ""); w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 without a bearer, got %d", w.Code)
		}
	})
}

// TestM2MModule_MountsReadOnlyRoutes pins the surface: exactly the three
// GET routes, no mutation.
func TestM2MModule_MountsReadOnlyRoutes(t *testing.T) {
	module := NewM2MModule(fakeInventoryReader{}, zap.NewNop(), func() bool { return true })
	engine := mountM2MMedia(t, module, newFakeM2MSecurity(false, nil, ""))

	want := map[string]bool{
		"GET /api/v1/media/assets":     false,
		"GET /api/v1/media/assets/:id": false,
		"GET /api/v1/media/facets":     false,
	}
	for _, r := range engine.Routes() {
		key := r.Method + " " + r.Path
		if _, ok := want[key]; ok {
			want[key] = true
			continue
		}
		if len(r.Path) > len("/api/v1/media") && r.Path[:len("/api/v1/media")] == "/api/v1/media" && r.Method != http.MethodGet {
			t.Errorf("M2M media surface must be read-only, found %s", key)
		}
	}
	for key, seen := range want {
		if !seen {
			t.Errorf("expected route %q to be registered, but it is missing", key)
		}
	}
}

// TestM2MModule_PassThroughWhenM2MDisabled pins the dev/test bypass: with
// EnableM2M()==false the guard short-circuits and the reads answer, which
// is what lets fixtures without an m2m_clients row keep working.
func TestM2MModule_PassThroughWhenM2MDisabled(t *testing.T) {
	module := NewM2MModule(fakeInventoryReader{}, zap.NewNop(), func() bool { return true })
	engine := mountM2MMedia(t, module, newFakeM2MSecurity(false, nil, ""))
	if w := doGET(engine, "/api/v1/media/facets", ""); w.Code != http.StatusOK {
		t.Fatalf("expected 200 in pass-through mode, got %d (body=%q)", w.Code, w.Body.String())
	}
}

// TestM2MModule_NilReadModelFailsClosed pins the fail-closed posture: with
// no media SSOT reader the surface answers 503, never an empty catalog.
func TestM2MModule_NilReadModelFailsClosed(t *testing.T) {
	module := NewM2MModule(nil, zap.NewNop(), func() bool { return true })
	engine := mountM2MMedia(t, module, newFakeM2MSecurity(false, nil, ""))
	for _, path := range []string{"/api/v1/media/assets", "/api/v1/media/assets/asset-1", "/api/v1/media/facets"} {
		if w := doGET(engine, path, ""); w.Code != http.StatusServiceUnavailable {
			t.Errorf("GET %s: expected 503 with a nil read model, got %d", path, w.Code)
		}
	}
}

// TestM2MModule_ScopeConstantIsStable pins the wire string: the admin
// key-minting endpoint grants scope strings verbatim, so renaming the
// constant without a migration silently revokes every issued key.
func TestM2MModule_ScopeConstantIsStable(t *testing.T) {
	if ScopeMediaRead != "media.read" {
		t.Fatalf("ScopeMediaRead must stay \"media.read\" (issued keys grant it verbatim), got %q", ScopeMediaRead)
	}
}
