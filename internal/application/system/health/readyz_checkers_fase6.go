// Package health — readyz_checkers_fase6.go (sister file D).
//
// PR-SPLIT-READYZ-CHECKERS closure (2026-08-08): canonical owner
// of Step 4 preflight probes + FASE 6 advanced capability probes.
//
// Each probe is a complete "trio" (interface + concrete + NewX)
// stored under its own godlike/06 SSOT owner-per-fact boundary.
// That is, this single file owns 9 trios:
//
//	Step 4 preflight (July 2026):
//	  - DriveCredentialsChecker + FileCredentialsChecker +
//	    NewDriveCredentialsChecker
//	  - DriveFolderChecker + publisherFolderChecker +
//	    NewDriveFolderChecker
//	  - PublisherChecker + wiredPublisherChecker +
//	    NewPublisherChecker
//	  - DestinationClipChecker + registryDestinationClipChecker +
//	    NewDestinationClipChecker
//
//	FASE 6 advanced capability (July 2026):
//	  - TempWritableChecker + defaultTempWritableChecker
//	    (constructed inline in WithTempPath on the orchestrator)
//	  - TTSChecker (concrete owned by internal/platform/process)
//	  - DriveRootChecker + driveRootAdapter + NewDriveRootChecker
//	  - OllamaChecker + ollamaHealthAdapter + NewOllamaChecker
//	  - OutboxChecker + outboxPoolProbe + NewOutboxChecker
//
// The orchestrator (A) attaches instances via With* setters and
// drives each check via its run*Check runner (both stay in (A)
// per the user-spec layout — all wiring lives in (A); type
// structure stays here).
package health

import (
	"context"
	"fmt"
	"os"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
)

// ── Step 4 Drive preflight trios ──────────────────────────────────────

// DriveCredentialsChecker verifies that Drive OAuth token and
// credentials files exist on disk. nil-safe: nil checker =
// ok+applicable=false (handled by runDriveCredentialsCheck
// nil-guard in the orchestrator).
type DriveCredentialsChecker interface {
	CredentialsPresent(ctx context.Context) (missing []string, err error)
}

// FileCredentialsChecker probes token.json + credentials.json
// on disk. Both paths are optional (empty → skip that probe).
type FileCredentialsChecker struct {
	TokenPath       string
	CredentialsPath string
}

// CredentialsPresent returns any missing credential file path
// enumerated with its os.Stat error. nil-receiver safe
// (returns nil, nil = no missing, no error).
func (c *FileCredentialsChecker) CredentialsPresent(_ context.Context) ([]string, error) {
	if c == nil {
		return nil, nil
	}
	var missing []string
	if c.TokenPath != "" {
		if _, err := os.Stat(c.TokenPath); err != nil {
			missing = append(missing, "token.json ("+c.TokenPath+"): "+err.Error())
		}
	}
	if c.CredentialsPath != "" {
		if _, err := os.Stat(c.CredentialsPath); err != nil {
			missing = append(missing, "credentials.json ("+c.CredentialsPath+"): "+err.Error())
		}
	}
	return missing, nil
}

// NewDriveCredentialsChecker builds a file-based Drive
// credentials checker. Pass empty strings to skip either probe.
func NewDriveCredentialsChecker(tokenPath, credentialsPath string) DriveCredentialsChecker {
	return &FileCredentialsChecker{TokenPath: tokenPath, CredentialsPath: credentialsPath}
}

// DriveFolderChecker verifies that the configured Drive folder is
// reachable (can list or stat). nil-safe.
type DriveFolderChecker interface {
	CheckFolder(ctx context.Context, folderID string) error
}

// publisherFolderChecker wraps delivery.Publisher for folder
// access verification via the publisher's ResolveFolder seam.
type publisherFolderChecker struct {
	pub delivery.Publisher
}

// CheckFolder verifies the folder exists by attempting to
// resolve it. Returns errCanaryPublisherNotWired if pub is nil
// (defined in sister file (C); same sentinel — the publisher
// unwired condition is the canonical failure mode for any
// publisher-rooted probe).
func (c *publisherFolderChecker) CheckFolder(ctx context.Context, folderID string) error {
	if c.pub == nil {
		return errCanaryPublisherNotWired
	}
	_, err := c.pub.ResolveFolder(ctx, delivery.PublishRequest{
		Destination:    delivery.DestinationYouTubeClip,
		ParentFolderID: folderID,
	})
	return err
}

// NewDriveFolderChecker creates a folder checker backed by
// delivery.Publisher (the same publisher used by the canary
// probe — the publisher unwired sentinel is shared across both
// probes for diagnostic consistency).
func NewDriveFolderChecker(pub delivery.Publisher) DriveFolderChecker {
	return &publisherFolderChecker{pub: pub}
}

// PublisherChecker verifies that the canonical
// delivery.Publisher is wired (non-nil). nil-safe.
type PublisherChecker interface {
	IsWired() bool
}

// wiredPublisherChecker checks delivery.Publisher is non-nil.
type wiredPublisherChecker struct {
	pub delivery.Publisher
}

// IsWired returns true if the Publisher field is non-nil.
// nil-receiver safe (returns false).
func (c *wiredPublisherChecker) IsWired() bool {
	return c != nil && c.pub != nil
}

// NewPublisherChecker creates a Publisher wiring checker. The
// preflight fails if pub is nil (composition must wire
// DriveBundle.Publisher).
func NewPublisherChecker(pub delivery.Publisher) PublisherChecker {
	return &wiredPublisherChecker{pub: pub}
}

// DestinationClipChecker verifies that DestinationYouTubeClip is
// registered in the DestinationRegistry. nil-safe.
type DestinationClipChecker interface {
	ClipDestinationRegistered() bool
}

