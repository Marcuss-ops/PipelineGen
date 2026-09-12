// Package testsupport holds the cross-package test doubles used by the
// script-generation test suites.
//
// Reason it exists (2026-09-12): the generation core moved out of
// internal/capabilities/scripts/usecase into
// internal/capabilities/scripts/usecase/gencore. Several tests that stay
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

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
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

// NOTE: the Engine builder is deliberately NOT here. Building a
// *gencore.Engine would make this package import usecase/gencore, which
// would then forbid gencore's own tests from importing testsupport
// (import cycle not allowed in test). The usecase package defines its own
// buildTestEngine helper in a _test.go file instead.

// ── Fixtures ──────────────────────────────────────────────────────────────

// ItemForTimings builds a minimal text-only GenerationItemV2 that:
//   - emits SourceSpec.Type=SourceText so source-resolution is a no-op
//     (no clip-search / Qdrant / drive calls)
//   - opts-in to ExtractEntities + GenerateMetadata so buildPostprocessorList
//     emits "entities" + "metadata" in plan.Postprocessors (both Required-class
//     and registered in the test's registry — the canonical two procs whose
//     per-stage variance the timing assertion inspects)
//   - leaves SaveToDB false (NormalizeItem overrides it to true) and
//     VoiceoverFolderID empty so ResolveVoiceoverFolderForItem short-circuits
//
// SourceSpec.Topic is populated so the model's prompts carry an anchor; the
// validator reads Topic + SourceText + Title through ResolveGenerationPlan
// downstream.
//
// Lives here rather than in a _test.go file because both usecase and
// usecase/gencore tests need it and test helpers are not importable.
func ItemForTimings() scriptpkg.GenerationItemV2 {
	return scriptpkg.GenerationItemV2{
		ID:       "iss3-timings-item",
		Title:    "Stage Durations Plumbing",
		Language: "en",
		Tone:     "neutral",
		Style:    "standard",
		Model:    "llama3:8b",
		Source: scriptpkg.SourceSpec{
			Type:  scriptpkg.SourceText,
			Topic: "Stage durations plumbing",
			// SourceText length is well above any sensible validator minimum
			// so ValidateItem succeeds even if the validator enforces one.
			SourceText: "This is a generated script with multiple sentences and narrative depth. A canonical test about per-stage postprocessor duration plumbing.",
		},
		ScriptParams: scriptpkg.ScriptSpec{
			TargetWords: 12,
		},
		Output: scriptpkg.OutputSpec{
			// Procs the test exercises:
			ExtractEntities:  scriptpkg.ToggleEnabled, // selects clip_search + provider search
			GenerateMetadata: scriptpkg.ToggleEnabled,
			// Opt out of every optional postprocessor; the plan still gains the
			// unconditional clip_bindings / asset_location_reconciliation /
			// persistence stages, but only the registered-and-planned stubs
			// (clip_search, metadata, persistence) execute.
			SaveToDB: false,
		},
	}
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
