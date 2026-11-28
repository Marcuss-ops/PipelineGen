package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/verbatimcheck"
)

func TestRunWritesPassingReport(t *testing.T) {
	code, stdout, stderr := runFixture(t, "Corrado Augias racconta.")
	if code != 0 {
		t.Fatalf("run code = %d, stderr=%q", code, stderr)
	}
	var report verbatimcheck.Report
	if err := json.Unmarshal(stdout, &report); err != nil {
		t.Fatalf("decode report %q: %v", stdout, err)
	}
	if !report.OK() || report.ExpectedWords != 3 || report.SpokenWords != 3 {
		t.Fatalf("report = %+v, want all three authored words verified", report)
	}
}

func TestRunReturnsFailureForTranscriptIssue(t *testing.T) {
	code, stdout, stderr := runFixture(t, "**Corrado** Augias racconta.")
	if code != 1 {
		t.Fatalf("run code = %d, want 1; stderr=%q stdout=%q", code, stderr, stdout)
	}
	var report verbatimcheck.Report
	if err := json.Unmarshal(stdout, &report); err != nil {
		t.Fatalf("decode report %q: %v", stdout, err)
	}
	if report.OK() || len(report.Issues) == 0 {
		t.Fatalf("report = %+v, want the marker issue reported", report)
	}
}

func runFixture(t *testing.T, transcriptText string) (int, []byte, []byte) {
	t.Helper()
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.txt")
	transcriptPath := filepath.Join(dir, "transcript.vtt")
	if err := os.WriteFile(sourcePath, []byte("**Corrado** Augias racconta."), 0o600); err != nil {
		t.Fatal(err)
	}
	transcript := "WEBVTT\n\n00:00:00.000 --> 00:00:01.000\n" + transcriptText + "\n"
	if err := os.WriteFile(transcriptPath, []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	probe := func(string) (time.Duration, error) { return time.Second, nil }
	code := run([]string{"-source", sourcePath, "-transcript", transcriptPath, "-audio", "unused.m4a"}, &stdout, &stderr, probe)
	return code, stdout.Bytes(), stderr.Bytes()
}
