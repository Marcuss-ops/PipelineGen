// Package app — mediasearch_readiness_test.go
package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/application/search"
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
}

// newSemanticReadinessChecker must adapt the composition root without
// panicking on partially-wired roots, and the nil DB must fail closed.
func TestSemanticReadinessChecker_RootAdapterNilDB(t *testing.T) {
	root := &wiring.ComposeRoot{
		AI: &wiring.AIBundle{},
		DB: nil,
		Process: &wiring.ProcessBundle{
			ProcessQdrantBundle: wiring.ProcessQdrantBundle{
				QdrantHealthProbe: nil,
			},
		},
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
