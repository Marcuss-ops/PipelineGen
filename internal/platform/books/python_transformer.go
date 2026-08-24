// Package pythontransformer — concrete BookTransformer
// implementation for the books capability (Fase 7 Spina Dorsale,
// July 2026). The transformer wraps the book_summarizer.py
// subprocess invocation and lives at the
// internal/infrastructure layer so the books apply layer
// (internal/application/books) is free of os/exec imports
// (godlike/06 "one owner per fact" — Python execution is an
// implementation detail, not a domain concern).
//
// Drop-in replacement for the legacy inline exec.CommandContext in
// books/service.go::ProcessBook + ProcessBookWithProgress. The
// user-visible pipeline behaviour is unchanged; the only externally
// visible delta is the port shape (books.Service no longer
// imports os/exec, database/sql, or path/filepath).
package pythontransformer

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/books"
)

// Config is the canonical pythontransformer configuration
// surface. Fase 7 review-fix #3 BACKFILL (July 2026): the
// ScriptPath / PythonBin / Enabled fields have been moved OUT of
// books.Config (apply layer) and INTO this concrete-owned
// Config (godlike/06 "one canonical owner per fact" — Python
// subprocess details belong with the Python-aware concrete, not
// with the apply-layer Service). Before BACKFILL, books.Config
// carried these fields as a duplicate-fact; after BACKFILL, this
// Config is the SSOT for any caller that needs to spawn the
// book_summarizer.py subprocess.
type Config struct {
	ScriptPath string `yaml:"script_path"`
	PythonBin  string `yaml:"python_bin"`
	Enabled    bool   `yaml:"enabled"`
}

// SubprocessTransformer is the canonical books.BookTransformer
// implementation. It spawns scripts/bridges/book_summarizer.py via
// the configured Python interpreter and parses the canonical
// [RESULT] {json} block from stdout.
//
// Build:
//
//	NewSubprocessTransformer(&pythontransformer.Config{ScriptPath, PythonBin, Enabled}, log)
//
// The constructor ABS-ifies ScriptPath so the working directory
// for the subprocess (filepath.Dir(t.scriptPath)) is stable
// regardless of caller CWD. matches books/service.go pre-Fase-7
// semantics (legacy buildArgs() helper).
type SubprocessTransformer struct {
	cfg        *Config
	scriptPath string
	log        *zap.Logger
}

// Compile-time assertion (AGENTS.md Pattern 0): SubprocessTransformer
// satisfies the canonical books.BookTransformer port. A future port
// drift (method added/removed) is a build failure here, not at
// the first caller site.
var _ books.BookTransformer = (*SubprocessTransformer)(nil)

// NewSubprocessTransformer constructs the canonical concrete.
// Fails closed at boot if cfg or log are nil, or if ScriptPath,
// PythonBin, or Enabled is false (per godlike/07 §"No fake
// availability" — the transformer must never be instantiated in
// a half-configured or disabled state that would silently fail at
// first Transform call).
//
// Enabled fail-closed (added in the reviewer-feedback #2 round
// post the initial BACKFILL commit): the pythontransformer.Config
// Enabled field was previously write-only at runtime — composition
// root set it but the constructor didn't gate on it, leaving the
// apply-layer Service.enabled field as the only runtime guard.
// The fail-closed here consolidates the gate: a disabled
// transformer cannot be constructed at all, so the booksService
// wired by BuildDomainBundle cannot be invoked via the
// composition-time path. The apply-layer Service.enabled
// remains as the runtime per-request gate (orthogonal concern:
// future dynamic-config-reload use cases).
func NewSubprocessTransformer(cfg *Config, log *zap.Logger) (*SubprocessTransformer, error) {
	if cfg == nil {
		return nil, errors.New("pythontransformer: cfg is required")
	}
	if log == nil {
		return nil, errors.New("pythontransformer: log is required")
	}
	if cfg.ScriptPath == "" {
		return nil, errors.New("pythontransformer: cfg.ScriptPath is empty — fail-closed per godlike/07 no-fake-availability")
	}
	if cfg.PythonBin == "" {
		return nil, errors.New("pythontransformer: cfg.PythonBin is empty — fail-closed per godlike/07 no-fake-availability")
	}
	if !cfg.Enabled {
		return nil, errors.New("pythontransformer: cfg.Enabled is false — fail-closed per godlike/07 no-fake-availability")
	}
	scriptPath := cfg.ScriptPath
	if !filepath.IsAbs(scriptPath) {
		if abs, err := filepath.Abs(scriptPath); err == nil {
			scriptPath = abs
		}
	}
	return &SubprocessTransformer{
		cfg:        cfg,
		scriptPath: scriptPath,
		log:        log,
	}, nil
}

