// Package app — mediasearch_readiness_test.go
package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/application/search"
	ollamaclient "github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/client"
)

// fakeReadyProbe is a minimal stand-in for the Qdrant HealthProbe.
type fakeReadyProbe struct{ err error }

func (f *fakeReadyProbe) Probe(ctx context.Context) error { return f.err }

// fakeAggregator satisfies mediasearchapi.AggregatorSearcher for tests.
type fakeAggregator struct{}

func (f *fakeAggregator) Search(ctx context.Context, q search.Query) (*search.Result, error) {
	return nil, nil
}

// fakeDBPinger satisfies the checker's PingContext port.
type fakeDBPinger struct{ err error }

func (f *fakeDBPinger) PingContext(ctx context.Context) error { return f.err }

func TestSemanticReadinessChecker_AllGreen(t *testing.T) {
	c := &semanticReadinessChecker{
		embedderWired: true,
		aggregator:    &fakeAggregator{},
		qdrantProbe:   &fakeReadyProbe{},
		dbPinger:      &fakeDBPinger{},
	}
	if err := c.Ready(context.Background()); err != nil {
		t.Fatalf("expected all-green readiness, got: %v", err)
	}
}

func TestSemanticReadinessChecker_QdrantDown(t *testing.T) {
	c := &semanticReadinessChecker{
		embedderWired: true,
		aggregator:    &fakeAggregator{},
		qdrantProbe:   &fakeReadyProbe{err: errors.New("qdrant connection refused")},
		dbPinger:      &fakeDBPinger{},
	}
	err := c.Ready(context.Background())
	if err == nil {
		t.Fatal("expected readiness error when Qdrant is down")
	}
	subs := err.(interface{ Subsystems() map[string]string }).Subsystems()
	if subs["qdrant_reachable"] == "" {
		t.Fatalf("expected qdrant_reachable failure, got subs=%v", subs)
	}
}

func TestSemanticReadinessChecker_MissingWiring(t *testing.T) {
	// Zero-value checker: every sub-system must fail-closed (godlike/07).
	c := &semanticReadinessChecker{}
	err := c.Ready(context.Background())
	if err == nil {
		t.Fatal("expected readiness error for unwired checker")
	}
	subs := err.(interface{ Subsystems() map[string]string }).Subsystems()
	for _, key := range []string{"embedder", "semantic_backend", "qdrant_reachable", "sqlite_hydration"} {
		if subs[key] == "" {
			t.Fatalf("expected %q failure for unwired checker, got subs=%v", key, subs)
		}
	}
}

func TestSemanticReadinessChecker_ErrorSanitized(t *testing.T) {
	c := &semanticReadinessChecker{
		embedderWired: true,
		aggregator:    &fakeAggregator{},
		qdrantProbe: &fakeReadyProbe{err: errors.New(
			"boom\nwith\nnewlines and a very long message that should be collapsed " +
				"to a single line to avoid leaking anything sensitive across the HTTP boundary")},
		dbPinger: &fakeDBPinger{},
	}
	err := c.Ready(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if strings.Contains(msg, "\n") {
		t.Fatalf("readiness error must be single-line, got: %q", msg)
	}
} // newSemanticReadinessChecker must adapt the composition root without
// panicking on partially-wired roots, and the nil DB must fail closed.
func TestSemanticReadinessChecker_RootAdapterNilDB(t *testing.T) {
	root := &wiring.ComposeRoot{
		AI:      &wiring.AIBundle{},
		DB:      nil,
		Process: &wiring.ProcessBundle{ProcessQdrantBundle: wiring.ProcessQdrantBundle{}},
	}
	c := newSemanticReadinessChecker(root, &fakeAggregator{})
	err := c.Ready(context.Background())
	if err == nil {
		t.Fatal("expected error for partially-wired root")
	}
	subs := err.(interface{ Subsystems() map[string]string }).Subsystems()
	if subs["embedder"] == "" || subs["qdrant_reachable"] == "" || subs["sqlite_hydration"] == "" {
		t.Fatalf("expected embedder+qdrant+sqlite failures, got subs=%v", subs)
	}
}

// Contract test: the Subsystems() keys emitted by the checker are exactly
// the ones buildReadinessReport (internal/api/mediasearch/readiness.go)
// consumes, and "workspace" is never set (enforcement is structural).
func TestSemanticReadinessChecker_SubsystemsContract(t *testing.T) {
	c := &semanticReadinessChecker{}
	err := c.Ready(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	subs := err.(interface{ Subsystems() map[string]string }).Subsystems()

	// buildReadinessReport keys: embedder, semantic_backend, qdrant /
	// qdrant_reachable, sqlite_hydration / sqlite, workspace.
	for _, key := range []string{"embedder", "semantic_backend", "qdrant_reachable", "sqlite_hydration"} {
		if subs[key] == "" {
			t.Errorf("contract: expected failure key %q, got %v", key, subs)
		}
	}
	if subs["workspace"] != "" {
		t.Errorf("contract: workspace must never be reported failing (structural), got %q", subs["workspace"])
	}
	if len(subs) != 4 {
		t.Errorf("contract: expected exactly 4 failure keys, got %v", subs)
	}
}

// WireMediasearchReadiness assembly: adapts the composition root into a
// checker whose embedder wiring is honored, and fail-closes on nil root.
func TestWireMediasearchReadiness_Assembly(t *testing.T) {
	// Root with embedder wired but Qdrant/DB absent: only qdrant+sqlite
	// must fail — embedder passes, proving the adapter read root.AI.
	// OllamaEmbedClient is typed *client.Client; a non-nil instance via
	// client.NewOllamaClient would need a base URL, so we assert on the
	// nil case below instead and use a non-nil zero-value pointer here
	// (the adapter only checks presence, never calls into it).
	root := &wiring.ComposeRoot{
		AI:      &wiring.AIBundle{OllamaEmbedClient: new(ollamaclient.Client)},
		Process: &wiring.ProcessBundle{ProcessQdrantBundle: wiring.ProcessQdrantBundle{}},
	}
	checker := WireMediasearchReadiness(root, &fakeAggregator{})
	if checker == nil {
		t.Fatal("expected non-nil checker from wired root")
	}
	err := checker.Ready(context.Background())
	if err == nil {
		t.Fatal("expected error (qdrant+sqlite unwired)")
	}
	subs := err.(interface{ Subsystems() map[string]string }).Subsystems()
	if subs["embedder"] != "" {
		t.Fatalf("expected embedder green (AI wired), got %q", subs["embedder"])
	}
	if subs["qdrant_reachable"] == "" || subs["sqlite_hydration"] == "" {
		t.Fatalf("expected qdrant+sqlite failures, got %v", subs)
	}

	// Nil root: still returns a usable checker that fail-closes.
	if checker = WireMediasearchReadiness(nil, nil); checker == nil {
		t.Fatal("expected non-nil checker even for nil root")
	} else if err := checker.Ready(context.Background()); err == nil {
		t.Fatal("expected fail-closed error for nil root")
	}
}
