// Package scriptdocs provides the concrete ReActPort adapter that
// invokes the Python ReAct agent bridge (scripts/bridges/react_agent.py)
// via subprocess execution.
//
// godlike/06 SSOT (one canonical owner per fact):
//   - ReActPort interface lives ONLY at internal/api/script-docs/handler.go
//   - This adapter is the SOLE concrete production implementation
//   - The Python bridge script lives ONLY at scripts/bridges/react_agent.py
//
// godlike/07 NO-FAKE-AVAILABILITY:
//   - nil OllamaURL → ErrReActAdapterMisconfigured (fail-closed at construction)
//   - subprocess failure → typed error propagation (no silent success)
//   - invalid JSON output → typed error (no silent truncation)
package scriptdocs

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	scriptdocsapi "github.com/Marcuss-ops/PipelineGen/internal/api/script-docs"
	processpkg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"
)

// ProcessRunner abstracts subprocess execution for testability.
// In production, defaultProcessRunner wraps process.Run;
// tests inject a stub that captures argv without spawning a process.
type ProcessRunner interface {
	Run(ctx context.Context, name string, args []string, opts processpkg.Options) (*processpkg.Result, error)
}

// defaultProcessRunner delegates to process.Run (production path).
type defaultProcessRunner struct{}

// Run implements ProcessRunner by delegating to process.Run.
func (d defaultProcessRunner) Run(ctx context.Context, name string, args []string, opts processpkg.Options) (*processpkg.Result, error) {
	return processpkg.Run(ctx, name, args, opts)
}

// Compile-time pin: defaultProcessRunner satisfies ProcessRunner.
var _ ProcessRunner = defaultProcessRunner{}

// AdapterConfig holds the configuration for the ReAct Python bridge adapter.
type AdapterConfig struct {
	// PythonBin is the Python binary path (default: "python3").
	PythonBin string
	// ScriptDir is the project root containing scripts/bridges/.
	ScriptDir string
	// OllamaURL is the Ollama server URL (required, fail-closed).
	OllamaURL string
	// OllamaModel is the model name (default: "gemma4:e4b").
	OllamaModel string
	// MaxStepsDefault is the default max ReAct steps when the request
	// omits MaxSteps (default: 5).
	MaxStepsDefault int
	// Runner overrides the subprocess runner for testing.
	Runner ProcessRunner
}

// ErrReActAdapterMisconfigured is the canonical typed sentinel for
// adapter misconfiguration (missing OllamaURL or ScriptDir).
var ErrReActAdapterMisconfigured = fmt.Errorf("scriptdocs adapter: misconfigured (OllamaURL and ScriptDir are required)")

// Adapter implements scriptdocsapi.ReActPort by invoking the Python
// ReAct agent bridge script via subprocess.
//
// Compile-time pin: Adapter satisfies the canonical ReActPort interface.
// Future drift in ReActPort's Generate signature surfaces as a build
// failure here, not a runtime panic.
var _ scriptdocsapi.ReActPort = (*Adapter)(nil)

// Adapter is the concrete ReActPort implementation that delegates
// to scripts/bridges/react_agent.py via subprocess.
type Adapter struct {
	pythonBin       string
	scriptPath      string
	ollamaURL       string
	ollamaModel     string
	maxStepsDefault int
	runner          ProcessRunner
}

// NewAdapter constructs a ReActPort adapter. Returns
// ErrReActAdapterMisconfigured when OllamaURL or ScriptDir is empty
// (fail-closed at construction per godlike/07).
//
// cfg.Runner may be nil → defaultProcessRunner is used (production path).
// cfg.PythonBin empty → "python3".
// cfg.OllamaModel empty → "gemma4:e4b".
// cfg.MaxStepsDefault ≤ 0 → 5.
func NewAdapter(cfg AdapterConfig) (*Adapter, error) {
	if cfg.OllamaURL == "" || cfg.ScriptDir == "" {
		return nil, ErrReActAdapterMisconfigured
	}
	pythonBin := cfg.PythonBin
	if pythonBin == "" {
		pythonBin = "python3"
	}
	model := cfg.OllamaModel
	if model == "" {
		model = "gemma4:e4b"
	}
	maxSteps := cfg.MaxStepsDefault
	if maxSteps <= 0 {
		maxSteps = 5
	}
	runner := cfg.Runner
	if runner == nil {
		runner = defaultProcessRunner{}
	}
	return &Adapter{
		pythonBin:       pythonBin,
		scriptPath:      filepath.Join(cfg.ScriptDir, "bridges", "react_agent.py"),
		ollamaURL:       cfg.OllamaURL,
		ollamaModel:     model,
		maxStepsDefault: maxSteps,
		runner:          runner,
	}, nil
}

