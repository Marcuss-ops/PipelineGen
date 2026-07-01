// Package voiceover — promo workflow moved to workflow/promo (PR 6, June 2026).
// GeneratePromo delegates to the workflow package.
//
// (June 2026 cutover): voiceoverGenBridge routes through legacy
// Service.GenerateWithDestination. The previous BLOC5.3 commit-1 cut
// that delegated to ProcessVoiceoverItemUseCase was reverted because
// the canonical per-item pipeline was never committed in this branch.
// The VoiceoverItemExecutor interface in ports.go is retained for the
// BLOC5.4 follow-up that will land the concrete pipeline.
package voiceover

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/workflow/promo"
	domainvo "github.com/Marcuss-ops/PipelineGen/internal/domain/voiceover"
	"go.uber.org/zap"
)

func (s *Service) GeneratePromo(ctx context.Context, req *PromoRequest) (*PromoResponse, error) {
	if s.translator == nil {
		return nil, fmt.Errorf("translator not configured")
	}

	// (June 2026 cutover): bridge routes to legacy Service.GenerateWithDestination
	// per-language. The VoiceoverItemExecutor port path is forward-deferred to BLOC5.4.
	voGen := &voiceoverGenBridge{
		service: s,
		log:     s.log,
	}
	gen := promo.NewGenerator(s.translator, voGen, s.log)

	result, err := gen.Generate(ctx, req)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// voiceoverGenBridge adapts *Service to the promo.VoiceoverGenerator narrow
// port. (June 2026 cutover): legacy service-route adapter; the canonical
// per-item pipeline (BLOC5.4 follow-up) will replace this with a typed
// port adapter once *ProcessVoiceoverItemUseCase lands.
type voiceoverGenBridge struct {
	service *Service
	log     *zap.Logger
}

func (b *voiceoverGenBridge) Generate(ctx context.Context, cmd domainvo.GenerateVoiceoverCommand) (*domainvo.Result, error) {
	if b == nil || b.service == nil {
		return nil, fmt.Errorf("voiceoverGenBridge: Service not wired")
	}
	normalized := cmd.Normalize()
	if err := normalized.Validate(); err != nil {
		return nil, fmt.Errorf("voiceoverGenBridge: validate command: %w", err)
	}

	// Map domain command to the legacy positional API: text + locale + filename + destination.
	// ID + FileHash + Status are surfaced where the canonical pipeline would have supplied them;
	// the legacy path only returns DriveLink + DriveFileID on success.
	destReq := &DestinationRequest{
		Kind:     "explicit",
		FolderID: normalized.Destination.FolderID,
	}
	res, err := b.service.GenerateWithDestination(ctx, normalized.Text, normalized.Locale, normalized.Filename(), destReq)
	if err != nil {
		// Surface the typed failure code as a Result envelope (OK=false +
		// Status="failed" + Warnings carrying the failure message). The
		// promo generator (workflow/promo) reads Result.DriveLink, so
		// leaving it empty here propagates to the per-language breakdown
		// in the response body — operators can grep the Warnings.
		return &domainvo.Result{
			OK: false,
			VoiceoverSynthesisResult: domainvo.VoiceoverSynthesisResult{
				Locale:   normalized.Locale,
				Text:     normalized.Text,
				Filename: normalized.Filename(),
			},
			Status:   string(StatusFailed),
			Warnings: []string{fmt.Sprintf("voiceover legacy generation failed: %s", err)},
		}, nil
	}
	if b.log != nil {
		b.log.Info("voiceoverGenBridge: legacy generation succeeded",
			zap.String("language", normalized.Locale),
			zap.String("drive_link", res.DriveLink))
	}
	return &domainvo.Result{
		OK: true,
		VoiceoverSynthesisResult: domainvo.VoiceoverSynthesisResult{
			Locale:    normalized.Locale,
			Text:      normalized.Text,
			Voice:     res.Voice,
			Filename:  normalized.Filename(),
			LocalPath: res.Path,
		},
		DriveLink:   res.DriveLink,
		DriveFileID: res.DriveFileID,
		Status:      string(StatusCompleted),
		Warnings:    []string{},
	}, nil
}

// Ensure bridge satisfies the interface at compile time.
var _ promo.VoiceoverGenerator = (*voiceoverGenBridge)(nil)
