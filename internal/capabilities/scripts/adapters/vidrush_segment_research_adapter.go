package adapters

import (
	"context"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// VidRushSegmentResearchAdapter adapts the canonical WebResearchResolver to
// one immutable VidRush segment. It stores research evidence in metadata and
// never changes the narration text.
type VidRushSegmentResearchAdapter struct {
	resolver interface {
		Resolve(context.Context, scriptpkg.SourceSpec, scriptpkg.SourceResolutionContext) (*scriptpkg.ResolvedSource, error)
	}
}

func NewVidRushSegmentResearchAdapter(resolver interface {
	Resolve(context.Context, scriptpkg.SourceSpec, scriptpkg.SourceResolutionContext) (*scriptpkg.ResolvedSource, error)
}) *VidRushSegmentResearchAdapter {
	return &VidRushSegmentResearchAdapter{resolver: resolver}
}

func (a *VidRushSegmentResearchAdapter) ResearchSegment(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult) (*scriptpkg.ResearchReport, error) {
	if a == nil || a.resolver == nil {
		return nil, nil
	}
	text := strings.TrimSpace(segment.Text)
	if text == "" {
		return nil, nil
	}
	source := scriptpkg.SourceSpec{Type: scriptpkg.SourceResearch, Topic: text, Query: text, Search: true}
	resolved, err := a.resolver.Resolve(ctx, source, scriptpkg.SourceResolutionContext{ItemID: segment.SegmentID, Language: planLanguage(plan), Title: planTitle(plan)})
	if err != nil {
		return nil, err
	}
	if resolved == nil {
		return nil, nil
	}
	return resolved.ResearchReport, nil
}

func planLanguage(plan *scriptpkg.ResolvedGenerationPlan) string {
	if plan == nil {
		return ""
	}
	return plan.Language
}

func planTitle(plan *scriptpkg.ResolvedGenerationPlan) string {
	if plan == nil {
		return ""
	}
	return plan.Title
}
