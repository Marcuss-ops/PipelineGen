// Package images (application/images) — service_diagnostics.go holds
// the diagnostics methods and the DiagnosticsReport type. Per
// PR-IMG-SPLIT-4 (July 2026), diagnostics is its own capability
// (operational/meta — neither generated nor retrieved).
package images

import (
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets/imagesrepo"
	"go.uber.org/zap"
)

// DiagnosticsReport is the JSON-serializable wiring report for the
// images subsystem. Surfaces repo/Drive/ingest/gen configuration
// and per-capability health status.
type DiagnosticsReport struct {
	OK                       bool                            `json:"ok"`
	Services                 []string                        `json:"services"`
	RepoConfigured           bool                            `json:"repo_configured"`
	DriveConfigured          bool                            `json:"drive_configured"`
	IngestConfigured         bool                            `json:"ingest_configured"`
	WikidataWorks            bool                            `json:"wikidata_works"`
	ImageGenWired            bool                            `json:"image_gen_wired"`
	ImageGenHealthy          bool                            `json:"image_gen_healthy"`
	ImageGenCooldownProfiles int                             `json:"image_gen_cooldown_profiles"`
	Capabilities             map[Capability]CapabilityStatus `json:"capabilities"`
}

// Diagnostics returns the full DiagnosticsReport from the Diag sub-service.
func (s *Service) Diagnostics() DiagnosticsReport { return s.Diag.Diagnostics() }

// CapabilityResolution returns the status of a single named capability.
func (s *Service) CapabilityResolution(cap Capability) CapabilityStatus {
	if s == nil || s.Diag == nil {
		return StatusNotImplemented
	}
	return s.Diag.CapabilityResolution(cap)
}

// AllCapabilities returns the status map of all registered capabilities.
func (s *Service) AllCapabilities() map[Capability]CapabilityStatus {
	if s == nil || s.Diag == nil {
		return nil
	}
	return s.Diag.AllCapabilities()
}

// Log returns the held zap.Logger (from the Diag sub-service).
func (s *Service) Log() *zap.Logger { return s.Diag.Log() }

// Repo returns the held ImagesRepository (from the Diag sub-service).
func (s *Service) Repo() *imagesrepo.ImagesRepository { return s.Diag.Repo() }

// SyncAssets triggers a local filesystem asset sync via the Diag sub-service.
func (s *Service) SyncAssets() error { return s.Diag.SyncAssets() }

// StopChromeProvider shuts down the persistent Chrome worker subprocess
// (slide_worker.py) if it is wired. Nil-safe and idempotent — safe to
// call even when the image generator is nil, not a ChromeImageProvider,
// or already stopped.
//
// VO-DECOMPOSITION P0 #1 CUTOVER follow-up (July 2026): mirrors the TTS
// worker Stop() pattern in DomainBundle.AudioProcessor.Stop(). Wired
// into shutdown.go::buildCleanup alongside the TTS worker.
func (s *Service) StopChromeProvider() error {
	if s == nil || s.Diag == nil || s.Diag.imageGen == nil {
		return nil
	}
	type chromeStopper interface{ Stop() error }
	cp, ok := s.Diag.imageGen.(chromeStopper)
	if !ok || cp == nil {
		return nil
	}
	return cp.Stop()
}
