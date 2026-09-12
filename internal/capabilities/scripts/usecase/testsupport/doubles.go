// Package testsupport holds the cross-package test doubles used by the
// script-generation test suites.
//
// Reason it exists (2026-09-12): the generation core moved out of
// internal/capabilities/scripts/usecase into
// internal/capabilities/scripts/usecase/generation. Several tests that stay
// in the parent package (clip-resolution, end-to-end generate, timing)
// still need an ollama generator fake and an Engine built with lenient
// segment validation, but a package's internal `_test.go` helpers are not
// importable. These doubles therefore live here, in a normal package.
//
// Nothing here may import usecase or generation's internal test helpers;
// the package deliberately depends only on the low-level ports so both
// sides of the boundary can use it.
package testsupport

import (
	"context"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase/generation"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ── Ollama generator fake ─────────────────────────────────────────────────

// FakeOllamaGen is a ports.ScriptGenerator fake injected into Engine.
type FakeOllamaGen struct {
	Calls       atomic.Int32
	Result      *scriptports.GenerationResult
	ReturnErr   error
	CapturedReq atomic.Pointer[scriptports.TextGenerationRequest]
}

// GenerateScript mirrors the real owner-owned boundary: like
// *ollama.Generator.GenerateScript, it emits the canonical ollama/generate
// operation itself. The Engine fan-out must not re-time the same boundary,
// so this fake records exactly once per call and leaves the engine with
// nothing extra to measure.
func (f *FakeOllamaGen) GenerateScript(ctx context.Context, req scriptports.TextGenerationRequest) (*scriptports.GenerationResult, error) {
	f.Calls.Add(1)
	f.CapturedReq.Store(&req)
	var result *scriptports.GenerationResult
	err := kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
		Stage:     kernobs.StageGenerate,
		Component: kernobs.ComponentOllama,
		Operation: kernobs.OperationGenerate,
		Items:     1,
	}, func(context.Context) error {
		if f.ReturnErr != nil {
			return f.ReturnErr
		}
		if f.Result != nil {
			result = f.Result
		} else {
			result = DefaultFakeResult()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// CanonicalFixtureText is the prose used as the default V1 `text` field
// across fake ollama results. Tests assert on this exact text. Any non-V1
// payload fails the fresh-path decode with ErrModelOutputMalformed, so
// DefaultFakeResult must emit canonical JSON.
const CanonicalFixtureText = "This is a generated script with multiple sentences and narrative depth."

// CanonicalFixture returns the canonical V1 JSON string used by
// DefaultFakeResult's Script field.
func CanonicalFixture() string {
	return `{"schema_version":1,"text":"` + CanonicalFixtureText + `","specscene":{"version":1,"scenes":[{"id":"scene-0","index":0,"text":"` + CanonicalFixtureText + `","kind":"narration","bindings":{}}]}}`
}

// DefaultFakeResult is the canned generation result used when a
// FakeOllamaGen is constructed without an explicit Result.
func DefaultFakeResult() *scriptports.GenerationResult {
	return &scriptports.GenerationResult{
		Script:      CanonicalFixture(),
		WordCount:   12,
		EstDuration: 4,
		Model:       "llama3:8b",
		Prompt:      "Write a script about testing.",
	}
}

// BuildTestEngine returns a generation Engine wired to the supplied fake
// generator and a nil memory gate, with lenient segment tolerances so unit
// tests are not fighting the production QA policy.
//
// A nil memory gate is safe only for tests that exercise NON-memory paths
// (UseMemory=false or ForceRefresh=true); tests that assert memory-path
// behaviour must construct the engine in-package where the narrow
// memory-gate types are visible.
func BuildTestEngine(gen *FakeOllamaGen) *generation.Engine {
	e := generation.NewEngine(gen, nil, zap.NewNop())
	e.ConfigureSegmentValidation(50, 50, 0)
	return e
}

// ── Post-processor fake ───────────────────────────────────────────────────

// StubPostProcessor implements adapters.PostProcessor for tests. It returns
// a canned result after SleepMs so the registry's per-stage elapsed
// timestamp is observable and distinguishable across processors.
//
// Policy always returns BestEffort so the registry's ValidateRequested +
// Run gate treats a downstream failure as a warning rather than aborting
// Execute; processor-policy gates are out of scope for these tests.
type StubPostProcessor struct {
	ProcessorName string
	SleepMs       int
	Result        *adapters.PostProcessResult
}

// Name reports the canonical processor name.
func (s *StubPostProcessor) Name() adapters.ProcessorName {
	return adapters.ProcessorName(s.ProcessorName)
}

// Policy always reports BestEffort.
func (s *StubPostProcessor) Policy(*scriptpkg.ResolvedGenerationPlan) adapters.ProcessorPolicy {
	return adapters.ProcessorBestEffort
}

// Process returns the canned result after the configured delay.
func (s *StubPostProcessor) Process(
	_ context.Context,
	_ *scriptpkg.ResolvedGenerationPlan,
	_ adapters.ProcessInput,
) (*adapters.PostProcessResult, error) {
	if s.SleepMs > 0 {
		time.Sleep(time.Duration(s.SleepMs) * time.Millisecond)
	}
	return s.Result, nil
}
