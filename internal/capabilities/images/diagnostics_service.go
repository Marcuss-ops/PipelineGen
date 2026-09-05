package images

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/ingest"
	imggeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/generation"
	imageingest "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/ingest"
	"go.uber.org/zap"
)

// DiagnosticsService exposes health checks and capability reporting for the
// images subsystem. AI generation reports only the Google Slides-backed
// Chrome/Playwright capability.
type DiagnosticsService struct {
	repo        ImageRepository
	driveReader imageingest.DriveReader
	imageGen    imggeneration.ImageGenerator
	ingestSvc   *ingest.Service
	log         *zap.Logger
}

// Diagnostics returns a truthful health report for the images subsystem.
func (d *DiagnosticsService) Diagnostics() DiagnosticsReport {
	report := DiagnosticsReport{
		OK:               d.repo != nil,
		Services:         []string{"repo", "drive", "google_slides", "chrome_playwright", "ingest"},
		RepoConfigured:   d.repo != nil,
		DriveConfigured:  d.driveReader != nil,
		IngestConfigured: d.ingestSvc != nil,
		ImageGenWired:    d.imageGen != nil,
		Capabilities:     d.AllCapabilities(),
	}

	// The structural-interface fallthrough catches any ImageGenerator
	// implementation that exposes Health() + ActiveCooldownProfiles(),
	// without coupling diagnostics to a concrete infrastructure adapter.
	if cp, ok := d.imageGen.(interface {
		Health() error
		ActiveCooldownProfiles() int
	}); ok {
		report.ImageGenHealthy = cp.Health() == nil
		report.ImageGenCooldownProfiles = cp.ActiveCooldownProfiles()
	}
	return report
}

// CapabilityResolution returns the status of the sole AI generation capability.
func (d *DiagnosticsService) CapabilityResolution(cap Capability) CapabilityStatus {
	switch cap {
	case CapImageGenChrome:
		if d.imageGen == nil {
			return StatusMissingDependency
		}
		return StatusAvailable
	default:
		return StatusNotImplemented
	}
}

// AllCapabilities returns only capabilities that users can actually invoke.
func (d *DiagnosticsService) AllCapabilities() map[Capability]CapabilityStatus {
	return map[Capability]CapabilityStatus{
		CapImageGenChrome: d.CapabilityResolution(CapImageGenChrome),
	}
}

func (d *DiagnosticsService) Log() *zap.Logger { return d.log }

func (d *DiagnosticsService) Repo() ImageRepository { return d.repo }

func (d *DiagnosticsService) SyncAssets() error { return nil }
