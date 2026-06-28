// Package scripts — postprocessor_registry.go is a re-export shim
// (June 2026, build-fix). The canonical PostProcessor / PostProcessResult /
// PostProcessorRegistry / ProcessorPolicy / SceneVoiceover / SceneImage /
// PipelineResult / ProcessInput types + their constructors and methods
// live in `internal/application/scripts/adapters/postprocessor_registry.go`
// (the `adapters` package).
//
// Historically this file held a parallel implementation of every type
// above. The two implementations drifted (e.g. AlreadyPersisted on
// PostProcessResult was added to the root and removed from adapters),
// which caused the build to fail with `undefined: SceneVoiceover` /
// `undefined: SceneImage` / `undefined: PipelineResult` errors at every
// reference inside this file. Rather than chase the drift one field at
// a time, the entire body is now type aliases + var/const re-exports
// of the canonical adapters symbols — preserving the public API of
// `package scripts` while routing every call through the single source
// of truth.
//
// All methods on the underlying types (Register, Run, Freeze, IsFrozen,
// Len, LookupPolicy, Registered, ValidateRequested, IsEmpty, …) remain
// reachable through the alias types because Go type aliases preserve
// the method set of the underlying type.
package scripts

import (
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	dto "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/dto"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
)

// Type aliases — preserve the public API of `package scripts` while
// routing to the canonical `adapters` package.
type (
	ProcessorPolicy        = adapters.ProcessorPolicy
	PostProcessor          = adapters.PostProcessor
	PostProcessResult      = adapters.PostProcessResult
	ProcessInput           = adapters.ProcessInput
	PostProcessorRegistry  = adapters.PostProcessorRegistry
	PipelineResult         = adapters.PipelineResult
	SceneImage             = adapters.SceneImage
	SceneVoiceover         = adapters.SceneVoiceover
)

// PostProcessArtifact lives in the dto package (compat_types.go)
// as the historical accumulator name used by tests and the image /
// voiceover processors. The root `scripts` package re-exports it
// here so callers can use `scripts.PostProcessArtifact` without
// importing the dto subpackage.
type PostProcessArtifact = dto.PostProcessArtifact

// Const re-exports.
const (
	ProcessorRequired   = adapters.ProcessorRequired
	ProcessorBestEffort = adapters.ProcessorBestEffort
)

// Var re-exports (package-level functions live in adapters).
var (
	DefaultPolicyFor              = adapters.DefaultPolicyFor
	NewPostProcessorRegistry      = adapters.NewPostProcessorRegistry
	NewClipBindingsProcessor      = adapters.NewClipBindingsProcessor
	NewClipSourceBuilderForTest   = usecase.NewClipSourceBuilderForTest
	BuildClipSpecSceneDocumentHTML = adapters.BuildClipSpecSceneDocumentHTML
	// BuildClipEvidence re-exports the canonical clip-evidence
	// builder from the usecase package so the test
	// `clip_evidence_builder_test.go` (also `package scripts`)
	// can call it as a bare function name. No import cycle
	// because usecase does not import the `scripts` root
	// package — only `adapters` and `dto` subpackages.
	BuildClipEvidence = usecase.BuildClipEvidence
)
