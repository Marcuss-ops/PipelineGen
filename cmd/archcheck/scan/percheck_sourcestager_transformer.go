// Package scan — Check 77: SourceStager + MediaTransformer SSOT (Wave 5, July 2026).
//
// scan/percheck_sourcestager_transformer.go owns the forward-prevention
// gate for Wave 3 source staging and media transformation. All source
// media preparation MUST go through the SourceStager port, and all
// media transformations MUST go through the MediaTransformer port.
// Direct use of os/exec to run ffmpeg, yt-dlp, wget, or raw media file
// manipulation in application code bypasses the typed ports and makes the
// system harder to test and migrate.
//
// Allowlist:
//   - internal/capabilities/assets/providers/*/stager_adapter.go :
//     SourceStager adapter implementations.
//   - internal/infrastructure/media/** : infrastructure media adapters.
//   - internal/app/** : composition-root wiring.
//   - *_test.go : tests may invoke raw binaries for verification.
//
// Pattern anchors:
//
//	exec\.Command.*ffmpeg|yt-dlp|wget  — raw binary invocation
//	ffmpeg|yt-dlp|wget string literals in application code
package scan

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// sourceStagerAllowPatterns are path suffixes that are allowed to
// contain raw binary invocations because they implement the canonical
// SourceStager / MediaTransformer adapters.
var sourceStagerAllowPatterns = []string{
	"stager_adapter.go",
	"internal/infrastructure/media/",
	"internal/app/",
}

// sourceStagerForbiddenPatterns are the raw binary invocation patterns
// that are forbidden in application code outside the adapter allowlist.
var sourceStagerForbiddenPatterns = []struct {
	re   *regexp.Regexp
	desc string
}{
	{regexp.MustCompile(`exec\.(Command|CommandContext)[^"]*"(ffmpeg|yt-dlp|wget)`),
		"raw os/exec invocation of ffmpeg/yt-dlp/wget"},
	{regexp.MustCompile(`"(ffmpeg|yt-dlp|wget)"`),
		"literal reference to ffmpeg/yt-dlp/wget binary"},
}

// ScanSourceStagerTransformer walks <root>/internal/application/** for
// non-test .go files and flags raw binary invocations outside the
// canonical adapter allowlist.
func ScanSourceStagerTransformer(root string, pol *policy.Policy, r *report.Report) {
	skipDirs := map[string]bool{
		".git": true, "vendor": true, "node_modules": true,
		"node-scraper": true, "examples": true, "scripts": true,
	}

	dir := filepath.Join(root, "internal/application")
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[filepath.Base(path)] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		relSlash := filepath.ToSlash(rel)

		for _, p := range sourceStagerAllowPatterns {
			if strings.Contains(relSlash, p) {
				return nil
			}
		}

		scanSourceStagerFile(root, path, relSlash, r)
		return nil
	})
}

func scanSourceStagerFile(root, path, relPath string, r *report.Report) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	lineNum := 0
	for sc.Scan() {
		lineNum++
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		for _, p := range sourceStagerForbiddenPatterns {
			if !p.re.MatchString(line) {
				continue
			}
			r.Violations = append(r.Violations, report.Violation{
				File:        relPath,
				Line:        lineNum,
				Rule:        "percheck_sourcestager_transformer",
				Severity:    string(report.SeverityError),
				MatchedRule: "raw_source_media_or_transform",
				Note:        "raw source media / transformation reference: " + p.desc + " — route through SourceStager (internal/application/acquisition) or MediaTransformer (internal/kernel/asset/transformer) instead",
			})
		}
	}
}
