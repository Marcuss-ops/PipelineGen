// Package scan — percheck_speech_timing_ssot.go
//
// Forward-prevention gate: production Go code MUST NOT construct
// SpeechTimingArtifact struct literals directly. The canonical builder is
// internal/capabilities/audio/speech_artifact.go::BuildSpeechTimingArtifact
// (it assembles AND validates the provider-neutral word-boundary contract).
// Any other package constructs the artifact only via that builder; consumers
// treat the artifact as a read-only projection.
//
// Exempt zones:
//   - internal/capabilities/audio — the canonical SSOT owner.
//   - **/*_test.go — regression-guard fixtures may build literals.
//   - cmd/archcheck/scan — the scanner itself.
//
// Matched rule_id: percheck_speech_timing_ssot
package scan

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

const speechTimingSSOTRule = "percheck_speech_timing_ssot"

const speechTimingSSOTNote = "forbidden direct SpeechTimingArtifact struct-literal construction outside the canonical builder (internal/capabilities/audio/speech_artifact.go::BuildSpeechTimingArtifact). Consumers must treat the artifact as a read-only projection and build it only through the canonical validated constructor."

const speechTimingSSOTLiteral = "SpeechTimingArtifact{"

var speechTimingSSOTSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
}

var speechTimingSSOTSkipPathPrefixes = []string{
	"cmd/archcheck/scan",
}

var speechTimingSSOTExemptPathPrefixes = []string{
	"internal/capabilities/audio",
}

// ScanSpeechTimingSSOT walks every .go file under the repo root and emits a
// violation for any production file that constructs a SpeechTimingArtifact
// literal outside the canonical audio capability.
func ScanSpeechTimingSSOT(root string, pol *policy.Policy, r *report.Report) {
	_ = pol

	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if speechTimingSSOTSkipDirs[base] {
				return filepath.SkipDir
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr == nil {
				if hasAnyPathPrefix(filepath.ToSlash(rel), speechTimingSSOTSkipPathPrefixes) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if strings.HasSuffix(relSlash, "_test.go") {
			return nil
		}
		if hasAnyPathPrefix(relSlash, speechTimingSSOTExemptPathPrefixes) {
			return nil
		}
		scanSpeechTimingSSOTFile(path, relSlash, r)
		return nil
	})
}

func scanSpeechTimingSSOTFile(path, relPath string, r *report.Report) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
			continue
		}
		if !strings.Contains(line, speechTimingSSOTLiteral) {
			continue
		}
		r.Violations = append(r.Violations, report.Violation{
			Package:     pkgFromSpeechTimingSSOTRel(relPath),
			File:        relPath,
			Line:        lineNo,
			Rule:        speechTimingSSOTRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "speech_timing_ssot_gate",
			Note:        speechTimingSSOTNote + " | snippet: " + truncateSpeechTimingSSOT(line),
		})
	}
}

func truncateSpeechTimingSSOT(s string) string {
	const maxLen = 120
	const marker = " <<<"
	if len(s) > maxLen {
		return s[:maxLen] + marker
	}
	return s
}

func pkgFromSpeechTimingSSOTRel(rel string) string {
	dir := filepath.Dir(rel)
	if dir == "." || dir == "" {
		return "."
	}
	return filepath.ToSlash(dir)
}
