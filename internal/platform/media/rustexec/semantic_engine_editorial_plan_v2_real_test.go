package rustexec

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// countingRustRunner wraps the real process runner only to count calls. The
// inner runner still spawns the semantic-engine binary exactly as production
// does; there is no fixture response.
type countingRustRunner struct {
	inner RustProcessRunner
	calls int
	input []byte
}

func (r *countingRustRunner) Run(ctx context.Context, binary string, input []byte, outputLimit int64) ([]byte, []byte, error) {
	r.calls++
	r.input = append([]byte(nil), input...)
	return r.inner.Run(ctx, binary, input, outputLimit)
}

func TestCompileOverlayPlanV2UsesOneRealWorkerCall(t *testing.T) {
	binary := resolveSemanticEngineBinary()
	if binary == "" {
		t.Skip("semantic-engine not installed; set VELOX_RUST_SEMANTIC_ENGINE_PATH for the real-worker v2 gate")
	}
	requestPath := filepath.Join("..", "..", "..", "..", "..", "BrainRust", "overlay-compiler", "tests", "fixtures", "overlay_plan", "requests", "minimal_phrase_only.request.json")
	request, err := os.ReadFile(requestPath)
	if err != nil {
		t.Skipf("v2 fixture not available: %v", err)
	}

	runner := &countingRustRunner{inner: rustProcessRunner{}}
	executor := &Executor{binaryPath: binary, runner: runner, outputLimit: semanticEngineOutputLimit}
	adapter, err := NewSemanticEngineAdapter(executor)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := adapter.CompileOverlayPlanV2(context.Background(), request)
	if err != nil {
		t.Fatalf("compile full v2 plan: %v", err)
	}
	if runner.calls != 1 {
		t.Fatalf("full-plan compile used %d worker calls, want 1", runner.calls)
	}
	if plan.PlanID != "min-fixture" || plan.SchemaVersion != "editorial_plan.v2" {
		t.Fatalf("plan identity: %+v", plan)
	}
	if len(plan.Items) != 3 {
		t.Fatalf("plan items = %d, want 3", len(plan.Items))
	}
	var sent map[string]any
	if err := json.Unmarshal(runner.input, &sent); err != nil {
		t.Fatalf("decode sent request: %v", err)
	}
	if sent["schema_version"] != "editorial_plan.v2" {
		t.Fatalf("v2 request must declare its schema at the top level: %s", runner.input)
	}
	if _, legacy := sent["v2"]; legacy {
		t.Fatalf("v2 request must not travel inside the legacy batch: %s", runner.input)
	}
	if strings.Count(strings.TrimSpace(string(runner.input)), "\n") != 0 {
		t.Fatalf("worker request must be exactly one NDJSON line: %s", runner.input)
	}
}
