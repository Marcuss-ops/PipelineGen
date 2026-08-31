// Package diagnostics — system_prober_ffmpeg.go: probeFFmpeg + the
// Renderer interface + FFmpegVersionProbeTimeout constant (Step 4
// follow-up, July 2026).
//
// godlike/06 SSOT: Renderer is the 1-method port AdminSystemProber
// needs from the detail.Processor cross-cutting dependency to probe
// FFmpeg binary presence/version. The interface is declared here
// (next to its sole consumer probeFFmpeg) per Pattern 0 +
// godlike/06's "smallest-port-possible" discipline; the canonical
// AdminSystemProber struct references the interface via same-package
// symbol resolution (no new import needed).
//
// godlike/07 NO-FAKE-AVAILABILITY §22: probeFFmpeg verifies both PATH
// presence (exec.LookPath) AND runtime execution (`ffmpeg -version`
// banner) — distinguishable from a simple `which ffmpeg` shell out
// that would silently succeed on a stale binary that fails to launch.
package diagnostics

import (
	"context"
	"os/exec"
	"strings"
	"time"

	artlist "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/artlist"
)

// FFmpegVersionProbeTimeout is the per-probe FFmpeg `-version`
// invocation budget. 2s — ffmpeg -version is a fast startup probe
// (no media processing, just version banner emission); 5s would be
// overkill and would risk hanging the entire diagnostics endpoint
// pile-up if FFmpeg is installed but misconfigured.
const FFmpegVersionProbeTimeout = 2 * time.Second

// Renderer is the minimal interface AdminSystemProber needs from the
// detail.Processor cross-cutting dependency to probe FFmpeg binary
// presence/version. The concrete is ffmpeg.NewProcessor; we declare a
// narrow 1-method port here to keep the SystemProber decoupled from
// the cross-cutting detail.Processor surface (godlike/06 Pattern 0:
// smallest-port-possible). Wired in Commit 2 (ffmpeg_binary probe).
//
// Pattern 0 placement: the interface is declared next to its sole
// consumer (probeFFmpeg, in this file) rather than in canonical. The
// AdminSystemProber struct (in canonical) references Renderer via
// same-package symbol resolution — no new import needed. Drift in
// the Renderer signature surfaces as a build failure in this file
// (where the impl is wired) rather than as a runtime panic in the
// canonical struct.
type Renderer interface {
	// FFmpegBinaryPath returns the resolved path to the ffmpeg binary,
	// or "" when no ffmpeg is available on the host.
	FFmpegBinaryPath() string
}

// probeFFmpeg returns the canonical ffmpeg_binary probe result.
// Two-step reachability check: (a) exec.LookPath("ffmpeg") honours
// $PATH when FFmpegBinaryPath is empty; (b) `ffmpeg -version` is
// invoked with a 2s timeout to verify the binary actually runs and
// emits the canonical `ffmpeg version X.Y.Z` banner.
//
// godlike/07 NO-FAKE-AVAILABILITY: the probe verifies both PATH
// presence AND runtime execution — distinguishable from a simple
// `which ffmpeg` shell out that would silently succeed on a stale
// binary that fails to launch.
//
// Errors are surfaced faithfully (string concatenation with the
// underlying exec.LookPath / Run error verbatim) so operators can
// grep for the canonical failure patterns (e.g.
// "exec.LookPath(\"ffmpeg\") failed: exec: \"ffmpeg\": executable
// file not found in $PATH").
func (p *AdminSystemProber) probeFFmpeg(ctx context.Context) artlist.ProbeResult {
	start := time.Now()

	ffmpegPath := strings.TrimSpace(p.FFmpegBinaryPath)
	if ffmpegPath == "" {
		// Fall back to exec.LookPath("ffmpeg") to honour $PATH —
		// matches the precedent set by
		// internal/capabilities/clips/upload/usecase.go line 361 +
		// cutter_test.go line 60 which use exec.LookPath directly
		// on the bare "ffmpeg" / "ffprobe" names.
		resolved, err := exec.LookPath("ffmpeg")
		if err != nil {
			return artlist.ProbeResult{
				OK:        false,
				Error:     "ffmpeg_binary_not_found",
				Detail:    "exec.LookPath(\"ffmpeg\") failed: " + err.Error() + " (operator must install ffmpeg on the host or set AdminSystemProber.FFmpegBinaryPath explicitly)",
				ElapsedMs: time.Since(start).Milliseconds(),
			}
		}
		ffmpegPath = resolved
	}

	// Resolve the runner (test fixtures swap a stub; production
	// wires DefaultRunner) and run ffmpeg -version with a 2s
	// per-probe timeout.
	runner := p.FFmpegRunner
	if runner == nil {
		runner = DefaultRunner{}
	}
	verCtx, verCancel := context.WithTimeout(ctx, FFmpegVersionProbeTimeout)
	defer verCancel()

	stdout, err := runner.Run(verCtx, ffmpegPath, []string{"-version"})
	if err != nil {
		return artlist.ProbeResult{
			OK:        false,
			Error:     "ffmpeg_version_probe_failed",
			Detail:    "ffmpeg -version invocation failed: " + err.Error(),
			ElapsedMs: time.Since(start).Milliseconds(),
		}
	}

	// Parse the version banner. ffmpeg -version output starts with
	// `ffmpeg version X.Y.Z ...`. If the banner is missing the
	// `ffmpeg` substring the binary at this path is not actually
	// FFmpeg (operator mis-config, wrong path); surface honestly
	// rather than silently OK.
	banner := strings.TrimSpace(string(stdout))
	if !strings.Contains(banner, "ffmpeg") {
		return artlist.ProbeResult{
			OK:        false,
			Error:     "ffmpeg_version_unparseable",
			Detail:    "ffmpeg -version output did not contain 'ffmpeg' header (binary at " + ffmpegPath + " is not FFmpeg?): " + banner,
			ElapsedMs: time.Since(start).Milliseconds(),
		}
	}

	firstLine := banner
	if idx := strings.Index(banner, "\n"); idx >= 0 {
		firstLine = banner[:idx]
	}
	return artlist.ProbeResult{
		OK:        true,
		Detail:    "ffmpeg binary=" + ffmpegPath + ", banner=\"" + firstLine + "\", elapsed=" + time.Since(start).String(),
		ElapsedMs: time.Since(start).Milliseconds(),
	}
}