// reactBridgePayload is the JSON payload sent to the Python bridge
// via --json CLI arg. Fields mirror the bridge's expected input.
type reactBridgePayload struct {
	Topic       string `json:"topic"`
	Context     string `json:"context,omitempty"`
	MaxSteps    int    `json:"max_steps"`
	OllamaURL   string `json:"ollama_url"`
	OllamaModel string `json:"ollama_model"`
}

// reactBridgeResponse is the JSON output from the Python bridge on stdout.
type reactBridgeResponse struct {
	Result     string           `json:"result"`
	Status     string           `json:"status"`
	StepsTaken int              `json:"steps_taken"`
	Evidence   []map[string]any `json:"evidence,omitempty"`
	Error      string           `json:"error,omitempty"`
}

// Generate implements scriptdocsapi.ReActPort. It constructs a JSON
// payload, invokes the Python bridge subprocess, and parses the JSON
// response.
//
// godlike/07 NO-FAKE-AVAILABILITY:
//   - subprocess exit non-zero → typed error (output included for diagnostics)
//   - invalid JSON output → typed error
//   - bridge reports status "error" → typed error with bridge error message
//   - ctx cancelled → subprocess is killed via exec.CommandContext
func (a *Adapter) Generate(ctx context.Context, req scriptdocsapi.ReActRequest) (scriptdocsapi.ReActResponse, error) {
	maxSteps := req.MaxSteps
	if maxSteps <= 0 {
		maxSteps = a.maxStepsDefault
	}

	payload := reactBridgePayload{
		Topic:       req.Topic,
		Context:     req.Context,
		MaxSteps:    maxSteps,
		OllamaURL:   a.ollamaURL,
		OllamaModel: a.ollamaModel,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return scriptdocsapi.ReActResponse{}, fmt.Errorf("scriptdocs adapter: marshal payload: %w", err)
	}

	result, err := a.runner.Run(ctx, a.pythonBin,
		[]string{a.scriptPath, "--json", string(payloadBytes)},
		processpkg.Options{
			Timeout:        0, // uses process.DefaultTimeout (10min) via process.Run
			CombinedOutput: true,
		},
	)
	if err != nil {
		return scriptdocsapi.ReActResponse{}, fmt.Errorf("scriptdocs adapter: subprocess failed: %w", err)
	}

	var bridgeResp reactBridgeResponse
	if err := json.Unmarshal([]byte(result.Output), &bridgeResp); err != nil {
		return scriptdocsapi.ReActResponse{}, fmt.Errorf(
			"scriptdocs adapter: failed to parse bridge JSON output: %w (output: %.500s)",
			err, result.Output,
		)
	}

	if bridgeResp.Error != "" {
		return scriptdocsapi.ReActResponse{}, fmt.Errorf("scriptdocs adapter: bridge error: %s", bridgeResp.Error)
	}

	if bridgeResp.Status == "error" {
		errMsg := bridgeResp.Error
		if errMsg == "" {
			errMsg = "bridge reported error status with no message"
		}
		return scriptdocsapi.ReActResponse{}, fmt.Errorf("scriptdocs adapter: bridge error: %s", errMsg)
	}

	return scriptdocsapi.ReActResponse{
		Result:     bridgeResp.Result,
		Status:     bridgeResp.Status,
		StepsTaken: bridgeResp.StepsTaken,
	}, nil
}
