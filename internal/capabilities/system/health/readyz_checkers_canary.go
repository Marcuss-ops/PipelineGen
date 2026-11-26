// Package health — readyz_checkers_canary.go (sister file C).
//
// PR-SPLIT-READYZ-CHECKERS closure (2026-08-08): canonical owner
// of two readiness probes: (i) a READ-ONLY Drive canary probe,
// (ii) job-handler presence via a HasHandler closure. Each
// capability is a complete "trio": interface + concrete + NewX
// constructor — all stored under their own godlike/06 SSOT
// owner-per-fact boundary.
//
// LOCALJOBS-01 (2026-11-26): GET /ready must never mutate external
// state. The readiness canary used to call delivery.Publisher.Publish,
// which created readyz-canary.txt in the configured Drive folder on
// every probe. The probe now only lists the target folder and checks
// that it is not trashed. Writing the canary file is owned exclusively
// by POST /canary-upload (internal/capabilities/admin).
//
// Type inventory (all private except NewX + RequiredHandlers):
//   - DriveCanaryPort (interface, public)
//   - readOnlyDriveProbe (concrete, private)
//   - canaryErr (private typed-error)
//   - errCanaryReaderNotWired (sentinel)
//   - NewReadOnlyDriveCanary (public ctor)
//   - NewReadOnlyDriveFolderChecker (public ctor)
//   - HandlerRegChecker (interface, public)
//   - HandlerPresenceChecker (concrete, public struct)
//   - RequiredHandlers (public var, the canonical critical-handler list)
//   - NewHandlerPresenceChecker (public ctor)
package system

import (
	"context"
	"fmt"
)

// DriveCanaryPort performs a READ-ONLY Drive canary probe and returns
// nil when the target folder is reachable and not trashed. It is
// deliberately not backed by delivery.Publisher: a health probe may
// not create files. nil-safe per the ReadyChecker aggregation contract
// (orchestrator-runner nil-guards before invocation).
type DriveCanaryPort interface {
	CheckCanaryReadOnly(ctx context.Context, folderID string) error
}

// readOnlyDriveProbe is the single read-only implementation shared by
// the drive_canary and drive_folder readiness checks. reach must list
// or stat the folder through the Drive Reader; notTrashed is optional
// but recommended because a trashed folder still lists its children.
type readOnlyDriveProbe struct {
	reach      func(ctx context.Context, folderID string) error
	notTrashed func(ctx context.Context, folderID string) (bool, error)
}

// check runs the read-only probes in fail-closed order: trashed state
// first (when wired), then reachability. It never writes.
func (p *readOnlyDriveProbe) check(ctx context.Context, folderID string) error {
	if p == nil || p.reach == nil {
		return errCanaryReaderNotWired
	}
	if p.notTrashed != nil {
		ok, err := p.notTrashed(ctx, folderID)
		if err != nil {
			return fmt.Errorf("drive readiness trashed-state probe: %w", err)
		}
		if !ok {
			return fmt.Errorf("drive readiness folder %s is TRASHED: published artifacts would be invisible in Drive", folderID)
		}
	}
	return p.reach(ctx, folderID)
}

func (p *readOnlyDriveProbe) CheckCanaryReadOnly(ctx context.Context, folderID string) error {
	return p.check(ctx, folderID)
}

func (p *readOnlyDriveProbe) CheckFolder(ctx context.Context, folderID string) error {
	return p.check(ctx, folderID)
}

// errCanaryReaderNotWired is the canonical sentinel for an unwired
// read-only Drive probe (composition root did not wire drive.Reader).
var errCanaryReaderNotWired = &canaryErr{msg: "drive reader not wired"}

// canaryErr is the typed-error carrier for the canary sentinel.
// Implements error; field is unexported so callers must use the
// sense-bounded sentinel.
type canaryErr struct{ msg string }

func (e *canaryErr) Error() string { return e.msg }

// NewReadOnlyDriveCanary creates a read-only Drive canary probe from a
// reachability closure (normally drive.Reader.ListFiles) and an optional
// trashed-state closure (drive.Reader.FileIsNotTrashed). It returns a
// nil-safe DriveCanaryPort that reports errCanaryReaderNotWired when
// reach is nil. There is no write seam in this constructor.
func NewReadOnlyDriveCanary(
	reach func(ctx context.Context, folderID string) error,
	notTrashed func(ctx context.Context, folderID string) (bool, error),
) DriveCanaryPort {
	return &readOnlyDriveProbe{reach: reach, notTrashed: notTrashed}
}

// NewReadOnlyDriveFolderChecker creates the folder-access readiness
// probe with the same read-only semantics as NewReadOnlyDriveCanary.
// It exists separately so each readiness fact keeps its own port.
func NewReadOnlyDriveFolderChecker(
	reach func(ctx context.Context, folderID string) error,
	notTrashed func(ctx context.Context, folderID string) (bool, error),
) DriveFolderChecker {
	return &readOnlyDriveProbe{reach: reach, notTrashed: notTrashed}
}

// ── Handler presence (Step 7, July 2026 — readiness gating) ──────────

// HandlerRegChecker verifies that critical job handlers are
// registered with the dispatcher. nil-safe: nil checker reports
// applicable=false (handled by runHandlerCheck nil-guard).
type HandlerRegChecker interface {
	MissingHandlers(ctx context.Context) (missing []string)
}

// RequiredHandlers is the canonical list of critical job
// handlers that MUST be registered for YouTube clips deploy
// readiness (Step 7, July 2026). Values are the canonical
// string-typed jobTypes per domain/job (TypeClipRegister +
// TypeBulkUploadYouTubeClips).
//
//   - media.clip: async register-batch handler (uses same ytSvc
//     as sync path). Canonical constant:
//     domain/job.TypeClipRegister = "media.clip".
//   - media.bulk_upload_youtube_clips: async bulk-upload worker
//     (uses same delivery.Publisher via ClipPublisherPort).
//     Canonical constant:
//     domain/job.TypeBulkUploadYouTubeClips = "media.bulk_upload_youtube_clips".
//
// Both async paths route through the SAME delivery.Publisher +
// Drive adapters as the sync register-from-youtube path, verified
// per AGENTS.md Step 7 wiring audit (2026-07-08).
var RequiredHandlers = []string{"media.clip", "media.bulk_upload_youtube_clips"}

// HandlerPresenceChecker probes HasHandler on the canonical
// appjobs.Service. The concrete interface avoids importing
// appjobs directly — callers pass a closure that adapts
// HasHandler (composition root in build_bundles_core.go wires
// the closure; the closure calls jobs.Service.HasHandler on the
// concrete dispatcher).
type HandlerPresenceChecker struct {
	has func(jobType string) bool
}

// MissingHandlers returns handler names that are NOT registered.
// nil-receiver safe (returns nil = fully registered).
func (c *HandlerPresenceChecker) MissingHandlers(_ context.Context) []string {
	if c == nil || c.has == nil {
		return nil
	}
	var missing []string
	for _, name := range RequiredHandlers {
		if !c.has(name) {
			missing = append(missing, name)
		}
	}
	return missing
}

// NewHandlerPresenceChecker builds a checker from a
// HasHandler-compatible closure. The adapter pattern keeps
// the health package decoupled from internal/jobs.
func NewHandlerPresenceChecker(has func(jobType string) bool) HandlerRegChecker {
	return &HandlerPresenceChecker{has: has}
}
