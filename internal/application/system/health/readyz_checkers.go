// Package health — readyz_checkers.go (slim orchestrator).
//
// PR-SPLIT-READYZ-CHECKERS closure (2026-08-08): split god-method
// from this single 780-LOC file into 4 same-package sister files
// per AGENTS.md Pattern 5 + godlike/06 SSOT one-canonical-owner-per-fact.
//
// Topology (godlike/06 SSOT — each file owns EXACTLY ONE capability):
//
//   - (A) THIS FILE: orchestrator — all With* setters + all 14
//     run*Check runners + formatNanos helper. Owns NO checker
//     interfaces, concretes, or NewX constructors.
//
//   - (B) readyz_checkers_tools.go: ToolsChecker application port. The
//     concrete CLI-toolpath readiness probe lives in
//     internal/platform/process.
//
//   - (C) readyz_checkers_canary.go: DriveCanaryPort +
//     HandlerRegChecker interfaces + publisherCanary concrete +
//     HandlerPresenceChecker concrete + RequiredHandlers var +
//     canaryErr + errCanaryPublisherNotWired +
//     errCanaryPublisherNilResult sentinels + NewPublisherCanary
//
//   - NewHandlerPresenceChecker. Owner of Drive canary-upload
//     probe + handler-presence probe (Section FileCanary).
//
//   - (D) readyz_checkers_fase6.go: 4 preflight interfaces
//     (DriveCredentialsChecker + DriveFolderChecker +
//     PublisherChecker + DestinationClipChecker) + their concretes
//     (FileCredentialsChecker + publisherFolderChecker +
//     wiredPublisherChecker + registryDestinationClipChecker) +
//     their NewX constructors + 5 FASE 6 capability interfaces
//     (TempWritableChecker + TTSChecker + DriveRootChecker +
//     OllamaChecker + OutboxChecker) + their concretes
//     (defaultTempWritableChecker + infrastructure TTS adapter +
//     driveRootAdapter + ollamaHealthAdapter + outboxPoolProbe) +
//     their NewX constructors. Owner of Step 4 drive-specific
//     preflight probes + FASE 6 advanced capability probes.
//
// Lookup paths preserved (same package, no import edge change):
//   - health.ReadyChecker (struct on *ReadyChecker — see ready.go)
//   - NewPublisherCanary /
//     NewHandlerPresenceChecker / NewDriveCredentialsChecker /
//     NewDriveFolderChecker / NewPublisherChecker /
//     NewDestinationClipChecker /
//     NewDriveRootChecker / NewOllamaChecker / NewOutboxChecker
//
// godlike/07 minimum-blast-radius: zero behavior change. Every
// runner is byte-equivalent to the pre-PR inline body (same timing,
// same field names, same error wrapping); every setter is a
// one-line field assignment with the same return-self fluent style.
// Future per-probe evolution OR new probes touches ONLY the
// relevant capability file (B/C/D); this orchestrator is stable.
package health

import (
	"context"
	"os"
	"time"
)

// ── With* fluent setters (orchestrator-owned, single source of truth) ─

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

// WithTempPath sets the temp-directory probe path; pairs with the
// default-temp writable checker (built lazily in (D)). Empty
// string opts out.
func (r *ReadyChecker) WithTempPath(path string) *ReadyChecker {
	r.tempPath = path
	r.tempChecker = &defaultTempWritableChecker{}
	return r
}

// WithTTSChecker attaches the Python TTS bridge probe.
func (r *ReadyChecker) WithTTSChecker(tc TTSChecker) *ReadyChecker {
	r.ttsChecker = tc
	return r
}

// WithDriveRootChecker attaches the Drive root-folder
// accessibility probe. Distinct from the credentials probe — those
// may be valid while the configured root folder is unreachable.
func (r *ReadyChecker) WithDriveRootChecker(drc DriveRootChecker) *ReadyChecker {
	r.driveRootChecker = drc
	return r
}

// WithDriveRootFolder sets the Drive root folder ID for the Drive
// root accessibility probe. Must be called AFTER WithDriveRootChecker
// or the check reports applicable=false. Empty string opts out.
func (r *ReadyChecker) WithDriveRootFolder(folderID string) *ReadyChecker {
	r.driveRootFolder = folderID
	return r
}

// WithOllamaChecker attaches the Ollama inference server probe.
func (r *ReadyChecker) WithOllamaChecker(oc OllamaChecker) *ReadyChecker {
	r.ollamaChecker = oc
	return r
}

// WithOutboxChecker attaches the outbox worker-pool liveness probe.
func (r *ReadyChecker) WithOutboxChecker(obc OutboxChecker) *ReadyChecker {
	r.outboxChecker = obc
	return r
}

// WithScriptGenerateCheck attaches the script.generate readiness checker.
// nil checker means the check is opted out.
func (r *ReadyChecker) WithScriptGenerateCheck(sgc ScriptGenerateChecker) *ReadyChecker {
	r.scriptGenerateCheck = sgc
	return r
}

// SetScriptRouteMounted wires the route-mounted probe for script.generate.
// The transport layer calls this after the gin engine is fully built.
func (r *ReadyChecker) SetScriptRouteMounted(fn func() bool) {
	if r == nil {
		return
	}
	r.scriptRouteMounted = fn
	if c, ok := r.scriptGenerateCheck.(*CompositeScriptGenerateChecker); ok {
		c.SetRouteMounted(fn)
	}
}

// ── Private check runners (orchestrator-owned, single source of truth) ─
//
// Each runner is byte-equivalent to the pre-PR inline body: same
// nil-guard semantic (nil checker → applicable=false), same time.Now
// / time.Since pair, same Checks-map field names, same error
// wrapping with the status flip on failure.

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

func (r *ReadyChecker) runScriptGenerateCheck(ctx context.Context, resp *HealthResponse) {
	if r.scriptGenerateCheck == nil {
		resp.Checks["script_generate"] = CheckResult{"ok": true, "applicable": false, "duration_ms": int64(0)}
		return
	}
	start := time.Now()
	result := r.scriptGenerateCheck.CheckScriptGenerate(ctx)
	elapsed := time.Since(start).Milliseconds()
	result["duration_ms"] = elapsed
	resp.Checks["script_generate"] = result
	if ok, _ := result["ok"].(bool); !ok {
		resp.OK = false
		resp.Status = "unhealthy"
	}
}

// formatNanos zero-pads nanoseconds to 9 digits so concurrent
// probes (same second) produce distinct filenames. Owned
// exclusively by the orchestrator so the runClipsPathCheck caller
// has a stable internal helper via same-package scope.
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
