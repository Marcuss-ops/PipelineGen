// Package embeddings provides infrastructure-level Embedder implementations.
//
// Two concrete Embedder implementations live here:
//
//   - PythonScriptEmbedder: subprocess invocation of a Python script.
//   - HTTPEmbedder: HTTP client to a Python sidecar embedding server.
//
// Both satisfy the canonical Embedder interface in internal/domain/asset.
// Application layer (e.g. application/association, application/realtime)
// depends on the interface; this package is wired in internal/app/.
package embeddings

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	coreembedding "github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	process "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"
)

// PythonScriptEmbedder generates embeddings by shelling out to a Python
// sidecar script. This is the original concrete implementation; it
// preserves the proven JSON-over-stdout contract used by
// bridges/generate_embedding.py so existing scripts work without
// changes. Although os/exec is a process boundary, the trade-off is
// acceptable here because the Python project is the canonical
// embedding authority (E5 multilingual base) and a pure-Go port would
// regress quality.
//
// Future: spawn a long-running sidecar server (HTTPEmbedder) instead
// of per-call subprocess startup, which costs ~1s on every call.
//
// Security: invocations go through process.Run (which uses
// exec.CommandContext with no shell) per AGENTS.md 🧰 utilities —
// Direct shell invocation via -c $cmd would be an injection risk and
// is explicitly forbidden.
type PythonScriptEmbedder struct {
	pythonBin  string
	scriptPath string
	opts       process.Options
}

// NewPythonScriptEmbedder creates an Embedder that runs
// `pythonBin scriptsDir/bridges/generate_embedding.py --text <text>`
// and parses the JSON-encoded []float32 result from combined stdout.
//
// pythonBin is typically "python3"; scriptsDir points at the project
// root (e.g. /opt/pipelinegen). The default 10-minute timeout applies
// — silently extended when ctx already carries an earlier deadline.
func NewPythonScriptEmbedder(pythonBin, scriptsDir string) coreembedding.Embedder {
	return &PythonScriptEmbedder{
		pythonBin:  pythonBin,
		scriptPath: filepath.Join(scriptsDir, "bridges", "generate_embedding.py"),
		opts:       process.DefaultOptions(),
	}
}

// Embed runs the Python sidecar and parses the JSON result.
// Empty text short-circuits to (nil, nil) so application callers do
// not have to special-case blank input. All errors wrap the original
// subprocess output (via platform.Run) for post-mortem visibility.
func (e *PythonScriptEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return nil, nil
	}

	result, err := process.Run(ctx, e.pythonBin,
		[]string{e.scriptPath, "--text", text},
		e.opts,
	)
	if err != nil {
		return nil, fmt.Errorf("embedding generation failed: %w", err)
	}

	var embedding []float32
	if err := json.Unmarshal([]byte(result.Output), &embedding); err != nil {
		return nil, fmt.Errorf("failed to parse embedding JSON: %w (output: %s)", err, result.Output)
	}

	return embedding, nil
}
