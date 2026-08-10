// Package health — readyz_checkers_canary.go (sister file C).
//
// PR-SPLIT-READYZ-CHECKERS closure (2026-08-08): canonical owner
// of two readiness probes: (i) Drive canary-upload via the
// delivery.Publisher port, (ii) job-handler presence via a
// HasHandler closure. Each capability is a complete "trio":
// interface + concrete + NewX constructor — all stored under
// their own godlike/06 SSOT owner-per-fact boundary.
//
// Type inventory (all private except NewX + RequiredHandlers):
//   - DriveCanaryPort (interface, public)
//   - publisherCanary (concrete, private)
//   - canaryErr (private typed-error)
//   - errCanaryPublisherNotWired (sentinel)
//   - errCanaryPublisherNilResult (sentinel)
//   - NewPublisherCanary (public ctor)
//   - HandlerRegChecker (interface, public)
//   - HandlerPresenceChecker (concrete, public struct)
//   - RequiredHandlers (public var, the canonical critical-handler list)
//   - NewHandlerPresenceChecker (public ctor)
package health

import (
	"context"
	"os"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
)

// DriveCanaryPort performs a real Drive canary upload and returns
// nil on success. nil-safe per the ReadyChecker aggregation
// contract (orchestrator-runner nil-guards before invocation).
type DriveCanaryPort interface {
	CanaryUpload(ctx context.Context, folderID string) error
}

// publisherCanary wraps delivery.Publisher for the ReadyChecker
// canary probe. The concrete implementation seeds a small temp
// file with real content (NOT /dev/null, which has 0 bytes and
// causes publisher-side hash-compute failures on some backends).
type publisherCanary struct {
	pub delivery.Publisher
}

// CanaryUpload performs a real Drive upload of a small dummy
// file. Returns errCanaryPublisherNotWired if pub is nil;
// errCanaryPublisherNilResult if Publish returns nil result.
func (c *publisherCanary) CanaryUpload(ctx context.Context, folderID string) error {
	if c.pub == nil {
		return errCanaryPublisherNotWired
	}
	// Write a small temp file with real content so Publisher can
	// compute SHA256 + upload successfully.
	tmp, err := os.CreateTemp("", "readyz-canary-*.txt")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString("/readyz Drive canary probe\n"); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()
	result, err := c.pub.Publish(ctx, delivery.PublishRequest{
		Destination:    delivery.DestinationAdmin,
		LocalPath:      tmp.Name(),
		Filename:       "readyz-canary.txt",
		Description:    "/readyz Drive canary probe",
		ParentFolderID: folderID,
	})
	if err != nil {
		return err
	}
	if result == nil {
		return errCanaryPublisherNilResult
	}
	return nil
}

// errCanaryPublisherNotWired is the canonical sentinel for an
// unwired canary (composition root did not wire delivery.Publisher).
var errCanaryPublisherNotWired = &canaryErr{msg: "publisher not wired"}

// errCanaryPublisherNilResult is the canonical sentinel for a
// Publisher.Publish that returned nil result without error
// (defensive guard against future publisher interface regressions).
var errCanaryPublisherNilResult = &canaryErr{msg: "publish returned nil result"}

// canaryErr is the typed-error carrier for both canary sentinels.
// Implements error; field is unexported so callers must use the
// two sense-bounded sentinels.
type canaryErr struct{ msg string }

func (e *canaryErr) Error() string { return e.msg }

// NewPublisherCanary creates a Drive canary backed by
// delivery.Publisher. Returns a nil-safe DriveCanaryPort that
// returns errCanaryPublisherNotWired when pub is nil.
func NewPublisherCanary(pub delivery.Publisher) DriveCanaryPort {
	return &publisherCanary{pub: pub}
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
