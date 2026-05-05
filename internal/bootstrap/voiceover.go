package bootstrap

import (
	"velox/go-master/internal/api/handlers/voiceover"
	"velox/go-master/internal/module"
	"velox/go-master/pkg/config"

	"go.uber.org/zap"
)

// VoiceoverWiring holds the Voiceover module wiring
type VoiceoverWiring struct {
	Handler       *voiceover.Handler
	SyncHandler   *voiceover.SyncHandler
	Module        module.Module
}

// WireVoiceover creates the Voiceover handler and module
func WireVoiceover(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*VoiceoverWiring, error) {
	var handler *voiceover.Handler
	var syncHandler *voiceover.SyncHandler
	var mod module.Module

	if coreDeps.VoiceoverService != nil {
		handler = voiceover.NewHandler(coreDeps.VoiceoverService)
		syncHandler = voiceover.NewSyncHandler(coreDeps.VoiceoverSync, log)
		mod = module.NewVoiceoverModule(cfg, log, handler, syncHandler)
		log.Info("created Voiceover module")
	}

	return &VoiceoverWiring{
		Handler:     handler,
		SyncHandler: syncHandler,
		Module:      mod,
	}, nil
}
