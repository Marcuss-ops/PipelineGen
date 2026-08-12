package testutil

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/collections"
)

// minimalTestSchema returns a schema with a single small dense vector,
// suitable for hermetic collection tests that don't need the full
// production v3 schema.
func minimalTestSchema() *schema.IndexSchema {
	return &schema.IndexSchema{
		Version:      "test-v1",
		PhysicalName: "test_placeholder",
		RuntimeAlias: "test_alias_placeholder",
		DenseVectors: []schema.EmbeddingSpec{
			{Channel: "text", Dimensions: 128, Distance: "Cosine"},
		},
	}
}

// ── Probe tests ──────────────────────────────────────────────────────

func TestProbeQdrant_Reachable(t *testing.T) {
	// httptest returns 200 by default for unmatched routes — this is
	// good enough for probeQdrant which only checks for >=500.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return 200 on /telemetry.
	}))
	defer srv.Close()

	client := transport.NewClient(&schema.Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())
	if err := probeQdrant(client); err != nil {
		t.Errorf("expected nil error for reachable Qdrant, got: %v", err)
	}
}

func TestProbeQdrant_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := transport.NewClient(&schema.Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())
	if err := probeQdrant(client); err == nil {
		t.Error("expected non-nil error for Qdrant returning 500")
	}
}

func TestProbeQdrant_Unauthorized(t *testing.T) {
	// A 401/403 (API key required) must be treated as probe failure
	// so tests don't proceed past the probe only to fail on
	// CreateCollection with a confusing error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	client := transport.NewClient(&schema.Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())
	if err := probeQdrant(client); err == nil {
		t.Error("expected non-nil error for Qdrant returning 401")
	}
}

// ── Collection name tests ────────────────────────────────────────────

func TestCollectionName_DeterministicPrefix(t *testing.T) {
	// t.Name() for a top-level test function is "TestCollectionName_DeterministicPrefix".
	name := collectionName(t)

	if !strings.HasPrefix(name, "test-") {
		t.Errorf("collection name must start with 'test-': got %q", name)
	}
	if !strings.Contains(name, "TestCollectionName_DeterministicPrefix") {
		t.Errorf("collection name must contain test function name: got %q", name)
	}
}

func TestCollectionName_ReplacesSlashes(t *testing.T) {
	// Subtests produce t.Name() like "Parent/Child".
	// We can't change t.Name(), but we can test the sanitization
	// logic via a subtest that exercises the slash path.
	t.Run("SubTest", func(t *testing.T) {
		name := collectionName(t)
		if strings.Contains(name, "/") {
			t.Errorf("collection name must not contain slashes: got %q", name)
		}
	})
}

func TestCollectionName_UniqueAcrossCalls(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 20; i++ {
		name := collectionName(t)
		if seen[name] {
			t.Errorf("duplicate collection name generated: %q", name)
		}
		seen[name] = true
	}
}

func TestCollectionName_Length(t *testing.T) {
	name := collectionName(t)
	// Format: test-<function>-<8hex>. Function name varies, but
	// total should be at least 20 characters.
	if len(name) < 20 {
		t.Errorf("collection name too short (%d chars): %q", len(name), name)
	}
}

// ── Full round-trip: live httptest mock ──────────────────────────────

