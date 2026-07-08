// File readyz_checkers.go — Step 8 severe readiness check implementations
// (YouTube Clips Deploy Readiness action plan, July 2026).
//
// godlike/06 SSOT (one canonical owner per fact): each checker interface
// + its concrete helper lives ONLY in this file. The ReadyChecker
// (ready.go) consumes them via the private run*Check methods.
package health

import (
	"context"
	"fmt"
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

// ── Step 4 Drive-specific checker interfaces (July 2026) ─────────────────

// DriveCredentialsChecker verifies that Drive OAuth token and
// credentials files exist on disk. nil-safe: nil checker = ok+applicable=false.
type DriveCredentialsChecker interface {
	CredentialsPresent(ctx context.Context) (missing []string, err error)
}

// DriveFolderChecker verifies that the configured Drive folder is
// reachable (can list or stat). nil-safe.
type DriveFolderChecker interface {
	CheckFolder(ctx context.Context, folderID string) error
}

// PublisherChecker verifies that the canonical delivery.Publisher
// is wired (non-nil). nil-safe.
type PublisherChecker interface {
	IsWired() bool
}

// DestinationClipChecker verifies that DestinationYouTubeClip is
// registered in the DestinationRegistry. nil-safe.
type DestinationClipChecker interface {
	ClipDestinationRegistered() bool
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

// WithDriveCredentials attaches the Drive credentials checker.
// nil checker means the check is opted out.
func (r *ReadyChecker) WithDriveCredentials(dc DriveCredentialsChecker) *ReadyChecker {
	r.driveCreds = dc
	return r
}

// WithDriveFolder attaches the Drive folder access checker.
// nil checker OR empty folderID means the check is opted out.
func (r *ReadyChecker) WithDriveFolder(df DriveFolderChecker, folderID string) *ReadyChecker {
	r.driveFolder = df
	r.driveFolderID = folderID
	return r
}

// WithPublisherCheck attaches the Publisher wiring checker.
// nil checker means the check is opted out.
func (r *ReadyChecker) WithPublisherCheck(pc PublisherChecker) *ReadyChecker {
	r.publisherCheck = pc
	return r
}

// WithDestinationClipCheck attaches the DestinationClip registry checker.
// nil checker means the check is opted out.
func (r *ReadyChecker) WithDestinationClipCheck(dc DestinationClipChecker) *ReadyChecker {
	r.destClipCheck = dc
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

// ── Step 4 private check runners (July 2026) ────────────────────────────

func (r *ReadyChecker) runDriveCredentialsCheck(ctx context.Context, resp *HealthResponse) {
	if r.driveCreds == nil {
		resp.Checks["drive_credentials"] = CheckResult{"ok": true, "applicable": false, "duration_ms": int64(0)}
		return
	}
	start := time.Now()
	missing, err := r.driveCreds.CredentialsPresent(ctx)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		resp.Checks["drive_credentials"] = CheckResult{"ok": false, "duration_ms": elapsed, "error": err.Error()}
		resp.OK = false
		resp.Status = "unhealthy"
	} else if len(missing) > 0 {
		resp.Checks["drive_credentials"] = CheckResult{"ok": false, "duration_ms": elapsed, "error": "missing Drive credential files", "missing": missing}
		resp.OK = false
		resp.Status = "unhealthy"
	} else {
		resp.Checks["drive_credentials"] = CheckResult{"ok": true, "duration_ms": elapsed, "credentials_found": true}
	}
}

func (r *ReadyChecker) runDriveFolderCheck(ctx context.Context, resp *HealthResponse) {
	if r.driveFolder == nil || r.driveFolderID == "" {
		resp.Checks["drive_folder"] = CheckResult{"ok": true, "applicable": false, "duration_ms": int64(0)}
		return
	}
	start := time.Now()
	err := r.driveFolder.CheckFolder(ctx, r.driveFolderID)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		resp.Checks["drive_folder"] = CheckResult{"ok": false, "duration_ms": elapsed, "error": "drive folder not accessible: " + err.Error(), "folder": r.driveFolderID}
		resp.OK = false
		resp.Status = "unhealthy"
	} else {
		resp.Checks["drive_folder"] = CheckResult{"ok": true, "duration_ms": elapsed, "folder": r.driveFolderID, "accessible": true}
	}
}

func (r *ReadyChecker) runPublisherCheck(resp *HealthResponse) {
	if r.publisherCheck == nil {
		resp.Checks["publisher"] = CheckResult{"ok": true, "applicable": false, "duration_ms": int64(0)}
		return
	}
	start := time.Now()
	wired := r.publisherCheck.IsWired()
	elapsed := time.Since(start).Milliseconds()
	if !wired {
		resp.Checks["publisher"] = CheckResult{"ok": false, "duration_ms": elapsed, "error": "delivery.Publisher not wired (composition root must wire DriveBundle.Publisher)"}
		resp.OK = false
		resp.Status = "unhealthy"
	} else {
		resp.Checks["publisher"] = CheckResult{"ok": true, "duration_ms": elapsed, "wired": true}
	}
}

func (r *ReadyChecker) runDestinationClipCheck(resp *HealthResponse) {
	if r.destClipCheck == nil {
		resp.Checks["destination_clip"] = CheckResult{"ok": true, "applicable": false, "duration_ms": int64(0)}
		return
	}
	start := time.Now()
	registered := r.destClipCheck.ClipDestinationRegistered()
	elapsed := time.Since(start).Milliseconds()
	if !registered {
		resp.Checks["destination_clip"] = CheckResult{"ok": false, "duration_ms": elapsed, "error": "DestinationYouTubeClip not registered in DestinationRegistry"}
		resp.OK = false
		resp.Status = "unhealthy"
	} else {
		resp.Checks["destination_clip"] = CheckResult{"ok": true, "duration_ms": elapsed, "registered": true}
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
// MUST be registered for YouTube clips deploy readiness (Step 7, July 2026).
//
//   - media.clip: async register-batch handler (uses same ytSvc as sync path)
//     Canonical constant: domain/job.TypeClipRegister = "media.clip"
//   - media.bulk_upload_youtube_clips: async bulk-upload worker (uses same
//     delivery.Publisher via ClipPublisherPort as sync path)
//     Canonical constant: domain/job.TypeBulkUploadYouTubeClips
//
// Both async paths route through the SAME delivery.Publisher + Drive
// adapters as the sync register-from-youtube path, verified per
// AGENTS.md Step 7 wiring audit (2026-07-08).
var RequiredHandlers = []string{"media.clip", "media.bulk_upload_youtube_clips"}

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

// ── FASE 6 severe readiness probes (July 2026) ────────────────────────
//
// Each probe follows the established ReadyChecker pattern:
//   1. Declare a typed interface (one method)
//   2. Add a With* fluent setter on ReadyChecker
//   3. Add a private run*Check method
//   4. Call it from CheckReady()
//   5. Wire in composition root (build_bundles_core.go::BuildUtilityBundle)
//
// Nil/unwired probes report {ok:true, applicable:false} so the /ready
// shape is stable across deploy profiles.

// TempWritableChecker verifies a directory path is writable by creating
// and removing a probe file. nil-safe: nil checker reports applicable=false.
type TempWritableChecker interface {
	CheckTempWritable(path string) error
}

// TTSChecker verifies the Python TTS bridge is available (python3 can
// import the required modules). nil-safe.
type TTSChecker interface {
	CheckTTS(ctx context.Context) error
}

// DriveRootChecker verifies the Drive root folder is accessible
// (folder exists and is reachable via the Drive API). Distinct from
// the Drive credential check — credentials may be valid but the
// configured root folder may have been deleted or its permissions
// revoked. nil-safe.
type DriveRootChecker interface {
	CheckDriveRoot(ctx context.Context, folderID string) error
}

// OllamaChecker verifies the Ollama inference server is reachable
// and returns a valid response. nil-safe.
type OllamaChecker interface {
	CheckOllama(ctx context.Context) error
}

// OutboxChecker verifies the outbox worker pool is running and
// processing events (not just that the outbox table exists in DB).
// nil-safe.
type OutboxChecker interface {
	CheckOutboxWorker(ctx context.Context) error
}

// ── ReadyChecker FASE 6 extensions ──────────────────────────────────

func (r *ReadyChecker) WithTempPath(path string) *ReadyChecker {
	r.tempPath = path
	r.tempChecker = &defaultTempWritableChecker{}
	return r
}

func (r *ReadyChecker) WithTTSChecker(tc TTSChecker) *ReadyChecker {
	r.ttsChecker = tc
	return r
}

func (r *ReadyChecker) WithDriveRootChecker(drc DriveRootChecker) *ReadyChecker {
	r.driveRootChecker = drc
	return r
}

// WithDriveRootFolder sets the Drive root folder ID for the Drive root
// accessibility probe. Must be called after WithDriveRootChecker or the
// check reports applicable=false. Empty string opts out.
func (r *ReadyChecker) WithDriveRootFolder(folderID string) *ReadyChecker {
	r.driveRootFolder = folderID
	return r
}

func (r *ReadyChecker) WithOllamaChecker(oc OllamaChecker) *ReadyChecker {
	r.ollamaChecker = oc
	return r
}

func (r *ReadyChecker) WithOutboxChecker(obc OutboxChecker) *ReadyChecker {
	r.outboxChecker = obc
	return r
}

// ── Private FASE 6 check runners ────────────────────────────────────

func (r *ReadyChecker) runTempPathCheck(resp *HealthResponse) {
	if r.tempChecker == nil || r.tempPath == "" {
		resp.Checks["temp"] = CheckResult{"ok": true, "applicable": false, "duration_ms": int64(0)}
		return
	}
	start := time.Now()
	err := r.tempChecker.CheckTempWritable(r.tempPath)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		resp.Checks["temp"] = CheckResult{"ok": false, "duration_ms": elapsed, "error": "temp path not writable: " + err.Error(), "path": r.tempPath}
		resp.OK = false
		resp.Status = "unhealthy"
	} else {
		resp.Checks["temp"] = CheckResult{"ok": true, "duration_ms": elapsed, "path": r.tempPath, "writable": true}
	}
}

func (r *ReadyChecker) runTTSCheck(ctx context.Context, resp *HealthResponse) {
	if r.ttsChecker == nil {
		resp.Checks["tts"] = CheckResult{"ok": true, "applicable": false, "duration_ms": int64(0)}
		return
	}
	start := time.Now()
	err := r.ttsChecker.CheckTTS(ctx)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		resp.Checks["tts"] = CheckResult{"ok": false, "duration_ms": elapsed, "error": "TTS unavailable: " + err.Error()}
		resp.OK = false
		resp.Status = "unhealthy"
	} else {
		resp.Checks["tts"] = CheckResult{"ok": true, "duration_ms": elapsed, "tts_available": true}
	}
}

func (r *ReadyChecker) runDriveRootCheck(ctx context.Context, resp *HealthResponse) {
	if r.driveRootChecker == nil || r.driveRootFolder == "" {
		resp.Checks["drive_root"] = CheckResult{"ok": true, "applicable": false, "duration_ms": int64(0)}
		return
	}
	start := time.Now()
	err := r.driveRootChecker.CheckDriveRoot(ctx, r.driveRootFolder)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		resp.Checks["drive_root"] = CheckResult{"ok": false, "duration_ms": elapsed, "error": "Drive root folder inaccessible: " + err.Error(), "folder_id": r.driveRootFolder}
		resp.OK = false
		resp.Status = "unhealthy"
	} else {
		resp.Checks["drive_root"] = CheckResult{"ok": true, "duration_ms": elapsed, "folder_id": r.driveRootFolder, "accessible": true}
	}
}

