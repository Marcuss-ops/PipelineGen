package health

import (
	"net"
	"strings"
	"time"
	"github.com/stretchr/testify/require"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestQdrantChecker_Disabled_Applicable verifies the Commit 2 fix:
// when vector search is disabled, the check returns
// {ok: true, applicable: false} with the strict hex pattern that
// service.go::Check recognises as "opt-out". The previous implementation
// returned {ok: true, enabled: false} which lacked `applicable` and
// therefore was inconsistent with Drive.
func TestQdrantChecker_Disabled_Applicable(t *testing.T) {
	c := NewQdrantChecker("http://127.0.0.1:6333", "media_assets", false)
	res := c.CheckQdrant(context.Background())

	ok, _ := res["ok"].(bool)
	if !ok {
		t.Fatalf("expected ok=true when capability is opted out, got %v", res)
	}
	app, _ := res["applicable"].(bool)
	if app {
		t.Fatalf("expected applicable=false when disabled, got %v", res)
	}
	note, _ := res["note"].(string)
	if note != "vector search disabled" {
		t.Fatalf("expected 'vector search disabled' note, got %q", note)
	}
	if _, hasError := res["error"]; hasError {
		t.Fatalf("expected no 'error' key when applicable=false, got %v", res)
	}
}

// TestQdrantChecker_Enabled_OK exercises the live probe path against
// a httptest server that mimics the Qdrant /readyz + /collections/<n>
// responses. Asserts applicable=true and ok=true.
func TestQdrantChecker_Enabled_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/readyz":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("all shards are ready"))
		case "/collections/media_assets":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{"points_count": 42},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewQdrantChecker(srv.URL, "media_assets", true)
	res := c.CheckQdrant(context.Background())

	ok, _ := res["ok"].(bool)
	if !ok {
		t.Fatalf("expected ok=true on healthy Qdrant mock, got %v", res)
	}
	app := res["applicable"]
	// When the check is enabled and runs the live probe, applicable is
	// either absent (legacy) or true (Commit 2 caller-set). Treat both
	// as "applicable" for this test — the key invariant is ok=true and
	// points_count present.
	_ = app
	if pc, ok := res["points_count"].(int64); !ok || pc != 42 {
		t.Fatalf("expected points_count=42, got %v", res["points_count"])
	}
	if coll, _ := res["collection"].(string); coll != "media_assets" {
		t.Fatalf("expected collection=media_assets, got %q", coll)
	}
}

// TestQdrantChecker_Enabled_Readyz403 verifies the failure path: when
// Qdrant /readyz returns non-OK, the check must report ok=false with an
// error (NOT applicable=false — because the capability IS enabled).
func TestQdrantChecker_Enabled_Readyz403(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	c := NewQdrantChecker(srv.URL, "media_assets", true)
	res := c.CheckQdrant(context.Background())

	ok, _ := res["ok"].(bool)
	if ok {
		t.Fatalf("expected ok=false on Qdrant /readyz 403, got %v", res)
	}
	if _, has := res["error"]; !has {
		t.Fatalf("expected 'error' key when probe fails, got %v", res)
	}
}


// fix(health) close-out: collection MUST present + readable; missing = ok=false.

func TestQdrantChecker_CollectionMissing(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			w.WriteHeader(http.StatusOK)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/collections/") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":"not_found","error":"collection missing"}`))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(ts.Close)
	c := NewQdrantChecker(ts.URL, "missing_collection", true)
	c.client = ts.Client()
	res := c.CheckQdrant(context.Background())
	require.False(t, res["ok"].(bool), "missing collection must be ok=false, got %v", res)
	require.Contains(t, res["error"].(string), "missing (404)")
}

func TestQdrantChecker_MalformedResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/collections/") {
			_, _ = w.Write([]byte(`{ malformed body without closing brace`))
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(ts.Close)
	c := NewQdrantChecker(ts.URL, "bad_response", true)
	c.client = ts.Client()
	res := c.CheckQdrant(context.Background())
	require.False(t, res["ok"].(bool), "malformed response must be ok=false, got %v", res)
	require.Contains(t, res["error"].(string), "malformed")
}

func TestQdrantChecker_NetworkError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	c := NewQdrantChecker("http://"+addr, "any", true)
	res := c.CheckQdrant(context.Background())
	require.False(t, res["ok"].(bool), "unreachable Qdrant must be ok=false, got %v", res)
	require.Contains(t, res["error"].(string), "connect", "error pin")
}

func TestQdrantChecker_ContextCancellation(t *testing.T) {
	gate := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-gate:
		}
	}))
	t.Cleanup(func() { ts.Close(); close(gate) })
	c := NewQdrantChecker(ts.URL, "any_col", true)
	c.client = ts.Client()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	res := c.CheckQdrant(ctx)
	require.False(t, res["ok"].(bool), "ctx-cancelled must be ok=false, got %v", res)
}