func TestWithHermeticCollection_CreatesAndDeletes(t *testing.T) {
	collectionCreated := false
	collectionDeleted := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /telemetry: health probe.
		if r.Method == http.MethodGet && r.URL.Path == "/telemetry" {
			return
		}
		// PUT /collections/<name>: collection creation.
		if r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/collections/") {
			collectionCreated = true
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"result":true,"status":"ok"}`))
			return
		}
		// PUT /collections/<name>/index: payload index creation.
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/index") {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"result":{"status":"acknowledged"},"status":"ok"}`))
			return
		}
		// DELETE /collections/<name>: collection deletion.
		if r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/collections/") {
			collectionDeleted = true
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"result":true,"status":"ok"}`))
			return
		}
		http.NotFound(w, r)
	}))
	// t.Cleanup runs AFTER the helper's own t.Cleanup (LIFO order:
	// last-registered runs first), so the deletion happens before
	// the server shuts down.
	t.Cleanup(srv.Close)

	client := transport.NewClient(&schema.Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())
	sch := minimalTestSchema()

	called := false
	var capturedName string

	WithHermeticCollection(t, client, sch,
		func(_ context.Context, name string, _ *collections.CollectionManager) {
			called = true
			capturedName = name
		},
	)

	if !called {
		t.Error("callback was not invoked")
	}
	if capturedName == "" {
		t.Error("collection name passed to callback was empty")
	}
	if !strings.HasPrefix(capturedName, "test-") {
		t.Errorf("collection name must start with 'test-': got %q", capturedName)
	}
	if !collectionCreated {
		t.Error("collection was not created (PUT /collections/<name> not called)")
	}
	if !collectionDeleted {
		t.Error("collection was not deleted (DELETE /collections/<name> not called)")
	}
}

// TestWithHermeticCollection_DeletesOnCallbackPanic verifies the
// cleanup contract: deletion still runs even if the callback panics.
func TestWithHermeticCollection_DeletesOnCallbackPanic(t *testing.T) {
	var deleted []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/telemetry" {
			return
		}
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/index") {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"result":{"status":"acknowledged"},"status":"ok"}`))
			return
		}
		if r.Method == http.MethodPut {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"result":true,"status":"ok"}`))
			return
		}
		if r.Method == http.MethodDelete {
			deleted = append(deleted, strings.TrimPrefix(r.URL.Path, "/collections/"))
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"result":true,"status":"ok"}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	client := transport.NewClient(&schema.Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())
	sch := minimalTestSchema()

	// recover the panic so the test can proceed to check deletion.
	func() {
		defer func() { recover() }()
		WithHermeticCollection(t, client, sch,
			func(_ context.Context, _ string, _ *collections.CollectionManager) {
				panic("intentional test panic")
			},
		)
	}()

	if len(deleted) == 0 {
		t.Error("collection was not deleted after callback panic — cleanup contract violated")
	}
}

// TestWithHermeticCollection_InvalidSchemaFailsFast verifies that an
// invalid schema fails before any Qdrant calls and the callback is
// never invoked.
func TestWithHermeticCollection_InvalidSchemaFailsFast(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("unexpected HTTP call to Qdrant mock — test should have failed-fast on invalid schema")
	}))
	defer srv.Close()

	client := transport.NewClient(&schema.Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())
	invalid := &schema.IndexSchema{} // missing Version, DenseVectors, etc.

	called := false
	fake := &fakeT{}
	// fakeT.Fatalf panics; recover so we can assert the outcome.
	func() {
		defer func() { recover() }()
		WithHermeticCollection(fake, client, invalid,
			func(_ context.Context, _ string, _ *collections.CollectionManager) {
				called = true
			},
		)
	}()
	if called {
		t.Error("callback should not have been invoked for invalid schema")
	}
	if !fake.fatalCalled {
		t.Error("expected t.Fatalf for invalid schema, but it was not called")
	}
}

// ── fakeT ────────────────────────────────────────────────────────────// fakeT is a minimal testing.TB that records whether Fatalf was called.
// It overrides only the methods that WithHermeticCollection uses;
// other testing.TB methods (Error, Log, Skip, etc.) will panic if
// called via the embedded nil interface. This is intentional — the
// test fails loudly if WithHermeticCollection calls an unexpected
// method, which is the desired behavior for a contract test.
type fakeT struct {
	testing.TB
	fatalCalled bool
}

func (f *fakeT) Fatalf(format string, args ...any) {
	f.fatalCalled = true
	panic("fakeT.Fatalf") // stop execution
}

func (f *fakeT) Helper()              {}
func (f *fakeT) Skipf(string, ...any) {}
func (f *fakeT) Cleanup(func())       {}
func (f *fakeT) Name() string         { return "FakeTest" }
