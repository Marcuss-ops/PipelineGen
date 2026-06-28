// Package voiceover — promo workflow moved to workflow/promo (PR 6, June 2026).
// GeneratePromo delegates to the workflow package.
package voiceover

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/workflow/promo"
	domainvo "github.com/Marcuss-ops/PipelineGen/internal/domain/voiceover"
)

func (s *Service) GeneratePromo(ctx context.Context, req *PromoRequest) (*PromoResponse, error) {
	if s.translator == nil {
		return nil, fmt.Errorf("translator not configured")
	}

	// Build the workflow generator on-the-fly.
	// Uses a bridge that calls into Service.Generate (the legacy path).
	// TODO(PR5-restore): replace with s.generateVC.Execute(ctx, cmd) when use case is restored.
	voGen := &voiceoverGenBridge{svc: s}
	gen := promo.NewGenerator(s.translator, voGen, s.log)

	result, err := gen.Generate(ctx, req)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// voiceoverGenBridge adapts *voiceover.Service to promo.VoiceoverGenerator.
type voiceoverGenBridge struct {
	svc *Service
}

func (b *voiceoverGenBridge) Generate(ctx context.Context, cmd domainvo.GenerateVoiceoverCommand) (*domainvo.Result, error) {
	// TODO(PR5-restore): replace with b.svc.generateVC.Execute(ctx, cmd)
	// when the use case files are restored.
	filename := cmd.Filename()
	result, err := b.svc.Generate(ctx, cmd.Text, cmd.Locale, filename)
	if err != nil {
		return nil, err
	}
	return &domainvo.Result{
		OK:          result.OK,
		ID:          cmd.ID(),
		Locale:      cmd.Locale,
		Text:        cmd.Text,
		Voice:       result.Voice,
		Filename:    filename,
		LocalPath:   result.Path,
		DriveLink:   result.DriveLink,
		DriveFileID: result.DriveFileID,
		Status:      "generated",
		Warnings:    []string{},
	}, nil
}

// Ensure bridge satisfies the interface at compile time.
var _ promo.VoiceoverGenerator = (*voiceoverGenBridge)(nil)
