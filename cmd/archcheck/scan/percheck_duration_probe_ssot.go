// Package scan — percheck_duration_probe_ssot.go
//
// Forward-prevention gate: production Go code MUST NOT spawn ffprobe or
// ffmpeg directly. The canonical media capability
// (internal/infrastructure/media/rustexec + the render probe adapter) is the
// single owner of media-binary execution; every other package measures an
// asset's total duration through the canonical probe port
// (rustexec.VideoProcessor.Probe) and the kernel duration contract
// (internal/kernel/asset.ResolveAssetDuration).
//
// Exempt zones:
//   - **/*_test.go — regression-guard surface may assert fixtures.
//   - cmd/archcheck/scan — the scanner itself.
//   - internal/infrastructure/media — the canonical media capability.
//
// Matched rule_id: percheck_duration_probe_ssot
package scan

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

const durationProbeSSOTRule = "percheck_duration_probe_ssot"

const durationProbeSSOTNote = "forbidden direct ffprobe/ffmpeg process spawn outside the canonical media probe capability (internal/infrastructure/media/). Duration measurement MUST go through the canonical probe port (rustexec.VideoProcessor.Probe) and the kernel duration contract (internal/kernel/asset.ResolveAssetDuration); never a raw ffprobe/ffmpeg subprocess."

var durationProbeSSOTSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
}

var durationProbeSSOTSkipPathPrefixes = []string{
	"cmd/archcheck/scan",
}

var durationProbeSSOTExemptPathPrefixes = []string{
	"internal/infrastructure/media",
}

// durationProbeSSOTMatch reports whether a single non-comment line spawns a
// raw ffprobe/ffmpeg process: an exec.Command(Context) call whose argument
// list carries the literal binary name.
func durationProbeSSOTMatch(line string) bool {
	lower := strings.ToLower(line)
	if !strings.Contains(lower, "exec.command") {
		return false
	}
	return strings.Contains(lower, `"ffprobe"`) || strings.Contains(lower, `"ffmpeg"`)
}

// ScanDurationProbeSSOT walks every .go file under the repo root and emits a
// violation for any production file that spawns ffprobe/ffmpeg outside the
// canonical media capability.
func ScanDurationProbeSSOT(root string, pol *policy.Policy, r *report.Report) {
	_ = pol // reserved for future SeverityOverride plumbing

	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if durationProbeSSOTSkipDirs[base] {
				return filepath.SkipDir
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr == nil {
				if hasAnyPathPrefix(filepath.ToSlash(rel), durationProbeSSOTSkipPathPrefixes) {
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
		if hasAnyPathPrefix(relSlash, durationProbeSSOTExemptPathPrefixes) {
			return nil
		}
		scanDurationProbeSSOTFile(path, relSlash, r)
		return nil
	})
}

func scanDurationProbeSSOTFile(path, relPath string, r *report.Report) {
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
		if !durationProbeSSOTMatch(line) {
			continue
		}
		r.Violations = append(r.Violations, report.Violation{
			Package:     pkgFromDurationProbeSSOTRel(relPath),
			File:        relPath,
			Line:        lineNo,
			Rule:        durationProbeSSOTRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "duration_probe_ssot_gate",
			Note:        durationProbeSSOTNote + " | snippet: " + truncateDurationProbeSSOT(line),
		})
	}
}

func truncateDurationProbeSSOT(s string) string {
	const maxLen = 120
	const marker = " <<<"
	if len(s) > maxLen {
		return s[:maxLen] + marker
	}
	return s
}

func pkgFromDurationProbeSSOTRel(rel string) string {
	dir := filepath.Dir(rel)
	if dir == "." || dir == "" {
		return "."
	}
	return filepath.ToSlash(dir)
}
