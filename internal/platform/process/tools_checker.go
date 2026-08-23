package process

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// DefaultToolsChecker probes required CLI tools through the platform PATH.
// The application layer owns only the ToolsChecker contract; this concrete
// implementation belongs to infrastructure because it performs OS lookup.
type DefaultToolsChecker struct {
	RequiredTools []string
}

func (c *DefaultToolsChecker) CheckTools(_ context.Context) []string {
	if c == nil {
		return nil
	}
	tools := c.RequiredTools
	if len(tools) == 0 {
		tools = []string{"yt-dlp", "ffmpeg", "ffprobe"}
	}
	var missing []string
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			missing = append(missing, tool)
		}
	}
	return missing
}

// ToolsChecker is the platform-facing tool lookup contract.
type ToolsChecker interface {
	CheckTools(ctx context.Context) (missing []string)
}

// TTSChecker is the platform-facing Python TTS probe contract.
type TTSChecker interface {
	CheckTTS(ctx context.Context) error
}

// NewToolsChecker constructs the production PATH-backed readiness adapter.
func NewToolsChecker() ToolsChecker {
	return &DefaultToolsChecker{RequiredTools: []string{"yt-dlp", "ffmpeg", "ffprobe"}}
}

// CommandTTSChecker probes the Python TTS bridge through the platform
// process boundary. The application layer depends only on health.TTSChecker.
type CommandTTSChecker struct {
	PythonBin string
	ScriptDir string
}

func (c *CommandTTSChecker) CheckTTS(ctx context.Context) error {
	python := c.PythonBin
	if python == "" {
		python = "python3"
	}
	cmd := exec.CommandContext(ctx, python, "-c", "import sys, edge_tts; sys.exit(0)")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s not available: %w", python, err)
	}
	if c.ScriptDir != "" {
		ttsScript := c.ScriptDir + "/bridges/tts_edge.py"
		if _, err := os.Stat(ttsScript); err != nil {
			return fmt.Errorf("TTS script %s not found: %w", ttsScript, err)
		}
	}
	return nil
}

// NewTTSChecker creates the infrastructure-owned Python TTS readiness adapter.
func NewTTSChecker(pythonBin, scriptDir string) TTSChecker {
	return &CommandTTSChecker{PythonBin: pythonBin, ScriptDir: scriptDir}
}
