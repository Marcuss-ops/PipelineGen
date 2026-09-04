package generation

import (
	"context"

	imagestyles "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/styles"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"go.uber.org/zap"
)

// GenerationService is the generation composition shell. The leaf package owns
// registry dispatch, style resolution, sync ingest and async job execution.
type GenerationService struct {
	registry *Registry
	styles   imagestyles.StyleResolver
	log      *zap.Logger
	storage  StoragePort
}

func NewGenerationService(registry *Registry, styles imagestyles.StyleResolver, log *zap.Logger, storage StoragePort) *GenerationService {
	if log == nil {
		log = zap.NewNop()
	}
	return &GenerationService{registry: registry, styles: styles, log: log, storage: storage}
}

func (g *GenerationService) GenerateSmartImage(ctx context.Context, subject, topic, style string, prompts, tags []string, width, height int, model string, skipDrive bool) (*detail.ImageAsset, error) {
	return GenerateSync(ctx, g, SyncCommand{
		Subject: subject, Topic: topic, Style: style, Prompts: prompts, Tags: tags,
		Width: width, Height: height, Model: model, SkipDrive: skipDrive,
	})
}

func (g *GenerationService) TriggerPrewarm(ctx context.Context, jobID string, count int) {
	select {
	case <-ctx.Done():
		return
	default:
	}
	if g != nil && g.registry != nil {
		g.registry.TriggerPrewarm(ctx, jobID, count)
	}
	if g != nil && g.log != nil {
		g.log.Info("Google Slides: automation session tab pool prewarmed", zap.String("job_id", jobID), zap.Int("count", count))
	}
}
