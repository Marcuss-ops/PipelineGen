package youtube

import (
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// RuntimeConfigFromConfig is the single config projection for the YouTube
// application runtime. The application layer receives only its typed DTO and
// does not depend on platform configuration.
func RuntimeConfigFromConfig(cfg *config.Config) youtubetypes.RuntimeConfig {
	if cfg == nil {
		return youtubetypes.RuntimeConfig{}
	}
	// Config owns concurrency values; preserve explicit operator settings.
	return youtubetypes.RuntimeConfig{
		MaxConcurrentVideoExtracts:    cfg.Concurrency.MaxConcurrentVideoExtracts,
		MaxConcurrentOllamaCalls:      cfg.Concurrency.MaxConcurrentOllamaCalls,
		YouTubeExtractTimeout:         cfg.Jobs.YouTubeExtractTimeout,
		SectionDownloadTimeoutSeconds: cfg.Jobs.YoutubeSectionDownloadTimeoutSeconds,
		DataDir:                       cfg.Storage.DataDir,
		YtdlpPath:                     cfg.External.ResolvedYtdlpPath(),
		ClipsFolderID:                 cfg.Drive.ClipsFolder(),
		YouTubeSubtitlesFolderID:      cfg.Drive.YouTubeSubtitlesFolder(),
		OllamaModel:                   cfg.External.OllamaModel,
		OllamaMetadataModel:           cfg.External.OllamaMetadataModel,
		YouTubeCookiesPath:            cfg.External.ResolveYouTubeCookiesPath(),
		YouTubeJSRuntimePath:          cfg.External.YouTubeJSRuntimePath,
		YouTubeEnabled:                cfg.Features.YouTubeEnabled,
	}
}