// registryDestinationClipChecker wraps DestinationRegistry.Has
// via a closure adapter. The closure is passed by the
// composition root (build_bundles_core.go wires it to
// registry.Has(key delivery.DestinationKey) bool).
type registryDestinationClipChecker struct {
	has func(key delivery.DestinationKey) bool
}

// ClipDestinationRegistered returns true if DestinationYouTubeClip
// is in the registry. nil-receiver safe (returns false).
func (c *registryDestinationClipChecker) ClipDestinationRegistered() bool {
	if c == nil || c.has == nil {
		return false
	}
	return c.has(delivery.DestinationYouTubeClip)
}

// NewDestinationClipChecker builds a checker from a Has closure
// on the registry. The composition root provides a closure
// adapter that wraps *delivery.DestinationRegistry.Has.
func NewDestinationClipChecker(has func(key delivery.DestinationKey) bool) DestinationClipChecker {
	return &registryDestinationClipChecker{has: has}
}

// ── FASE 6 advanced capability trios ────────────────────────────────

// TempWritableChecker verifies a directory path is writable by
// creating and removing a probe file. nil-safe: nil checker
// reports applicable=false.
type TempWritableChecker interface {
	CheckTempWritable(path string) error
}

// defaultTempWritableChecker verifies writability via
// os.CreateTemp. Constructed inline by WithTempPath (the
// orchestrator owns the construction call, but the type lives
// here).
type defaultTempWritableChecker struct{}

// CheckTempWritable creates + removes a probe file via
// os.CreateTemp. Clean-up is best-effort (errors from os.Remove
// are deliberately ignored — the probe file name is unguessable
// and short-lived, so a clean-up failure is purely cosmetic).
func (c *defaultTempWritableChecker) CheckTempWritable(path string) error {
	f, err := os.CreateTemp(path, ".readyz-temp-probe-*")
	if err != nil {
		return err
	}
	f.Close()
	return os.Remove(f.Name())
}

// TTSChecker is the application port for the Python TTS bridge probe.
// The subprocess-backed concrete lives in internal/platform/process.
type TTSChecker interface {
	CheckTTS(ctx context.Context) error
}

// DriveRootChecker verifies the Drive root folder is accessible
// (folder exists and is reachable via the Drive API). Distinct
// from the Drive credential check — credentials may be valid but
// the configured root folder may have been deleted or its
// permissions revoked. nil-safe.
type DriveRootChecker interface {
	CheckDriveRoot(ctx context.Context, folderID string) error
}

// driveRootAdapter satisfies DriveRootChecker via a reachability
// closure. The composition root adapts drive.Reader.ListFiles to
// an error-only probe (discard file list, return only the error).
type driveRootAdapter struct {
	probe func(ctx context.Context, folderID string) error
}

// CheckDriveRoot runs the closure. Returns a typed error if the
// probe is nil (composition root misconfiguration signal).
func (a *driveRootAdapter) CheckDriveRoot(ctx context.Context, folderID string) error {
	if a.probe == nil {
		return fmt.Errorf("Drive reader not wired")
	}
	return a.probe(ctx, folderID)
}

// NewDriveRootChecker creates a Drive root folder probe from a
// reachability closure. The closure should call the Drive API
// (e.g. ListFiles) and return nil on success.
func NewDriveRootChecker(probe func(ctx context.Context, folderID string) error) DriveRootChecker {
	return &driveRootAdapter{probe: probe}
}

// OllamaChecker verifies the Ollama inference server is
// reachable and returns a valid response. nil-safe.
type OllamaChecker interface {
	CheckOllama(ctx context.Context) error
}

// ollamaHealthAdapter wraps an Ollama health-check function for
// the ReadyChecker probe interface. The closure pattern keeps
// the health package decoupled from internal/ml/ollama.
type ollamaHealthAdapter struct {
	check func(ctx context.Context) bool
}

// CheckOllama runs the closure. Two typed errors surface:
// fmt.Errorf("Ollama health check not wired") when closure is
// nil, fmt.Errorf("Ollama health check returned false") when the
// closure signals down.
func (a *ollamaHealthAdapter) CheckOllama(ctx context.Context) error {
	if a.check == nil {
		return fmt.Errorf("Ollama health check not wired")
	}
	if !a.check(ctx) {
		return fmt.Errorf("Ollama health check returned false")
	}
	return nil
}

// NewOllamaChecker creates an Ollama reachability probe from a
// CheckHealth-style closure. The composition root wraps the
// concrete Ollama client health-check method in a closure
// adapter that matches this signature.
func NewOllamaChecker(check func(ctx context.Context) bool) OllamaChecker {
	return &ollamaHealthAdapter{check: check}
}

// OutboxChecker verifies the outbox worker pool is running and
// processing events (not just that the outbox table exists in
// DB). nil-safe.
type OutboxChecker interface {
	CheckOutboxWorker(ctx context.Context) error
}

// outboxPoolProbe wraps an outbox-pool liveness probe for the
// ReadyChecker interface.
type outboxPoolProbe struct {
	probe func(ctx context.Context) error
}

// CheckOutboxWorker runs the closure.
func (p *outboxPoolProbe) CheckOutboxWorker(ctx context.Context) error {
	if p.probe == nil {
		return fmt.Errorf("outbox pool probe not wired")
	}
	return p.probe(ctx)
}

// NewOutboxChecker creates an outbox worker liveness probe.
// The composition root passes a closure that checks whether the
// outboxevents.Pool is running (e.g. via an in-memory flag).
func NewOutboxChecker(probe func(ctx context.Context) error) OutboxChecker {
	return &outboxPoolProbe{probe: probe}
}
