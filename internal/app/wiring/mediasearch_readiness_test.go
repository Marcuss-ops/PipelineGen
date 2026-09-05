package wiring

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
)

type fakeAggregator struct{ semantic bool }

func (f *fakeAggregator) Search(ctx context.Context, q search.Query) (*search.Result, error) {
	return nil, nil
}

func (f *fakeAggregator) HasBackend(name string) bool {
	return f != nil && f.semantic && name == "semantic"
}

type fakeDBPinger struct{ err error }

func (f *fakeDBPinger) PingContext(ctx context.Context) error { return f.err }

func TestSemanticReadinessChecker_AllGreen(t *testing.T) {
	c := &semanticReadinessChecker{
		embedderWired:        true,
		semanticBackendWired: true,
		aggregator:           &fakeAggregator{semantic: true},
		mediaPostgres:        &fakeDBPinger{},
	}
	if err := c.Ready(context.Background()); err != nil {
		t.Fatalf("expected all-green readiness, got: %v", err)
	}
}

func TestSemanticReadinessChecker_PostgresDown(t *testing.T) {
	c := &semanticReadinessChecker{
		embedderWired:        true,
		semanticBackendWired: true,
		aggregator:           &fakeAggregator{semantic: true},
		mediaPostgres:        &fakeDBPinger{err: errors.New("postgres connection refused")},
	}
	err := c.Ready(context.Background())
	if err == nil {
		t.Fatal("expected readiness error when media PostgreSQL is down")
	}
	subs := err.(interface{ Subsystems() map[string]string }).Subsystems()
	if subs["media_postgres"] == "" {
		t.Fatalf("expected media_postgres failure, got subs=%v", subs)
	}
}

func TestSemanticReadinessChecker_MissingWiring(t *testing.T) {
	c := &semanticReadinessChecker{}
	err := c.Ready(context.Background())
	if err == nil {
		t.Fatal("expected readiness error for unwired checker")
	}
	subs := err.(interface{ Subsystems() map[string]string }).Subsystems()
	for _, key := range []string{"embedder", "semantic_backend", "media_postgres"} {
		if subs[key] == "" {
			t.Fatalf("expected %q failure for unwired checker, got subs=%v", key, subs)
		}
	}
}

func TestSemanticReadinessChecker_ErrorSanitized(t *testing.T) {
	c := &semanticReadinessChecker{
		embedderWired:        true,
		semanticBackendWired: true,
		aggregator:           &fakeAggregator{semantic: true},
		mediaPostgres: &fakeDBPinger{err: errors.New(
			"boom\nwith\nnewlines and a very long message that should be collapsed " +
				"to a single line to avoid leaking anything sensitive across the HTTP boundary")},
	}
	err := c.Ready(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "\n") {
		t.Fatalf("readiness error must be single-line, got: %q", err.Error())
	}
}

func TestSemanticReadinessChecker_RootAdapterNilPostgres(t *testing.T) {
	root := &ComposeRoot{}
	c := newSemanticReadinessChecker(root, &fakeAggregator{semantic: true})
	err := c.Ready(context.Background())
	if err == nil {
		t.Fatal("expected error for partially-wired root")
	}
	subs := err.(interface{ Subsystems() map[string]string }).Subsystems()
	if subs["embedder"] != "" || subs["semantic_backend"] != "" {
		t.Fatalf("semantic backend should prove E5/backend wiring, got subs=%v", subs)
	}
	if subs["media_postgres"] == "" {
		t.Fatalf("expected media_postgres failure, got subs=%v", subs)
	}
}

func TestSemanticReadinessChecker_SubsystemsContract(t *testing.T) {
	c := &semanticReadinessChecker{}
	err := c.Ready(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	subs := err.(interface{ Subsystems() map[string]string }).Subsystems()
	for _, key := range []string{"embedder", "semantic_backend", "media_postgres"} {
		if subs[key] == "" {
			t.Errorf("contract: expected failure key %q, got %v", key, subs)
		}
	}
	if subs["workspace"] != "" {
		t.Errorf("contract: workspace must never be reported failing, got %q", subs["workspace"])
	}
	if len(subs) != 3 {
		t.Errorf("contract: expected exactly 3 failure keys, got %v", subs)
	}
}

func TestWireMediasearchReadiness_Assembly(t *testing.T) {
	root := &ComposeRoot{}
	checker := WireMediasearchReadiness(root, &fakeAggregator{semantic: true})
	if checker == nil {
		t.Fatal("expected non-nil checker from wired semantic aggregator")
	}
	err := checker.Ready(context.Background())
	if err == nil {
		t.Fatal("expected error (media PostgreSQL unwired)")
	}
	subs := err.(interface{ Subsystems() map[string]string }).Subsystems()
	if subs["embedder"] != "" || subs["semantic_backend"] != "" {
		t.Fatalf("expected semantic registration to prove E5/backend wiring, got %v", subs)
	}
	if subs["media_postgres"] == "" {
		t.Fatalf("expected media_postgres failure, got %v", subs)
	}

	checker = WireMediasearchReadiness(root, &fakeAggregator{semantic: false})
	err = checker.Ready(context.Background())
	if err == nil {
		t.Fatal("expected fail-closed error when semantic backend is absent")
	}
	subs = err.(interface{ Subsystems() map[string]string }).Subsystems()
	if subs["embedder"] == "" || subs["semantic_backend"] == "" {
		t.Fatalf("expected embedder+semantic_backend failures, got %v", subs)
	}

	if checker = WireMediasearchReadiness(nil, nil); checker == nil {
		t.Fatal("expected non-nil checker even for nil root")
	} else if err := checker.Ready(context.Background()); err == nil {
		t.Fatal("expected fail-closed error for nil root")
	}
}
