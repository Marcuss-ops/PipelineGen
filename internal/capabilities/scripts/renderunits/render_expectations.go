package renderunits

import (
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	kernelscript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ErrRendererNotWired is returned when a render-enabled request reaches the
// localized render fan-out without a configured enqueuer. The absent
// dependency is a composition error, not a transient render failure: retrying
// cannot fix it, and returning nil would hide the missing work until the run's
// final render-set gate.
var ErrRendererNotWired = errors.New("localized renderer is not wired")

// LocalizedRenderRequested reports whether the render spec asks for localized
// render work. A clip run may still be audio-only or timeline-only, in which
// case the fan-out is a no-op rather than a missing dependency.
func LocalizedRenderRequested(spec kernelscript.VideoRenderSpec) bool {
	return spec.Enabled || spec.OverlaysOnly
}

// RecordExpectations records the canonical render-unit promise when the local
// Chronon render lane is active. A render-enabled clip run with at least one
// expected unit fails closed here when the renderer is absent: the alternative
// is paying script/TTS/audio work for a run that can only end in
// INCOMPLETE_RENDER_SET. dispatchSealed disables the promise while the render
// dispatch is intentionally closed.
func RecordExpectations(req contract.GenerateRequest, scenes []contract.Scene, result *contract.GenerateResult, dispatchSealed, rendererWired bool) error {
	if req.Source.Type != kernelscript.SourceClips || !req.Render.Enabled {
		return nil
	}
	result.RenderMetrics = &contract.RenderMetrics{Concurrency: req.Render.RenderConcurrency}
	if dispatchSealed {
		return nil
	}
	expected := ExpectedRenderUnits(req, scenes)
	result.ExpectedRenderCount = expected
	result.RenderMetrics.Expected = expected
	if expected > 0 && !rendererWired {
		return ErrRendererNotWired
	}
	return nil
}

// RenderSetSatisfiesExpectation enforces the exact render-unit contract.
// Additional renders indicate a dispatch/planning mismatch and must not be
// silently certified as a complete run.
func RenderSetSatisfiesExpectation(expected, successful, failed int) bool {
	return successful == expected && failed == 0
}

// DedupeLocalizedRenders keeps the newest receipt per render-unit identity
// (language, scene, clip) while preserving first-seen ordering, which is also
// the order used to assemble the final timeline.
func DedupeLocalizedRenders(renders []contract.LocalizedRenderResult) []contract.LocalizedRenderResult {
	if len(renders) < 2 {
		return renders
	}
	unique := make([]contract.LocalizedRenderResult, 0, len(renders))
	indices := make(map[string]int, len(renders))
	for _, rendered := range renders {
		key := string(rendered.Language) + "\x00" + rendered.SceneID + "\x00" + rendered.ClipID
		if index, ok := indices[key]; ok {
			unique[index] = rendered
			continue
		}
		indices[key] = len(unique)
		unique = append(unique, rendered)
	}
	return unique
}
