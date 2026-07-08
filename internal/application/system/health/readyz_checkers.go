// File readyz_checkers.go — Step 8 severe readiness check implementations
// (YouTube Clips Deploy Readiness action plan, July 2026).
//
// godlike/06 SSOT (one canonical owner per fact): each checker interface
// + its concrete helper lives ONLY in this file. The ReadyChecker
// (ready.go) consumes them via the private run*Check methods.
package health

import (
	"context"
	"os"
	"os/exec"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
)

// ── Checker interfaces ──────────────────────────────────────────────────

// ToolsChecker verifies that required CLI tools (yt-dlp, ffmpeg, ffprobe)
// are present on the system PATH. nil-safe: nil checker reports
// ok=true + applicable=false.
type ToolsChecker interface {
	CheckTools(ctx context.Context) (missing []string)
}

// DriveCanaryPort performs a real Drive canary upload and returns nil
// on success. nil-safe per the ReadyChecker aggregation contract.
type DriveCanaryPort interface {
	CanaryUpload(ctx context.Context, folderID string) error
}

// HandlerRegChecker verifies that critical job handlers are registered
// with the dispatcher. nil-safe: nil checker reports applicable=false.
type HandlerRegChecker interface {
	MissingHandlers(ctx context.Context) (missing []string)
}

// ── ReadyChecker Step 8 extensions ──────────────────────────────────────

// WithTools attaches the ToolsChecker to the ReadyChecker.
// nil-safe: passing nil means the tools check is opted out.
func (r *ReadyChecker) WithTools(tc ToolsChecker) *ReadyChecker {
	r.tools = tc
	return r
}

// WithClipsPath sets the writable path to probe for clips.
// Empty string means the check is opted out.
func (r *ReadyChecker) WithClipsPath(path string) *ReadyChecker {
	r.clipsPath = path
	return r
}

// WithDriveCanary attaches the Drive canary port + target folder.
// nil port OR empty folder means the canary is opted out.
func (r *ReadyChecker) WithDriveCanary(canary DriveCanaryPort, folderID string) *ReadyChecker {
	r.canary = canary
	r.canaryFolder = folderID
	return r
}

// WithHandlerCheck attaches the handler registration checker.
// nil checker means the check is opted out.
func (r *ReadyChecker) WithHandlerCheck(hc HandlerRegChecker) *ReadyChecker {
	r.handlerCheck = hc
	return r
}

// ── Private check runners ───────────────────────────────────────────────

func (r *ReadyChecker) runToolsCheck(ctx context.Context, resp *HealthResponse) {
	if r.tools == nil {
		resp.Checks["tools"] = CheckResult{"ok": true, "applicable": false, "duration_ms": int64(0)}
		return
	}
	start := time.Now()
	missing := r.tools.CheckTools(ctx)
	elapsed := time.Since(start).Milliseconds()
	if len(missing) == 0 {
		resp.Checks["tools"] = CheckResult{"ok": true, "duration_ms": elapsed, "tools_found": true}
	} else {
		resp.Checks["tools"] = CheckResult{"ok": false, "duration_ms": elapsed, "error": "missing CLI tools", "missing": missing}
		resp.OK = false
		resp.Status = "unhealthy"
	}
}

func (r *ReadyChecker) runClipsPathCheck(resp *HealthResponse) {
	if r.clipsPath == "" {
		resp.Checks["clips_path"] = CheckResult{"ok": true, "applicable": false, "duration_ms": int64(0)}
		return
	}
	start := time.Now()
	now := time.Now()
	tmpFile := r.clipsPath + "/.readiness-probe-" + now.Format("20060102-150405") + "-" + formatNanos(now.Nanosecond())
	elapsed := time.Since(start).Milliseconds()
	// Try to create + remove a probe file to verify writability.
	if f, err := os.Create(tmpFile); err != nil {
		resp.Checks["clips_path"] = CheckResult{"ok": false, "duration_ms": elapsed, "error": "clips path not writable: " + err.Error()}
		resp.OK = false
		resp.Status = "unhealthy"
	} else {
		f.Close()
		os.Remove(tmpFile)
		resp.Checks["clips_path"] = CheckResult{"ok": true, "duration_ms": elapsed, "path": r.clipsPath, "writable": true}
	}
}

func (r *ReadyChecker) runCanaryCheck(ctx context.Context, resp *HealthResponse) {
	if r.canary == nil || r.canaryFolder == "" {
		resp.Checks["drive_canary"] = CheckResult{"ok": true, "applicable": false, "duration_ms": int64(0)}
		return
	}
	start := time.Now()
	err := r.canary.CanaryUpload(ctx, r.canaryFolder)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		resp.Checks["drive_canary"] = CheckResult{"ok": false, "duration_ms": elapsed, "error": "drive canary upload failed: " + err.Error()}
		resp.OK = false
		resp.Status = "unhealthy"
	} else {
		resp.Checks["drive_canary"] = CheckResult{"ok": true, "duration_ms": elapsed, "folder": r.canaryFolder}
	}
}

