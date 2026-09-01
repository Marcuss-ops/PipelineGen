package local

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	entityports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

const (
	DeviceAuto = "auto"
	DeviceCPU  = "cpu"
	DeviceGPU  = "gpu"
)

// HybridExtractor routes local extraction to an optional GPU bridge and keeps
// the deterministic Go extractor as the explicit CPU/auto fallback. The GPU
// bridge performs NER locally; it never calls an LLM.
type HybridExtractor struct {
	cpu       entityports.EntityExtractor
	gpu       entityports.EntityExtractor
	available func(context.Context) bool
	once      sync.Once
	gpuOK     bool
}

func NewHybridExtractor() *HybridExtractor {
	gpu := NewGPUExtractorFromEnvironment()
	return &HybridExtractor{
		cpu:       NewExtractor(),
		gpu:       gpu,
		available: gpu.Available,
	}
}

func (e *HybridExtractor) ExtractEntities(ctx context.Context, req scriptpkg.EntityExtractionRequest) (*scriptpkg.EntityResult, error) {
	device := strings.ToLower(strings.TrimSpace(req.Device))
	if device == "" {
		device = DeviceAuto
	}
	switch device {
	case DeviceCPU:
		return e.cpu.ExtractEntities(ctx, req)
	case DeviceGPU:
		if e.gpu == nil || !e.gpuAvailable(ctx) {
			return nil, fmt.Errorf("%w: GPU semantic extractor unavailable", scriptpkg.ErrEntityExtractorUnavailable)
		}
		return e.gpu.ExtractEntities(ctx, req)
	case DeviceAuto:
		if e.gpu != nil && e.gpuAvailable(ctx) {
			result, err := e.gpu.ExtractEntities(ctx, req)
			if err == nil {
				return result, nil
			}
		}
		return e.cpu.ExtractEntities(ctx, req)
	default:
		return nil, fmt.Errorf("%w: unsupported local extraction device %q", scriptpkg.ErrEntityExtractorUnavailable, req.Device)
	}
}

func (e *HybridExtractor) gpuAvailable(ctx context.Context) bool {
	e.once.Do(func() { e.gpuOK = e.available != nil && e.available(ctx) })
	return e.gpuOK
}

// GPUExtractor is a subprocess adapter around the optional spaCy/CuPy bridge.
// Keeping the Python ML boundary here follows the repository's existing ML
// bridge convention and avoids adding CUDA-linked Go dependencies.
type GPUExtractor struct {
	python string
	script string
}

func NewGPUExtractorFromEnvironment() *GPUExtractor {
	python := strings.TrimSpace(os.Getenv("PIPELINEGEN_NLP_PYTHON"))
	if python == "" {
		python = "python3"
	}
	script := strings.TrimSpace(os.Getenv("PIPELINEGEN_NLP_GPU_BRIDGE"))
	if script == "" {
		script = filepath.Join("scripts", "bridges", "local_nlp_gpu.py")
	}
	return &GPUExtractor{python: python, script: script}
}

func (e *GPUExtractor) Available(ctx context.Context) bool {
	if e == nil || strings.TrimSpace(e.script) == "" {
		return false
	}
	if _, err := exec.LookPath(e.python); err != nil {
		return false
	}
	if _, err := os.Stat(e.script); err != nil {
		return false
	}
	cmd := exec.CommandContext(ctx, e.python, e.script, "--check-gpu")
	return cmd.Run() == nil
}

func (e *GPUExtractor) ExtractEntities(ctx context.Context, req scriptpkg.EntityExtractionRequest) (*scriptpkg.EntityResult, error) {
	if e == nil || !e.Available(ctx) {
		return nil, fmt.Errorf("%w: GPU bridge or CUDA runtime unavailable", scriptpkg.ErrEntityExtractorUnavailable)
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("gpu extractor encode request: %w", err)
	}
	cmd := exec.CommandContext(ctx, e.python, e.script)
	cmd.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: GPU bridge failed: %v (%s)", scriptpkg.ErrEntityExtractorUnavailable, err, strings.TrimSpace(stderr.String()))
	}
	var result scriptpkg.EntityResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return nil, fmt.Errorf("gpu extractor decode response: %w", err)
	}
	return &result, nil
}

var _ entityports.EntityExtractor = (*HybridExtractor)(nil)
var _ entityports.EntityExtractor = (*GPUExtractor)(nil)
