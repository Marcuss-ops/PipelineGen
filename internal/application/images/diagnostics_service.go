package images

import (
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"go.uber.org/zap"
)

// DiagnosticsService exposes health checks and capability reporting
// for the images subsystem. It is self-contained: zero cross-boundary
// references to other sub-services.
type DiagnosticsService struct {
	repo         *assets.ImagesRepository
	driveReader   drive.Reader
	imageGen     ImageGenerator
	ingestSvc    *ingest.Service
	log          *zap.Logger
	nvidiaAPIKey string
}

// Diagnostics returns a comprehensive health report for the images subsystem.
func (d *DiagnosticsService) Diagnostics() DiagnosticsReport {
	report := DiagnosticsReport{
		OK:               d.repo != nil,
		Services:         []string{"repo", "drive", "nvidia", "remote_image_gen", "chrome_playwright"},
		RepoConfigured:   d.repo != nil,
		DriveConfigured:  d.driveReader != nil,
		NvidiaConfigured: d.CapabilityResolution(CapImageGenNvidia) == StatusAvailable,
		IngestConfigured: d.ingestSvc != nil,
		ImageGenWired:    d.imageGen != nil,
		Capabilities:     d.AllCapabilities(),
	}

	if cp, ok := d.imageGen.(*ChromeImageProvider); ok {
		report.ImageGenHealthy = cp.Health() == nil
		report.ImageGenCooldownProfiles = cp.ActiveCooldownProfiles()
	}
	return report
}

// CapabilityResolution returns CapabilityStatus for the given Capability.
func (d *DiagnosticsService) CapabilityResolution(cap Capability) CapabilityStatus {
	switch cap {
	case CapImageGenNvidia:
		return StatusNotImplemented
	case CapRemoteImageGen:
		return StatusNotImplemented
	case CapImageGenChrome:
		if d.imageGen == nil {
			return StatusMissingDependency
		}
		return StatusAvailable
	default:
		return StatusNotImplemented
	}
}

// AllCapabilities returns the resolved status for every known capability.
func (d *DiagnosticsService) AllCapabilities() map[Capability]CapabilityStatus {
	return map[Capability]CapabilityStatus{
		CapImageGenNvidia: d.CapabilityResolution(CapImageGenNvidia),
		CapRemoteImageGen: d.CapabilityResolution(CapRemoteImageGen),
		CapImageGenChrome: d.CapabilityResolution(CapImageGenChrome),
	}
}

// Log returns the internal logger.
func (d *DiagnosticsService) Log() *zap.Logger { return d.log }

// Repo returns the underlying images repository.
func (d *DiagnosticsService) Repo() *assets.ImagesRepository { return d.repo }

// SyncAssets is a no-op kept for API compatibility.
func (d *DiagnosticsService) SyncAssets() error { return nil }