func (r *ReadyChecker) runOllamaCheck(ctx context.Context, resp *HealthResponse) {
	if r.ollamaChecker == nil {
		resp.Checks["ollama"] = CheckResult{"ok": true, "applicable": false, "duration_ms": int64(0)}
		return
	}
	start := time.Now()
	err := r.ollamaChecker.CheckOllama(ctx)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		resp.Checks["ollama"] = CheckResult{"ok": false, "duration_ms": elapsed, "error": "Ollama unreachable: " + err.Error()}
		resp.OK = false
		resp.Status = "unhealthy"
	} else {
		resp.Checks["ollama"] = CheckResult{"ok": true, "duration_ms": elapsed, "ollama_reachable": true}
	}
}

func (r *ReadyChecker) runOutboxCheck(ctx context.Context, resp *HealthResponse) {
	if r.outboxChecker == nil {
		resp.Checks["outbox"] = CheckResult{"ok": true, "applicable": false, "duration_ms": int64(0)}
		return
	}
	start := time.Now()
	err := r.outboxChecker.CheckOutboxWorker(ctx)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		resp.Checks["outbox"] = CheckResult{"ok": false, "duration_ms": elapsed, "error": "outbox worker unavailable: " + err.Error()}
		resp.OK = false
		resp.Status = "unhealthy"
	} else {
		resp.Checks["outbox"] = CheckResult{"ok": true, "duration_ms": elapsed, "outbox_worker_active": true}
	}
}

