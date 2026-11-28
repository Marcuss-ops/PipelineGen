// Command verbatimcheck compares a source_text_verbatim narration with a
// word-aligned WebVTT transcript and the corresponding audio duration.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/verbatimcheck"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
)

type durationProbe func(audioPath string) (time.Duration, error)

func main() {
	if code := run(os.Args[1:], os.Stdout, os.Stderr, probeAudioDuration); code != 0 {
		os.Exit(code)
	}
}

func run(args []string, stdout, stderr io.Writer, probe durationProbe) int {
	flags := flag.NewFlagSet("verbatimcheck", flag.ContinueOnError)
	flags.SetOutput(stderr)
	sourcePath := flags.String("source", "", "path to the authored narration text")
	transcriptPath := flags.String("transcript", "", "path to a word-aligned WebVTT transcript")
	audioPath := flags.String("audio", "", "path to the synthesized audio (duration checked with ffprobe)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *sourcePath == "" || *transcriptPath == "" || *audioPath == "" || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: verbatimcheck -source narration.txt -transcript words.vtt -audio final_audio.m4a")
		return 2
	}
	source, err := os.ReadFile(*sourcePath)
	if err != nil {
		fmt.Fprintf(stderr, "read source: %v\n", err)
		return 2
	}
	transcript, err := os.Open(*transcriptPath)
	if err != nil {
		fmt.Fprintf(stderr, "open transcript: %v\n", err)
		return 2
	}
	cues, parseErr := verbatimcheck.ParseVTT(transcript)
	closeErr := transcript.Close()
	if parseErr != nil {
		fmt.Fprintf(stderr, "parse transcript: %v\n", parseErr)
		return 2
	}
	if closeErr != nil {
		fmt.Fprintf(stderr, "close transcript: %v\n", closeErr)
		return 2
	}
	duration, err := probe(*audioPath)
	if err != nil {
		fmt.Fprintf(stderr, "probe audio: %v\n", err)
		return 2
	}
	report := verbatimcheck.Check(string(source), cues, duration)
	if err := json.NewEncoder(stdout).Encode(report); err != nil {
		fmt.Fprintf(stderr, "write report: %v\n", err)
		return 2
	}
	fmt.Fprintln(stderr, report.Summary())
	if !report.OK() {
		return 1
	}
	return 0
}

// probeAudioDuration measures the audio through the canonical media probe port.
// percheck_duration_probe_ssot forbids a raw ffprobe/ffmpeg spawn outside
// internal/platform/media: the media engine owns media-binary execution and the
// kernel duration contract owns the value, so this tool asks the same prober the
// pipeline uses instead of shelling out on its own.
func probeAudioDuration(audioPath string) (time.Duration, error) {
	if strings.TrimSpace(audioPath) == "" {
		return 0, fmt.Errorf("audio path is empty")
	}
	probe := rustexec.NewVideoProcessor(
		envOrDefault("VELOX_RUST_MUSCLES_PATH", filepath.Join("bin", "pipelinegen-muscles")),
		envOrDefault("FFMPEG_PATH", "ffmpeg"),
		zap.NewNop(),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	info, err := probe.Probe(ctx, audioPath)
	if err != nil {
		return 0, fmt.Errorf("probe %q through the media engine: %w", audioPath, err)
	}
	if info == nil || info.Duration <= 0 {
		return 0, fmt.Errorf("media engine returned no positive duration for %q", audioPath)
	}
	return info.Duration, nil
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
