package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
)

func TestWarmModelLoadsOnceAndVerifiesResidency(t *testing.T) {
	var psCalls atomic.Int32
	var chatCalls atomic.Int32
	var chatBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/ps":
			call := psCalls.Add(1)
			if call == 1 {
				_, _ = w.Write([]byte(`{"models":[]}`))
				return
			}
			// Derived from the constant: when the resident bucket moves, the fake
			// moves with it instead of pinning the residency check to a stale width.
			_, _ = w.Write([]byte(fmt.Sprintf(`{"models":[{"name":"gemma4:e4b@sha256:test","context_length":%d}]}`, types.ProductionRunnerContext)))
		case "/api/chat":
			chatCalls.Add(1)
			if err := json.NewDecoder(r.Body).Decode(&chatBody); err != nil {
				t.Errorf("decode chat request: %v", err)
			}
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":""},"done":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	c := NewClient(server.URL, "gemma4:e4b", 5)
	if err := c.WarmModel(context.Background(), "gemma4:e4b"); err != nil {
		t.Fatalf("WarmModel: %v", err)
	}
	if got := psCalls.Load(); got != 2 {
		t.Fatalf("/api/ps calls = %d, want pre- and post-warm verification", got)
	}
	if got := chatCalls.Load(); got != 1 {
		t.Fatalf("/api/chat calls = %d, want one warm request", got)
	}
	if got, _ := chatBody["keep_alive"].(string); got != "30m" {
		t.Fatalf("keep_alive = %q, want 30m", got)
	}
	if got, _ := chatBody["model"].(string); got != "gemma4:e4b" {
		t.Fatalf("model = %q, want configured model", got)
	}
	options, _ := chatBody["options"].(map[string]any)
	if got := options["num_ctx"]; got != float64(types.ProductionRunnerContext) {
		t.Fatalf("options.num_ctx = %v, want %d resident runner", got, types.ProductionRunnerContext)
	}
}

// TestWarmModelRecordsItsOwnMeasuredOperation pins the Pipeline Waste Audit
// §2.3 acceptance criterion: the warm probe's model load must be attributable
// as its OWN operation. When the probe was unmeasured, a warm-up that paid a
// full model load landed inside the `generate` stage wall with no operation
// behind it, so the stage looked slow while every operation inside it looked
// fast — and the regression was invisible.
func TestWarmModelRecordsItsOwnMeasuredOperation(t *testing.T) {
	var psCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/ps":
			if psCalls.Add(1) == 1 {
				_, _ = w.Write([]byte(`{"models":[]}`))
				return
			}
			_, _ = w.Write([]byte(fmt.Sprintf(`{"models":[{"name":"gemma4:e4b","context_length":%d}]}`, types.ProductionRunnerContext)))
		case "/api/chat":
			// A cold load (~45s) followed by a trivial probe inference.
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":""},"done":true,"load_duration":45000000000,"prompt_eval_count":1,"prompt_eval_duration":1000000,"eval_count":1,"eval_duration":2000000,"total_duration":46000000000}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{JobID: "job-warm", AttemptID: "attempt-1"})
	ctx := kernobs.WithRun(context.Background(), run)

	c := NewClient(server.URL, "gemma4:e4b", 5)
	if err := c.WarmModel(ctx, "gemma4:e4b"); err != nil {
		t.Fatalf("WarmModel: %v", err)
	}
	run.Finish()

	ops := run.Report().Operations
	var warm *kernobs.OperationReport
	for i := range ops {
		if ops[i].Operation == string(kernobs.OperationWarm) {
			warm = &ops[i]
			break
		}
	}
	if warm == nil {
		t.Fatalf("warm probe operation not recorded; operations = %+v", ops)
	}
	if warm.Stage != string(kernobs.StageGenerate) || warm.Component != string(kernobs.ComponentOllama) {
		t.Fatalf("warm operation attributed to stage=%q component=%q, want generate/ollama", warm.Stage, warm.Component)
	}

	var meta map[string]any
	if err := json.Unmarshal([]byte(warm.MetadataJSON), &meta); err != nil {
		t.Fatalf("warm metadata %q: %v", warm.MetadataJSON, err)
	}
	if got := meta["model_load_ms"]; got != float64(45000) {
		t.Fatalf("warm model_load_ms = %v, want 45000 (the load must be its own measured fact)", got)
	}
	if got := meta["cold_start"]; got != true {
		t.Fatalf("warm cold_start = %v, want true", got)
	}
	if got := meta["inference_work_ms"]; got != float64(3) {
		t.Fatalf("warm inference_work_ms = %v, want 3", got)
	}
	if got := meta["num_ctx"]; got != float64(types.ProductionRunnerContext) {
		t.Fatalf("warm num_ctx = %v, want %d (resident runner bucket)", got, types.ProductionRunnerContext)
	}
}

func TestWarmModelSkipsChatWhenAlreadyResident(t *testing.T) {
	var psCalls atomic.Int32
	var chatCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/ps" {
			psCalls.Add(1)
			_, _ = w.Write([]byte(fmt.Sprintf(`{"models":[{"name":"gemma4:e4b","context_length":%d}]}`, types.ProductionRunnerContext)))
			return
		}
		if r.URL.Path == "/api/chat" {
			chatCalls.Add(1)
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	c := NewClient(server.URL, "gemma4:e4b", 5)
	if err := c.WarmModel(context.Background(), "gemma4:e4b"); err != nil {
		t.Fatalf("WarmModel: %v", err)
	}
	if got := psCalls.Load(); got != 1 {
		t.Fatalf("/api/ps calls = %d, want one resident check", got)
	}
	if got := chatCalls.Load(); got != 0 {
		t.Fatalf("/api/chat calls = %d, want zero for resident model", got)
	}
}

func TestWarmModelReloadsWhenResidentContextIsWrong(t *testing.T) {
	var psCalls atomic.Int32
	var chatCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/ps":
			call := psCalls.Add(1)
			if call == 1 {
				// A deliberately WRONG resident width: it must not equal the
				// production bucket, or the correction path would not be exercised.
				_, _ = w.Write([]byte(`{"models":[{"name":"gemma4:e4b","context_length":2048}]}`))
				return
			}
			_, _ = w.Write([]byte(fmt.Sprintf(`{"models":[{"name":"gemma4:e4b","context_length":%d}]}`, types.ProductionRunnerContext)))
		case "/api/chat":
			chatCalls.Add(1)
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":""},"done":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	c := NewClient(server.URL, "gemma4:e4b", 5)
	if err := c.WarmModel(context.Background(), "gemma4:e4b"); err != nil {
		t.Fatalf("WarmModel: %v", err)
	}
	if got := chatCalls.Load(); got != 1 {
		t.Fatalf("/api/chat calls = %d, want one context correction", got)
	}
}