// ── Concrete FASE 6 checker implementations ─────────────────────────

// defaultTempWritableChecker verifies writability via os.CreateTemp.
type defaultTempWritableChecker struct{}

func (c *defaultTempWritableChecker) CheckTempWritable(path string) error {
	f, err := os.CreateTemp(path, ".readyz-temp-probe-*")
	if err != nil {
		return err
	}
	f.Close()
	return os.Remove(f.Name())
}

// CommandTTSChecker probes the Python TTS bridge by spawning
// python3 -c "<import-statement>" and checking exit code.
type CommandTTSChecker struct {
	PythonBin string // python3 path; empty → exec.LookPath("python3")
	ScriptDir string // directory containing TTS scripts
}

func (c *CommandTTSChecker) CheckTTS(ctx context.Context) error {
	python := c.PythonBin
	if python == "" {
		python = "python3"
	}
	// Fast probe: can python3 import the TTS bridge modules?
	// The TTS bridge uses edge_tts + aiohttp; probe imports.
	cmd := exec.CommandContext(ctx, python, "-c", "import sys, edge_tts; sys.exit(0)")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s not available: %w", python, err)
	}
	// If script dir is set, verify the TTS script exists on disk.
	if c.ScriptDir != "" {
		ttsScript := c.ScriptDir + "/tts_edge.py"
		if _, err := os.Stat(ttsScript); err != nil {
			return fmt.Errorf("TTS script %s not found: %w", ttsScript, err)
		}
	}
	return nil
}

