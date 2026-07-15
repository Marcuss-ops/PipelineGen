package app

import (
	"fmt"

	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	voiceoverjobs "github.com/Marcuss-ops/PipelineGen/internal/application/voiceover/jobs"
	domainvoiceover "github.com/Marcuss-ops/PipelineGen/internal/domain/voiceover"
)

// wireVoiceoverJobBindings registers the parent and child voiceover handlers.
func wireVoiceoverJobBindings(domains *DomainBundle, jobs *JobsBundle, log *zap.Logger) error {
	if domains.VoiceoverGenerateHandler != nil && jobs.Service != nil {
		if err := domains.VoiceoverGenerateHandler.Register(jobs.Service); err != nil {
			return fmt.Errorf("voiceover.generate handler wiring (Catena A P0): %w", err)
		}
		log.Info("voiceover.generate handler registered (Catena A P0 wiring complete)")
	} else {
		log.Warn("voiceover.generate handler NOT registered (typed-port chain incomplete — Drive / destResolver / outbox / lifecycle / repo / audio / db must all be wired)",
			zap.Bool("generate_handler_built", domains.VoiceoverGenerateHandler != nil),
			zap.Bool("jobs_service_available", jobs.Service != nil))
	}

	if jobs.Service != nil && domains.VoiceoverProcessItem != nil {
		fanout := voiceoverjobs.NewFanoutVoiceoversUseCase(voiceoverjobs.FanoutDeps{
			Enqueuer: jobs.Service,
			Logger:   log,
		})
		parentHandler := voiceoverjobs.NewGenerateJobHandler(fanout, log)
		if !jobs.Service.HasHandler(domainvoiceover.TypeGenerate) {
			if err := parentHandler.Register(jobs.Service); err != nil {
				return fmt.Errorf("voiceover.generate parent handler Register (BLOC5.3 commit-2): %w", err)
			}
		} else {
			log.Info("voiceover.generate handler already bound (Catena A P0 wiring succeeded) — preserving dispatcher binding; BLOC5.3 fanout-bound handler canonicals the domains.VoiceoverGenerateHandler field reference for downstream state-tracking",
				zap.String("job_type", domainvoiceover.TypeGenerate))
		}
		domains.VoiceoverGenerateHandler = parentHandler

		childHandler := voiceoverjobs.NewGenerateItemJobHandler(domains.VoiceoverProcessItem, log)
		if err := childHandler.Register(jobs.Service); err != nil {
			return fmt.Errorf("voiceover.generate_item child handler Register (BLOC5.3 commit-2): %w", err)
		}
		domains.VoiceoverGenerateItemHandler = childHandler
		log.Info("BLOC5.3 commit-2 voiceover handlers wired: parent voiceover.generate + child voiceover.generate_item")
	}
	return nil
}

func appendVoiceoverCriticalValidators(domains *DomainBundle, jobs *JobsBundle, validators *[]CriticalHandler) {
	if jobs.Service != nil {
		vh := domains.VoiceoverGenerateHandler
		if vh != nil {
			*validators = append(*validators, CriticalHandler{
				Name: "voiceover.generate",
				Bind: func(svc *appjobs.Service) error {
					if svc.HasHandler(domainvoiceover.TypeGenerate) {
						return nil
					}
					return vh.Register(svc)
				},
			})
		}
	}
	if gih := domains.VoiceoverGenerateItemHandler; gih != nil && jobs.Service != nil {
		*validators = append(*validators, CriticalHandler{
			Name: "voiceover.generate_item",
			Bind: func(svc *appjobs.Service) error {
				return gih.Register(svc)
			},
		})
	}
}
