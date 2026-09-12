// Package usecase — engine_builder_test.go
//
// buildTestEngine builds a generation Engine wired to the shared
// testsupport Ollama fake and a nil memory gate, with lenient segment
// tolerances so unit tests are not fighting the production QA policy.
//
// It lives in this package (not in testsupport) because it needs
// *gencore.Engine: if testsupport imported gencore, gencore's own tests
// could not import testsupport (import cycle not allowed in test).
//
// A nil memory gate is safe only for tests that exercise NON-memory paths
// (UseMemory=false or ForceRefresh=true); tests asserting memory-path
// behaviour live in gencore where the narrow memory-gate types are visible.
package usecase

import (
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase/gencore"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase/testsupport"
)

func buildTestEngine(gen *testsupport.FakeOllamaGen) *gencore.Engine {
	e := gencore.NewEngine(gen, nil, zap.NewNop())
	e.ConfigureSegmentValidation(50, 50, 0)
	return e
}
