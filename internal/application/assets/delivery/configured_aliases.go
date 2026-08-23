package delivery

import (
	capdelivery "github.com/Marcuss-ops/PipelineGen/internal/capabilities/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	platformdelivery "github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
)

type PathBuilder = platformdelivery.PathBuilder
type DestinationPolicy = platformdelivery.DestinationPolicy
type DestinationRegistry = platformdelivery.DestinationRegistry

type StartupValidationReport = platformdelivery.StartupValidationReport
type DestinationValidationResult = platformdelivery.DestinationValidationResult
type StartupDriveRootsValidator = platformdelivery.StartupDriveRootsValidator
type StartupRootsProbe = platformdelivery.StartupRootsProbe
type DriveRootsValidator = platformdelivery.DriveRootsValidator
type DriveValidatorMetrics = platformdelivery.DriveValidatorMetrics

var (
	ErrDriveStartupValidationFailed    = platformdelivery.ErrDriveStartupValidationFailed
	ErrMissingStartupValidatorRegistry = platformdelivery.ErrMissingStartupValidatorRegistry
	ErrMissingStartupValidatorFolders  = platformdelivery.ErrMissingStartupValidatorFolders
)

func NewDestinationRegistry(cfg *config.Config) *DestinationRegistry {
	return platformdelivery.NewDestinationRegistry(cfg)
}

func NewDriveRootsValidator(registry *DestinationRegistry, folders StartupRootsProbe, log *zap.Logger, metrics *DriveValidatorMetrics) (*DriveRootsValidator, error) {
	return platformdelivery.NewDriveRootsValidator(registry, folders, log, metrics)
}

func NewDriveValidatorMetrics() *DriveValidatorMetrics {
	return &DriveValidatorMetrics{
		Probes:    observability.DriveRootsValidatorProbesTotal,
		Duration:  observability.DriveRootsValidatorProbeDuration,
		LastRunTS: observability.DriveRootsValidatorLastRunTimestamp,
		LastRunOK: observability.DriveRootsValidatorLastRunSucceeded,
	}
}

// NewDriveValidatorMetricsFromCollectors builds the platform metrics
// contract from caller-owned collectors, primarily for composition tests.
func NewDriveValidatorMetricsFromCollectors(probes *prometheus.CounterVec, duration *prometheus.HistogramVec, lastRunTS, lastRunOK prometheus.Gauge) *DriveValidatorMetrics {
	return &DriveValidatorMetrics{Probes: probes, Duration: duration, LastRunTS: lastRunTS, LastRunOK: lastRunOK}
}

var _ capdelivery.Publisher = (Publisher)(nil)

var ErrYouTubeAssetPathMissingField = platformdelivery.ErrYouTubeAssetPathMissingField

func YouTubeAssetPath(req PublishRequest) ([]string, error) {
	return platformdelivery.YouTubeAssetPath(req)
}

func YouTubeClipPath(req PublishRequest) ([]string, error) {
	return platformdelivery.YouTubeClipPath(req)
}

func ArtlistPath(req PublishRequest) ([]string, error) {
	return platformdelivery.ArtlistPath(req)
}

func StockPath(req PublishRequest) ([]string, error) {
	return platformdelivery.StockPath(req)
}

func ImagePath(req PublishRequest) ([]string, error) {
	return platformdelivery.ImagePath(req)
}

func VoiceoverPath(req PublishRequest) ([]string, error) {
	return platformdelivery.VoiceoverPath(req)
}

func BookPath(req PublishRequest) ([]string, error) {
	return platformdelivery.BookPath(req)
}

func ScriptPath(req PublishRequest) ([]string, error) {
	return platformdelivery.ScriptPath(req)
}

func SoundEffectPath(req PublishRequest) ([]string, error) {
	return platformdelivery.SoundEffectPath(req)
}

func DocumentPath(req PublishRequest) ([]string, error) {
	return platformdelivery.DocumentPath(req)
}

func AdminPath(req PublishRequest) ([]string, error) {
	return platformdelivery.AdminPath(req)
}

func ClipMetadataPath(req PublishRequest) ([]string, error) {
	return platformdelivery.ClipMetadataPath(req)
}

func RenderedClipPath(req PublishRequest) ([]string, error) {
	return platformdelivery.RenderedClipPath(req)
}
