// Package scriptdocs — hermetic TDD tests for the ReAct Python bridge adapter.
//
// All tests are hermetic (zero live Ollama, zero live Python subprocess,
// zero network). The ProcessRunner stub captures argv without spawning
// a process.
package scriptdocs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	scriptdocsapi "github.com/Marcuss-ops/PipelineGen/internal/api/script-docs"
	processpkg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"
)

// ── Test stubs ──────────────────────────────────────────────────────

// captureRunner records the arguments passed to Run without spawning
// a subprocess. Its handler field controls the returned output/error.
type captureRunner struct {
	handler func(ctx context.Context, name string, args []string, opts processpkg.Options) (*processpkg.Result, error)
	calls   int
	lastCtx context.Context
	lastArg string // the --json argument payload
}

func (c *captureRunner) Run(ctx context.Context, name string, args []string, opts processpkg.Options) (*processpkg.Result, error) {
	c.calls++
	c.lastCtx = ctx
	// Capture the --json argument (last arg after "--json").
	for i, a := range args {
		if a == "--json" && i+1 < len(args) {
			c.lastArg = args[i+1]
		}
	}
	if c.handler != nil {
		return c.handler(ctx, name, args, opts)
	}
	return &processpkg.Result{Output: `{"result":"default","status":"ok","steps_taken":1}`}, nil
}

// Compile-time pin.
var _ ProcessRunner = (*captureRunner)(nil)

// okRunner returns a canned successful response.
func okRunner(result, status string, steps int) *captureRunner {
	return &captureRunner{
		handler: func(_ context.Context, _ string, _ []string, _ processpkg.Options) (*processpkg.Result, error) {
			resp := reactBridgeResponse{Result: result, Status: status, StepsTaken: steps}
			b, _ := json.Marshal(resp)
			return &processpkg.Result{Output: string(b)}, nil
		},
	}
}

// errRunner returns a subprocess error.
func errRunner(errMsg string) *captureRunner {
	return &captureRunner{
		handler: func(_ context.Context, _ string, _ []string, _ processpkg.Options) (*processpkg.Result, error) {
			return nil, fmt.Errorf("%s", errMsg)
		},
	}
}

// bridgeErrRunner returns a valid JSON response with an error field.
func bridgeErrRunner(errMsg string) *captureRunner {
	return &captureRunner{
		handler: func(_ context.Context, _ string, _ []string, _ processpkg.Options) (*processpkg.Result, error) {
			resp := reactBridgeResponse{Status: "error", Error: errMsg}
			b, _ := json.Marshal(resp)
			return &processpkg.Result{Output: string(b)}, nil
		},
	}
}

// invalidJSONRunner returns non-JSON output.
func invalidJSONRunner(output string) *captureRunner {
	return &captureRunner{
		handler: func(_ context.Context, _ string, _ []string, _ processpkg.Options) (*processpkg.Result, error) {
			return &processpkg.Result{Output: output}, nil
		},
	}
}

func defaultTestConfig() AdapterConfig {
	return AdapterConfig{
		OllamaURL:       "http://localhost:11434",
		ScriptDir:       "/opt/pipelinegen",
		OllamaModel:     "gemma4:e4b",
		MaxStepsDefault: 5,
		Runner:          okRunner("test result", "ok", 3),
	}
}

// ── NewAdapter tests ────────────────────────────────────────────────

func TestNewAdapter_MissingOllamaURL_ReturnsError(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.OllamaURL = ""
	_, err := NewAdapter(cfg)
	if !errors.Is(err, ErrReActAdapterMisconfigured) {
		t.Fatalf("expected ErrReActAdapterMisconfigured, got: %v", err)
	}
}

func TestNewAdapter_MissingScriptDir_ReturnsError(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.ScriptDir = ""
	_, err := NewAdapter(cfg)
	if !errors.Is(err, ErrReActAdapterMisconfigured) {
		t.Fatalf("expected ErrReActAdapterMisconfigured, got: %v", err)
	}
}