// NewTTSChecker creates a Python TTS probe. scriptDir is optional
// (empty → skip script-exists check).
func NewTTSChecker(pythonBin, scriptDir string) TTSChecker {
	return &CommandTTSChecker{PythonBin: pythonBin, ScriptDir: scriptDir}
}

// driveRootAdapter satisfies DriveRootChecker via a reachability
// closure. The composition root adapts drive.Reader.ListFiles to
// an error-only probe (discard file list, only check the error).
type driveRootAdapter struct {
	probe func(ctx context.Context, folderID string) error
}

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

// ollamaHealthAdapter wraps an Ollama health-check function for the
// ReadyChecker probe interface.
type ollamaHealthAdapter struct {
	check func(ctx context.Context) bool
}

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
// CheckHealth-style closure.
func NewOllamaChecker(check func(ctx context.Context) bool) OllamaChecker {
	return &ollamaHealthAdapter{check: check}
}

// outboxPoolProbe wraps an outbox-pool liveness probe for the
// ReadyChecker interface.
type outboxPoolProbe struct {
	probe func(ctx context.Context) error
}

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

// ── Step 4 concrete checker implementations (July 2026) ──────────────────

// FileCredentialsChecker probes token.json + credentials.json on disk.
type FileCredentialsChecker struct {
	TokenPath       string
	CredentialsPath string
}

// CredentialsPresent returns any missing credential files.
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

// NewDriveCredentialsChecker builds a file-based Drive credentials checker.
func NewDriveCredentialsChecker(tokenPath, credentialsPath string) DriveCredentialsChecker {
	return &FileCredentialsChecker{TokenPath: tokenPath, CredentialsPath: credentialsPath}
}

// publisherFolderChecker wraps delivery.Publisher for folder access verification.
type publisherFolderChecker struct {
	pub delivery.Publisher
}

// CheckFolder verifies the folder exists by attempting to resolve it.
func (c *publisherFolderChecker) CheckFolder(ctx context.Context, folderID string) error {
	if c.pub == nil {
		return errCanaryPublisherNotWired
	}
	_, err := c.pub.ResolveFolder(ctx, delivery.PublishRequest{
		Destination:        delivery.DestinationYouTubeClip,
		RootFolderOverride: folderID,
	})
	return err
}

// NewDriveFolderChecker creates a folder checker backed by delivery.Publisher.
func NewDriveFolderChecker(pub delivery.Publisher) DriveFolderChecker {
	return &publisherFolderChecker{pub: pub}
}

// wiredPublisherChecker checks delivery.Publisher is non-nil.
type wiredPublisherChecker struct {
	pub delivery.Publisher
}

// IsWired returns true if the Publisher field is non-nil.
func (c *wiredPublisherChecker) IsWired() bool {
	return c != nil && c.pub != nil
}

// NewPublisherChecker creates a Publisher wiring checker.
func NewPublisherChecker(pub delivery.Publisher) PublisherChecker {
	return &wiredPublisherChecker{pub: pub}
}

// registryDestinationClipChecker wraps DestinationRegistry.Has.
type registryDestinationClipChecker struct {
	has func(key delivery.DestinationKey) bool
}

// ClipDestinationRegistered returns true if DestinationYouTubeClip is registered.
func (c *registryDestinationClipChecker) ClipDestinationRegistered() bool {
	if c == nil || c.has == nil {
		return false
	}
	return c.has(delivery.DestinationYouTubeClip)
}

// NewDestinationClipChecker builds a checker from a Has closure on the registry.
func NewDestinationClipChecker(has func(key delivery.DestinationKey) bool) DestinationClipChecker {
	return &registryDestinationClipChecker{has: has}
}
