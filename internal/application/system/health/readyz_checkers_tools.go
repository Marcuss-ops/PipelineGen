// Package health — readyz_checkers_tools.go (sister file B).
//
// PR-SPLIT-READYZ-CHECKERS closure (2026-08-08): canonical owner
// of the CLI-toolpath readiness probe. ToolsChecker (interface) +
// DefaultToolsChecker (concrete) + NewToolsChecker (canonical
// constructor) all live ONLY in this file per godlike/06 SSOT
// one-canonical-owner-per-fact. The orchestrator (A) attaches
// instances to *ReadyChecker via WithTools; the run*Check runner
// stays in (A) per the user-spec layout.
package health

import (
	"context"
	"os/exec"
)

// ToolsChecker verifies that required CLI tools (yt-dlp, ffmpeg,
// ffprobe) are present on the system PATH. nil-safe: nil checker
// reports ok=true + applicable=false (handled by the
// ReadyChecker.runToolsCheck nil-guard in the orchestrator).
type ToolsChecker interface {
	CheckTools(ctx context.Context) (missing []string)
}

// DefaultToolsChecker probes yt-dlp, ffmpeg, ffprobe (or any
// caller-supplied list) on PATH. The RequiredTools field is the
// allow-list override — when empty the canonical 3-tool default
// is used.
type DefaultToolsChecker struct {
	RequiredTools []string
}

// CheckTools returns any tools missing from PATH. nil-receiver
// safe (returns nil, i.e. no missing tools).
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

// NewToolsChecker creates a default tools checker for yt-dlp,
// ffmpeg, ffprobe. The composition root (utility_bundle.go or
// health_wiring.go) calls this once and attaches via
// ReadyChecker.WithTools.
func NewToolsChecker() ToolsChecker {
	return &DefaultToolsChecker{RequiredTools: []string{"yt-dlp", "ffmpeg", "ffprobe"}}
}
