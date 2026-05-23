package app

import (
	"velox/go-master/internal/api/handlers/voiceover"
	"velox/go-master/internal/config"
	voiceoverPkg "velox/go-master/internal/media/voiceover"
	"velox/go-master/internal/module"

	"go.uber.org/zap"
)

// VoiceoverWiring holds the Voiceover module wiring
type VoiceoverWiring struct {
	SyncHandler *voiceover.SyncHandler
	Module      module.Module
	Service     *voiceoverPkg.Service
}

// WireVoiceover creates the Voiceover module
func WireVoiceover(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*VoiceoverWiring, error) {
	var syncHandler *voiceover.SyncHandler
	var mod module.Module

	if coreDeps.VoiceoverService != nil {
		coreDeps.VoiceoverService.RegisterHandler(coreDeps.JobsService)
		syncHandler = voiceover.NewSyncHandler(coreDeps.VoiceoverSync, log)
		// Only /sync route; /generate and /batch are handled by Assets module
		// which supports both sync and async via job queue
		mod = module.NewVoiceoverModule(cfg, log, syncHandler)
		log.Info("created Voiceover module")
	}

	return &VoiceoverWiring{
		SyncHandler: syncHandler,
		Module:      mod,
		Service:     coreDeps.VoiceoverService,
	}, nil
}
