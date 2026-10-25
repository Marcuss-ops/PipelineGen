package audio

import (
	"fmt"
	"os"
	"strings"
)

// FinalAudioOutputPath reserves a unique output path for one render of
// planSHA256.
//
// The plan hash is deterministic, so the historical name
// `<tmp>/pipelinegen-final-audio-<sha>.m4a` named the SAME file for every run of
// that plan. Two components (or two workers) rendering the same plan at once
// wrote to the same path: one writer truncated the file while the other probed
// it, producing `ffprobe exited with status 1` and intermittently failing the
// listen-gate suites. The reserved path keeps the plan hash for traceability
// but is unique per invocation (os.CreateTemp appends a random suffix, and the
// PID is captured by the OS temp namespace); the placeholder is removed before
// the renderer creates the master.
//
// The caller owns the returned file for the lifetime of the render and its
// consumers; this helper only reserves the name.
func FinalAudioOutputPath(planSHA256 string) (string, error) {
	pattern := "pipelinegen-final-audio-"
	if hash := strings.TrimSpace(planSHA256); hash != "" {
		pattern += hash + "-"
	}
	pattern += "*.m4a"
	file, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", fmt.Errorf("reserve final audio output: %w", err)
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("reserve final audio output: %w", err)
	}
	if err := os.Remove(path); err != nil {
		return "", fmt.Errorf("reserve final audio output: %w", err)
	}
	return path, nil
}