func TestNewAdapter_NilRunner_DefaultsToProcessRun(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.Runner = nil
	a, err := NewAdapter(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a == nil {
		t.Fatal("expected non-nil adapter")
	}
	// The adapter's runner should be defaultProcessRunner, not nil.
	if a.runner == nil {
		t.Fatal("expected defaultProcessRunner when Runner is nil")
	}
}

func TestNewAdapter_DefaultModel(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.OllamaModel = ""
	a, err := NewAdapter(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.ollamaModel != "gemma4:e4b" {
		t.Fatalf("expected default model 'gemma4:e4b', got %q", a.ollamaModel)
	}
}

func TestNewAdapter_DefaultMaxSteps(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.MaxStepsDefault = 0
	a, err := NewAdapter(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.maxStepsDefault != 5 {
		t.Fatalf("expected default maxSteps 5, got %d", a.maxStepsDefault)
	}
}

// ── Generate tests ──────────────────────────────────────────────────

func TestGenerate_HappyPath(t *testing.T) {
	runner := okRunner("The document covers boxing history.", "ok", 3)
	cfg := defaultTestConfig()
	cfg.Runner = runner
	a, err := NewAdapter(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resp, err := a.Generate(context.Background(), scriptdocsapi.ReActRequest{
		Topic: "boxing history",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Result != "The document covers boxing history." {
		t.Fatalf("unexpected result: %q", resp.Result)
	}
	if resp.Status != "ok" {
		t.Fatalf("expected status 'ok', got %q", resp.Status)
	}
	if resp.StepsTaken != 3 {
		t.Fatalf("expected 3 steps, got %d", resp.StepsTaken)
	}
	if runner.calls != 1 {
		t.Fatalf("expected 1 subprocess call, got %d", runner.calls)
	}
}

func TestGenerate_PassesPayloadToSubprocess(t *testing.T) {
	runner := okRunner("r", "ok", 1)
	cfg := defaultTestConfig()
	cfg.Runner = runner
	a, err := NewAdapter(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, _ = a.Generate(context.Background(), scriptdocsapi.ReActRequest{
		Topic:    "test topic",
		Context:  "extra context",
		MaxSteps: 7,
	})

	// Parse the captured --json argument.
	if runner.lastArg == "" {
		t.Fatal("expected --json argument to be captured")
	}
	var payload reactBridgePayload
	if err := json.Unmarshal([]byte(runner.lastArg), &payload); err != nil {
		t.Fatalf("failed to unmarshal captured payload: %v", err)
	}
	if payload.Topic != "test topic" {
		t.Fatalf("expected topic 'test topic', got %q", payload.Topic)
	}
	if payload.Context != "extra context" {
		t.Fatalf("expected context 'extra context', got %q", payload.Context)
	}
	if payload.MaxSteps != 7 {
		t.Fatalf("expected max_steps 7, got %d", payload.MaxSteps)
	}
	if payload.OllamaURL != "http://localhost:11434" {
		t.Fatalf("expected ollama_url 'http://localhost:11434', got %q", payload.OllamaURL)
	}
	if payload.OllamaModel != "gemma4:e4b" {
		t.Fatalf("expected ollama_model 'gemma4:e4b', got %q", payload.OllamaModel)
	}
}

func TestGenerate_UsesDefaultMaxSteps(t *testing.T) {
	runner := okRunner("r", "ok", 1)
	cfg := defaultTestConfig()
	cfg.MaxStepsDefault = 8
	cfg.Runner = runner
	a, err := NewAdapter(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, _ = a.Generate(context.Background(), scriptdocsapi.ReActRequest{
		Topic:    "test",
		MaxSteps: 0, // should fall through to default
	})

	var payload reactBridgePayload
	_ = json.Unmarshal([]byte(runner.lastArg), &payload)
	if payload.MaxSteps != 8 {
		t.Fatalf("expected max_steps 8 (default), got %d", payload.MaxSteps)
	}
}

func TestGenerate_SubprocessError_ReturnsError(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.Runner = errRunner("python3 not found")
	a, err := NewAdapter(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = a.Generate(context.Background(), scriptdocsapi.ReActRequest{
		Topic: "test",
	})
	if err == nil {
		t.Fatal("expected error from subprocess failure")
	}
	if got := err.Error(); got == "" {
		t.Fatal("expected non-empty error message")
	}
}

func TestGenerate_BridgeReportsError_ReturnsError(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.Runner = bridgeErrRunner("Ollama connection refused")
	a, err := NewAdapter(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = a.Generate(context.Background(), scriptdocsapi.ReActRequest{
		Topic: "test",
	})
	if err == nil {
		t.Fatal("expected error from bridge error response")
	}
	if got := err.Error(); got == "" {
		t.Fatal("expected non-empty error message")
	}
}

func TestGenerate_InvalidJSONOutput_ReturnsError(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.Runner = invalidJSONRunner("this is not json")
	a, err := NewAdapter(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = a.Generate(context.Background(), scriptdocsapi.ReActRequest{
		Topic: "test",
	})
	if err == nil {
		t.Fatal("expected error from invalid JSON output")
	}
}

func TestGenerate_PartialStatus_ReturnsPartial(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.Runner = okRunner("partial result", "partial", 2)
	a, err := NewAdapter(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resp, err := a.Generate(context.Background(), scriptdocsapi.ReActRequest{
		Topic: "test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != "partial" {
		t.Fatalf("expected status 'partial', got %q", resp.Status)
	}
	if resp.StepsTaken != 2 {
		t.Fatalf("expected 2 steps, got %d", resp.StepsTaken)
	}
}

func TestGenerate_ContextCancellation_Propagated(t *testing.T) {
	// Use a runner that respects context by checking if it was called
	// (context cancellation is handled by process.Run / exec.CommandContext;
	//  we verify the runner receives the context).
	runner := okRunner("r", "ok", 1)
	cfg := defaultTestConfig()
	cfg.Runner = runner
	a, err := NewAdapter(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := context.Background()
	_, _ = a.Generate(ctx, scriptdocsapi.ReActRequest{Topic: "test"})

	if runner.lastCtx == nil {
		t.Fatal("expected context to be passed to runner")
	}
}

// ── Compile-time pin ────────────────────────────────────────────────

func TestAdapter_SatisfiesReActPort(t *testing.T) {
	// Compile-time assertion: *Adapter implements ReActPort.
	// If this fails to compile, ReActPort's Generate signature drifted.
	var _ scriptdocsapi.ReActPort = (*Adapter)(nil)
}
