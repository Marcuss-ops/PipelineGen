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

	process "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"
	coreembedding "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
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
// Empty text short-circuits to (EmbeddingResult{}, nil) so application
// callers do not have to special-case blank input. All errors wrap the
// original subprocess output (via process.Run) for post-mortem visibility.
//
// QDRANT-001b (July 2026): the return type is now EmbeddingResult instead
// of []float32. The sidecar emits the canonical envelope
// {"embedding": [...], "dimensions": 768, "model": "<name>",
// "model_version": "<version>", "error": ""}. The function parses the
// full envelope and returns it; the caller unwraps .Vector when only the
// raw vector is needed.
func (e *PythonScriptEmbedder) Embed(ctx context.Context, text string) (coreembedding.EmbeddingResult, error) {
	if text == "" {
		return coreembedding.EmbeddingResult{}, nil
	}

	result, err := process.Run(ctx, e.pythonBin,
		[]string{e.scriptPath, "--text", text},
		e.opts,
	)
	if err != nil {
		return coreembedding.EmbeddingResult{}, fmt.Errorf("embedding generation failed: %w", err)
	}

	// QDRANT-001 / QDRANT-001b: parse the canonical sidecar envelope.
	// Legacy sidecars that emit a raw []float32 array are still accepted
	// via the fallback path (Model and ModelVersion set to empty).
	var envelope struct {
		Embedding    []float32 `json:"embedding"`
		Dimensions   int       `json:"dimensions"`
		Model        string    `json:"model"`
		ModelVersion string    `json:"model_version"`
		ContractHash string    `json:"contract_hash"`
		Error        string    `json:"error"`
	}
	if err := json.Unmarshal([]byte(result.Output), &envelope); err != nil {
		// Fallback: try parsing as a raw []float32 array (legacy sidecar).
		var legacy []float32
		if err2 := json.Unmarshal([]byte(result.Output), &legacy); err2 != nil {
			return coreembedding.EmbeddingResult{}, fmt.Errorf("failed to parse embedding JSON: %w (output: %s)", err, result.Output)
		}
		return coreembedding.EmbeddingResult{
			Vector:     legacy,
			Dimensions: len(legacy),
		}, nil
	}

	// Fail-loud on sidecar-reported error.
	if envelope.Error != "" {
		return coreembedding.EmbeddingResult{}, fmt.Errorf("sidecar error: %s", envelope.Error)
	}

	if len(envelope.Embedding) == 0 {
		return coreembedding.EmbeddingResult{}, fmt.Errorf("sidecar returned empty embedding vector")
	}

	// QDRANT-001b: validate declared dimensions match actual vector length.
	if envelope.Dimensions > 0 && envelope.Dimensions != len(envelope.Embedding) {
		return coreembedding.EmbeddingResult{}, fmt.Errorf(
			"dimension mismatch: declared %d, actual embedding length %d",
			envelope.Dimensions, len(envelope.Embedding))
	}

	return coreembedding.EmbeddingResult{
		Vector:       envelope.Embedding,
		Dimensions:   envelope.Dimensions,
		Model:        envelope.Model,
		ModelVersion: envelope.ModelVersion,
		ContractHash: envelope.ContractHash,
	}, nil
}
