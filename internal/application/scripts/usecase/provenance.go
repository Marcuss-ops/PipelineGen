// Package usecase — provenance.go computes and attaches the complete
// generation provenance block to a GenerationResult.
package usecase

import (
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// buildProvenance assembles the GenerationProvenance block from the
// resolved plan, engine output, and mode info. It is called BEFORE
// postprocessing so the document processor can fill DocID/DocLink after
// creating or updating the Google Doc.
func buildProvenance(
	plan scriptpkg.ResolvedGenerationPlan,
	engineResult *EngineResult,
	modeInfo *scriptpkg.GenerationModeInfo,
) *scriptpkg.GenerationProvenance {
	prov := &scriptpkg.GenerationProvenance{
		SourceType:     plan.SourceKind,
		SourceTextHash: hashSourceText(plan),
		Model:          engineResult.Model,
		PromptVersion:  plan.PromptVersion,
		PlannerVersion: plan.EditorPromptVersion,
	}

	if plan.ClipEvidence != nil {
		prov.ClipIDs = append([]string(nil), plan.ClipEvidence.AcceptedClipIDs...)
		if plan.NumClips > 0 && plan.NumClips < len(prov.ClipIDs) {
			prov.ClipIDs = prov.ClipIDs[:plan.NumClips]
		}
	}

	if modeInfo != nil {
		prov.RequestedMode = modeInfo.RequestedMode
		prov.UsedMode = modeInfo.UsedMode
		prov.FallbackUsed = modeInfo.FallbackUsed
	}

	return prov
}

// hashSourceText returns a SHA-256 hex digest of the canonical source
// text (plan source text + clip evidence assembled text).
func hashSourceText(plan scriptpkg.ResolvedGenerationPlan) string {
	parts := []string{strings.TrimSpace(plan.SourceText)}
	if plan.ClipEvidence != nil {
		parts = append(parts, strings.TrimSpace(plan.ClipEvidence.ModelSourceText()))
	}
	h := digest.SHA256Bytes([]byte(strings.Join(parts, "\n")))
	return h
}
