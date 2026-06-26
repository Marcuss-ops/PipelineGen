// Package scripts — processor_voiceover.go generates voiceover
// audio for the script scenes. Enabled as "voiceover" in the
// plan's Postprocessors list.
//
// NOTE: The real VoiceoverService implementation was deleted in
// commit d61068b3. This processor is a forward-compatible
// placeholder that returns ErrPostprocessFailed when the service
// is not yet wired. When the voiceover pipeline is re-constituted,
// wire the real voiceover.Service here.
package scripts

import (
	"context"
	"fmt"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// VoiceoverProcessor generates voiceover audio for script scenes.
// Currently a real-failure placeholder.
type VoiceoverProcessor struct {
	// voService would be *voiceover.Service when re-constituted.
	voService interface{}
}

// NewVoiceoverProcessor creates a VoiceoverProcessor.
// voService may be nil (placeholder failure mode).
func NewVoiceoverProcessor(voService interface{}) *VoiceoverProcessor {
	return &VoiceoverProcessor{voService: voService}
}

func (p *VoiceoverProcessor) Name() string { return "voiceover" }

func (p *VoiceoverProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, script string) (*PostProcessResult, error) {
	if p.voService == nil {
		return nil, fmt.Errorf("%w: voiceover processor: VoiceoverService not yet re-constituted (placeholder)", scriptpkg.ErrPostprocessFailed)
	}
	if script == "" {
		return &PostProcessResult{}, nil
	}

	_ = ctx
	_ = plan

	// Placeholder: return error until real implementation is restored.
	return nil, fmt.Errorf("%w: voiceover processor: GenerateVoiceover not yet implemented (pending VoiceoverService restoration)", scriptpkg.ErrPostprocessFailed)
}