func (r *ReadyChecker) runHandlerCheck(ctx context.Context, resp *HealthResponse) {
	if r.handlerCheck == nil {
		resp.Checks["handlers"] = CheckResult{"ok": true, "applicable": false, "duration_ms": int64(0)}
		return
	}
	start := time.Now()
	missing := r.handlerCheck.MissingHandlers(ctx)
	elapsed := time.Since(start).Milliseconds()
	if len(missing) == 0 {
		resp.Checks["handlers"] = CheckResult{"ok": true, "duration_ms": elapsed, "handlers_registered": true}
	} else {
		resp.Checks["handlers"] = CheckResult{"ok": false, "duration_ms": elapsed, "error": "missing critical handlers", "missing": missing}
		resp.OK = false
		resp.Status = "unhealthy"
	}
}

// formatNanos zero-pads nanoseconds to 9 digits so concurrent
// probes (same second) produce distinct filenames.
func formatNanos(ns int) string {
	return string([]byte{
		byte('0' + ns/100_000_000),
		byte('0' + (ns/10_000_000)%10),
		byte('0' + (ns/1_000_000)%10),
		byte('0' + (ns/100_000)%10),
		byte('0' + (ns/10_000)%10),
		byte('0' + (ns/1_000)%10),
		byte('0' + (ns/100)%10),
		byte('0' + (ns/10)%10),
		byte('0' + ns%10),
	})
}

// ── Concrete checker implementations ────────────────────────────────────

// DefaultToolsChecker probes yt-dlp, ffmpeg, ffprobe on PATH.
type DefaultToolsChecker struct {
	RequiredTools []string
}

// CheckTools returns any tools missing from PATH.
func (c *DefaultToolsChecker) CheckTools(_ context.Context) []string {
	if c == nil {
		return nil
	}
	var missing []string
	tools := c.RequiredTools
	if len(tools) == 0 {
		tools = []string{"yt-dlp", "ffmpeg", "ffprobe"}
	}
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			missing = append(missing, tool)
		}
	}
	return missing
}

// publisherCanary wraps delivery.Publisher for the ReadyChecker canary.
type publisherCanary struct {
	pub delivery.Publisher
}

// CanaryUpload performs a real Drive upload of a small dummy file.
// Uses a temp file with actual content (not /dev/null, which has 0
// bytes and causes Publisher hash-compute failures on some backends).
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
		Destination:        delivery.DestinationAdmin,
		LocalPath:          tmp.Name(),
		Filename:           "readyz-canary.txt",
		Description:        "/readyz Drive canary probe",
		RootFolderOverride: folderID,
	})
	if err != nil {
		return err
	}
	if result == nil {
		return errCanaryPublisherNilResult
	}
	return nil
}

// errCanaryPublisherNotWired is a sentinel for unwired canary.
var errCanaryPublisherNotWired = &canaryErr{"publisher not wired"}

// errCanaryPublisherNilResult is a sentinel for nil result.
var errCanaryPublisherNilResult = &canaryErr{"publish returned nil result"}

type canaryErr struct{ msg string }

func (e *canaryErr) Error() string { return e.msg }

// NewPublisherCanary creates a Drive canary backed by delivery.Publisher.
func NewPublisherCanary(pub delivery.Publisher) DriveCanaryPort {
	return &publisherCanary{pub: pub}
}

// NewToolsChecker creates a default tools checker for yt-dlp, ffmpeg, ffprobe.
func NewToolsChecker() ToolsChecker {
	return &DefaultToolsChecker{RequiredTools: []string{"yt-dlp", "ffmpeg", "ffprobe"}}
}

// RequiredHandlers is the canonical list of critical job handlers that
// MUST be registered for YouTube clips deploy readiness (Step 8, July 2026).
var RequiredHandlers = []string{"media.clip", "clips.process"}

// HandlerPresenceChecker probes HasHandler on the canonical
// appjobs.Service. The concrete interface avoids importing appjobs
// directly — callers pass a closure that adapts HasHandler.
type HandlerPresenceChecker struct {
	has func(jobType string) bool
}

// MissingHandlers returns handler names that are NOT registered.
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

// NewHandlerPresenceChecker builds a checker from a HasHandler closure.
func NewHandlerPresenceChecker(has func(jobType string) bool) HandlerRegChecker {
	return &HandlerPresenceChecker{has: has}
}
