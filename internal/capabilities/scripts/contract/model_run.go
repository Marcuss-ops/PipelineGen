package contract

import (
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// EntityExtractionDisabled reports whether the caller explicitly disabled
// run-level entity extraction via output.extract_entities=disabled. A segment
// with extract_entities=enabled may still opt back in independently.
func (req GenerateRequest) EntityExtractionDisabled() bool {
	return req.ExtractEntities == scriptpkg.ToggleDisabled
}

// HasEnabledSegmentEntityExtraction reports whether at least one segment
// explicitly opts back into entity extraction when the run-level toggle is off.
func (req GenerateRequest) HasEnabledSegmentEntityExtraction() bool {
	for _, segment := range req.ScriptParams.Segments {
		if SegmentExtractionExplicitlyEnabled(segment) {
			return true
		}
	}
	return false
}

// NeedsSemanticEnrichment reports whether the run has a real downstream
// consumer for per-scene semantic extraction. Omitted entity extraction is
// intentionally no longer an implicit NLP request: plain text/audio/docs
// runs do not need VidRush, while explicit entities, scene images, enabled
// extraction policy, or an active visual media plan do.
func (req GenerateRequest) NeedsSemanticEnrichment() bool {
	if req.ExtractEntities == scriptpkg.ToggleDisabled && !req.HasEnabledSegmentEntityExtraction() {
		return false
	}
	if req.HasEnabledSegmentEntityExtraction() || req.ExtractEntities == scriptpkg.ToggleEnabled || req.GenerateSceneImages == scriptpkg.ToggleEnabled {
		return true
	}
	if req.MediaPlan.Extraction.EntityImageSurfaceEnabled() ||
		(req.MediaPlan.Extraction.Enabled && !req.MediaPlan.Extraction.HasCategoryOnlyIncludes()) ||
		req.MediaPlan.Extraction.EntityExtractionExplicitlyRequested() {
		return true
	}
	return mediadomain.IsActiveMediaPlanMode(req.MediaPlan.Mode)
}

// DocsRequested reports whether the request still asks for the retired Google
// Docs capability through the canonical (docs.enabled) or legacy
// (docs_enabled) field.
func (req GenerateRequest) DocsRequested() bool {
	return req.Docs.Enabled || req.DocsEnabled
}

// SegmentExtractionExplicitlyEnabled reports whether a payload segment itself
// opts in. It deliberately does not treat the run-level default as a segment
// opt-in; this is used only when the run-level toggle is disabled.
func SegmentExtractionExplicitlyEnabled(segment scriptpkg.ScriptSegment) bool {
	return segment.ExtractEntities == scriptpkg.ToggleEnabled
}
