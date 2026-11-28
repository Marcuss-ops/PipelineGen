// Command verbatimcheck compares a source_text_verbatim narration with a
// word-aligned WebVTT transcript and the corresponding audio duration.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/verbatimcheck"
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

func probeAudioDuration(audioPath string) (time.Duration, error) {
	if strings.TrimSpace(audioPath) == "" {
		return 0, fmt.Errorf("audio path is empty")
	}
	output, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", audioPath).CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("ffprobe %q: %w: %s", audioPath, err, strings.TrimSpace(string(output)))
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
	if err != nil || seconds <= 0 {
		if err == nil {
			err = fmt.Errorf("duration must be positive")
		}
		return 0, fmt.Errorf("invalid ffprobe duration %q: %w", strings.TrimSpace(string(output)), err)
	}
	return time.Duration(seconds * float64(time.Second)), nil
}