// Transform runs the book-summarisation pipeline against the
// resolved source + request shape and parses the [RESULT] {json}
// block from stdout. Returns a wrapped exec error on non-zero
// exit (the wrapper preserves the captured stdout/stderr so ops
// can grep logs for the script's own diagnostic lines).
func (t *SubprocessTransformer) Transform(ctx context.Context, req *books.TransformRequest) (*books.TransformResult, error) {
	args, err := t.buildArgs(req)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, t.cfg.PythonBin, args...)
	cmd.Dir = filepath.Dir(t.scriptPath)

	source := req.Source.LocalPath
	if source == "" {
		source = "gdoc:" + req.Source.GoogleDocID
	}
	t.log.Info("processing book via script",
		zap.String("source", source),
		zap.String("script", t.scriptPath),
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("book processing failed: %w, output: %s",
			err, strings.TrimSpace(string(output)))
	}
	return t.parseOutput(string(output), req), nil
}

// TransformWithProgress is the [PROGRESS] %d %s streaming variant
// of Transform. The progress-line parser is the same as
// pre-Fase-7 books/service.go::ProcessBookWithProgress. The
// callers (use cases + handlers) get progress updates via the
// callback; the function still returns the canonical
// TransformResult on success.
//
// NOTE: this method is NOT part of the books.BookTransformer
// port interface (EXPAND window: only the single-shot Transform
// is exposed on the port; progress-callback consumers reach the
// concrete directly via the SubprocessTransformer type). The
// next-wave port expansion will fold both methods onto the
// BookTransformer interface OR move progress reporting into the
// apply layer.
func (t *SubprocessTransformer) TransformWithProgress(ctx context.Context, req *books.TransformRequest, onProgress func(int, string)) (*books.TransformResult, error) {
	args, err := t.buildArgs(req)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, t.cfg.PythonBin, args...)
	cmd.Dir = filepath.Dir(t.scriptPath)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start book processor: %w", err)
	}

	var fullOutput strings.Builder
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		fullOutput.WriteString(line)
		fullOutput.WriteString("\n")

		if strings.HasPrefix(line, "[PROGRESS] ") {
			trimmed := strings.TrimPrefix(line, "[PROGRESS] ")
			if pct, msg, ok := parseProgressLine(trimmed); ok {
				if onProgress != nil {
					onProgress(pct, msg)
				}
				continue
			}
		}

		t.log.Debug("book script output", zap.String("line", line))
	}

	stderrBytes, _ := io.ReadAll(stderr)

	if err := cmd.Wait(); err != nil {
		errOutput := fullOutput.String() + "\n" + string(stderrBytes)
		return nil, fmt.Errorf("book processing failed: %w, output: %s",
			err, strings.TrimSpace(errOutput))
	}
	return t.parseOutput(fullOutput.String(), req), nil
}

// buildArgs constructs the CLI args for book_summarizer.py from
// a TransformRequest. Moved verbatim from
// internal/application/books/service.go (pre-Fase-7). Kept private
// to the concrete — book TransformerRequest fields are the public
// contract; CLI flag shape is an internal detail.
func (t *SubprocessTransformer) buildArgs(req *books.TransformRequest) ([]string, error) {
	if req.Source.LocalPath == "" && req.Source.GoogleDocID == "" {
		return nil, errors.New("source: local_path or google_doc_id is required")
	}

	args := []string{filepath.Base(t.scriptPath)}

	if req.Source.GoogleDocID != "" {
		args = append(args, "--google-doc-id", req.Source.GoogleDocID)
	} else {
		args = append(args, "--file", req.Source.LocalPath)
	}

	model := req.Model
	if model == "" {
		model = "gemma4:e4b"
	}
	args = append(args, "--model", model)

	if req.PagesPerChunk > 0 {
		args = append(args, "--pages-per-chunk", fmt.Sprintf("%d", req.PagesPerChunk))
	}
	chunkSize := req.ChunkSize
	if chunkSize <= 0 {
		chunkSize = 12000
	}
	args = append(args, "--chunk-size", fmt.Sprintf("%d", chunkSize))
	if req.MaxChunks > 0 {
		args = append(args, "--max-chunks", fmt.Sprintf("%d", req.MaxChunks))
	}

	if req.OverlapSize > 0 {
		args = append(args, "--overlap-size", fmt.Sprintf("%d", req.OverlapSize))
	} else {
		args = append(args, "--overlap-size", "2000")
	}

	ollamaURL := req.OllamaURL
	if ollamaURL == "" {
		ollamaURL = "http://127.0.0.1:11434"
	}
	args = append(args, "--ollama-url", ollamaURL)

	if req.Instruction != "" {
		args = append(args, "--instruction", req.Instruction)
	}
	if req.OutputPath != "" {
		args = append(args, "--output", req.OutputPath)
	}

	if req.DriveFolderID != "" {
		args = append(args, "--drive-folder-id", req.DriveFolderID)
	}
	if req.Language != "" {
		args = append(args, "--language", req.Language)
	}
	if req.TranslateOnly {
		args = append(args, "--translate-only")
	}
	if req.GeneratePDF {
		args = append(args, "--generate-pdf")
	}
	if req.PDFStyle != "" {
		args = append(args, "--pdf-style", req.PDFStyle)
	}

	return args, nil
}

