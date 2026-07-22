package images

import (
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets/imagesrepo"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"go.uber.org/zap"
)

// DiagnosticsService exposes health checks and capability reporting for the
// images subsystem. AI generation reports only the Google Slides-backed
// Chrome/Playwright capability.
type DiagnosticsService struct {
	repo        *imagesrepo.ImagesRepository
	driveReader drive.Reader
	imageGen    ImageGenerator
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

	// PR-IMAGES-CHROME-RETIRED (July 2026): the explicit
	// *ChromeImageProvider type-assert branch is REMOVED. The
	// structural-interface fallthrough below catches any ImageGenerator
	// implementation that exposes Health() + ActiveCooldownProfiles()
	// (ChromeImageProvider satisfies the structural shape, so its
	// presence continues to surface diagnostics correctly; the
	// removed branch was a duplicate code path, not additional state).
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

func (d *DiagnosticsService) Repo() *imagesrepo.ImagesRepository { return d.repo }

func (d *DiagnosticsService) SyncAssets() error { return nil }
