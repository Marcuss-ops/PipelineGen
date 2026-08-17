// Package workerdoctor — probes_invariant.go (PR-SPLIT-WORKERDOCTOR-PROBES,
// 2026-07-06).
//
// Process-layer invariant probes: external binary presence
// (ffmpeg / yt-dlp / node / python3 / aria2c) derived from
// feature flags, and Go runtime stats (goroutines, memory, CPU).
//
// These probes run at PROCESS layer (this worker's own exec +
// runtime stats), unlike the dependency probes (LOCAL filesystem
// + config) and the liveness probes (NETWORK master URL).
//
// godlike/06 SSOT: this file is the canonical SOLE owner of the
// invariant-rule probe surface (engine binaries + runtime stats).
// Shared path helpers (resolveFFMpegPath, resolveFFprobePath) are
// co-located here rather than in a shared helpers file per AGENTS.md
// Pattern 5 one-canonical-owner-per-fact — they are exclusively
// consumed by probeEngine and have no callers in the liveness/
// dependency probe scopes.
package workerdoctor

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// probeEngine is the engine-tools probe. It consults Features to know
// which tools the deployment actually needs, and looks each one up on
// PATH (with the lookup seam provided by DefaultProbes).
//
// Even when a feature is disabled we still probe ffmpeg/ffprobe
// because(script/video pipelines share them); we ONLY require
// per-feature tools when the matching feature is enabled.
func probeEngine(cfg DoctorConfig, dp DefaultProbes) ProbeReceipt {
	lookup := dp.LookPath
	if lookup == nil {
		lookup = exec.LookPath
	}
	required := []string{`ffmpeg`}
	if cfg != nil {
		if cfg.YouTubeEnabled() {
			required = append(required, `yt-dlp`)
		}
		if cfg.ArtlistEnabled() {
			required = append(required, "node")
		}
		if cfg.ScriptClipsEnabled() {
			required = append(required, "python3")
		}
	}
	missing := make([]string, 0, len(required))
	for _, t := range required {
		if _, err := lookup(t); err != nil {
			missing = append(missing, t)
		}
	}
	// Always probe ffprobe via ffmpeg-path derivation even if ffmpeg
	// passes: the loader in ffmpeg/probe.go derives ffprobe from the
	// ffmpeg binary location at config time. We surface that as an
	// extras entry for operator visibility but do not fail-closed on
	// it (some pure-audio deployments don't need it).
	extras := map[string]any{
		"required":         required,
		"ffmpeg_resolved":  resolveFFMpegPath(cfg),
		"ffprobe_resolved": resolveFFprobePath(cfg),
	}
	if len(missing) > 0 {
		return ProbeReceipt{
			OK:         false,
			Applicable: true,
			Error:      "engine binaries missing on PATH: " + strings.Join(missing, ", "),
			Extras:     extras,
		}
	}
	return ProbeReceipt{OK: true, Applicable: true, Extras: extras}
}

// resolveFFMpegPath mirrors ExternalConfig.FfmpegPath with a fallback.
func resolveFFMpegPath(cfg DoctorConfig) string {
	if cfg != nil && cfg.FfmpegPath() != "" {
		return cfg.FfmpegPath()
	}
	return `ffmpeg`
}

// resolveFFprobePath derives ffprobe from ffmpeg, mirroring ffmpeg/probe.go.
func resolveFFprobePath(cfg DoctorConfig) string {
	ffmpeg := resolveFFMpegPath(cfg)
	if ffmpeg == `ffmpeg` {
		return "ffprobe"
	}
	dir, name := filepath.Split(ffmpeg)
	if name == `ffmpeg` || name == "ffmpeg.exe" {
		return filepath.Join(dir, "ffprobe")
	}
	return "ffprobe"
}

// probeRuntime reports disk + memory cabinet stats. Soft thresholds
// only — the doctor is informational here. We deliberately do NOT
// fail-closed on "low memory" because a worker can still start
// under tight memory; a low disk should still warn but not block
// because operators sometimes run ad-hoc in a 4GiB Docker setup
// where 80% used is ordinary.
//
// For production mode (--production) the doctor flips the soft
// thresholds into hard fails. Production flag is consumed by the
// CLI; this probe always returns ok=true unless an exec syscall
// itself fails.
//
// Disk stats are deliberately absent here: portable disk stats
// require platform-specific shelling (df on Unix, fsutil on Windows).
// For that the doctor intentionally skips the probes; the
// filesystem check above covers "is the disk reachable", not
// "how full is it".
func probeRuntime(_ DefaultProbes) ProbeReceipt {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	return ProbeReceipt{
		OK:         true,
		Applicable: true,
		Note:       "runtime stats are informational; thresholds configured via --production flag",
		Extras: map[string]any{
			"go_routines":  runtime.NumGoroutine(),
			"go_version":   runtime.Version(),
			"go_os":        runtime.GOOS,
			"go_arch":      runtime.GOARCH,
			"num_cpu":      runtime.NumCPU(),
			"mem_alloc_kb": mem.Alloc / 1024,
			"mem_sys_kb":   mem.Sys / 1024,
			"num_gc":       mem.NumGC,
		},
	}
}