// parseOutput extracts the canonical TransformResult from the
// Python script's stdout. Moved verbatim from
// internal/application/books/service.go::parseOutput (pre-Fase-7).
// Output de-truncation at 300 chars preserved for log brevity.
func (t *SubprocessTransformer) parseOutput(outputStr string, req *books.TransformRequest) *books.TransformResult {
	preview := outputStr
	if len(preview) > 300 {
		preview = preview[:300]
	}
	t.log.Info("book processed", zap.String("output_preview", preview))

	result := &books.TransformResult{
		Language: req.Language,
	}

	if idx := strings.Index(outputStr, "[RESULT]"); idx >= 0 {
		rawJSON := outputStr[idx+8:]
		if closeIdx := strings.LastIndex(rawJSON, "}"); closeIdx >= 0 {
			rawJSON = rawJSON[:closeIdx+1]
		}
		jsonStr := strings.TrimSpace(rawJSON)
		var resultJSON map[string]any
		if err := json.Unmarshal([]byte(jsonStr), &resultJSON); err == nil {
			if v, ok := resultJSON["output_file"].(string); ok && v != "" {
				result.OutputPath = v
			}
			if v, ok := resultJSON["pdf_file"].(string); ok && v != "" {
				result.PDFPath = v
			}
			if v, ok := resultJSON["language"].(string); ok && v != "" {
				result.Language = v
			}
			if v, ok := resultJSON["chunks_processed"].(float64); ok {
				result.ChunksProcessed = int(math.Round(v))
			}
			if drive, ok := resultJSON["drive"].(map[string]any); ok {
				if v, ok := drive["folder"].(string); ok && v != "" {
					result.DriveFolderURL = v
				}
				if v, ok := drive["document"].(string); ok && v != "" {
					result.DriveDocURL = v
				}
				if v, ok := drive["pdf"].(string); ok && v != "" {
					result.DrivePDFURL = v
				}
			}
		}
	} else {
		lines := strings.Split(outputStr, "\n")
		for _, line := range lines {
			if strings.Contains(line, "Saved summary to:") {
				if parts := strings.Split(line, "Saved summary to:"); len(parts) > 1 {
					result.OutputPath = strings.TrimSpace(parts[1])
				}
			}
			if strings.Contains(line, "Generated PDF:") {
				if parts := strings.Split(line, "Generated PDF:"); len(parts) > 1 {
					result.PDFPath = strings.TrimSpace(parts[1])
				}
			}
			if strings.Contains(line, "Uploaded to Google Docs:") {
				if parts := strings.Split(line, "Uploaded to Google Docs:"); len(parts) > 1 {
					result.DriveDocURL = strings.TrimSpace(parts[1])
				}
			}
		}
	}

	if result.OutputPath == "" && req.OutputPath != "" {
		result.OutputPath = req.OutputPath
	}

	return result
}

// parseProgressLine decodes the "[PROGRESS] <pct>% <msg>" line
// shape emitted by book_summarizer.py. pct must be in [0, 100];
// the line must contain exactly one "%" separator. Returns
// ok=false on any malformed line (parser is permissive — the
// apply layer's ProgressReporter decides whether to surface the
// partial message via callback).
func parseProgressLine(s string) (int, string, bool) {
	parts := strings.SplitN(strings.TrimSpace(s), "%", 2)
	if len(parts) != 2 {
		return 0, "", false
	}
	pct, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || pct < 0 || pct > 100 {
		return 0, "", false
	}
	return pct, strings.TrimSpace(parts[1]), true
}
