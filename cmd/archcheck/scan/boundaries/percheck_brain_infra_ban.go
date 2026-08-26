// Package scan — percheck_brain_infra_ban.go
//
// Forward-prevention gate for the Brain capability (and its
// mediamemory consumer). Brain code MUST NOT import infrastructure
// packages (qdrant, SQLite, Drive) or spawn FFmpeg/yt-dlp processes.
// The brain is the decision layer; the nervous system (infra) executes.
//
// Scope:
//   - internal/capabilities/brain/**
//   - internal/capabilities/mediamemory/**
//
// Exempt:
//   - _test.go files
//   - cmd/admin/** (operator tooling)
//   - comment-only references (warn, not violate)
//
// Banned surfaces:
//   - imports of internal/platform/qdrant
//   - imports of internal/platform/sqlite
//   - imports of internal/platform/drive
//   - imports of concrete external providers (Artlist/YouTube)
//   - literal calls: qdrant.NewClient, db.Exec, repo.Upsert,
//     exec.Command("ffmpeg"), exec.Command("yt-dlp")
//
// This scanner is text-based; it may flag banned strings that appear
// inside comments or string literals. Operators can move prose/log
// messages if needed, but the gate deliberately errs on the side of
// catching accidental infrastructure coupling.
package boundaries

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

const (
	brainInfraBanRule = "percheck_brain_infra_ban"

	brainScopePrefix       = "internal/capabilities/brain/"
	mediaMemoryScopePrefix = "internal/capabilities/mediamemory/"
)

var (
	brainInfraBannedImportPrefixes = []string{
		"\"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant",
		"\"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite",
		"\"github.com/Marcuss-ops/PipelineGen/internal/platform/drive",
		"\"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/artlist",
		"\"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/youtube",
		"\"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/artlist",
		"\"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/youtube",
	}
)

// brainInfraBannedExecRe catches exec.Command("ffmpeg"/"yt-dlp")
// invocations with flexible spacing/quoting. It is used as a fallback
// by detectBannedCall when the stricter literal list misses a variant.
var brainInfraBannedExecRe = regexp.MustCompile(
	`(?i)exec\.Command\s*\(\s*"(ffmpeg|yt-dlp)"`,
)

// ScanBrainInfraBan walks the brain and mediamemory application zones
// and reports any direct use of banned infrastructure imports or
// process-spawning literals.
func ScanBrainInfraBan(root string, pol *policy.Policy, r *report.Report) {
	_ = pol // reserved for future severity override

	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if base == ".git" || base == "vendor" || base == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if !strings.HasPrefix(relSlash, brainScopePrefix) &&
			!strings.HasPrefix(relSlash, mediaMemoryScopePrefix) {
			return nil
		}
		if strings.HasSuffix(relSlash, "_test.go") {
			return nil
		}
		if strings.HasPrefix(relSlash, "cmd/admin/") {
			return nil
		}
		// Adapters are deliberate infrastructure bridges; the brain
		// application layer itself (non-adapters) must remain clean.
		if strings.Contains(relSlash, "/adapters/") {
			return nil
		}
		scanBrainInfraBanFile(path, relSlash, r)
		return nil
	})
}

func scanBrainInfraBanFile(path, relPath string, r *report.Report) {
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

		if strings.HasPrefix(trimmed, "//") ||
			strings.HasPrefix(trimmed, "/*") ||
			strings.HasPrefix(trimmed, "*") {
			if containsAnyBannedImport(line) || detectBannedCall(line) != "" {
				brainInfraBanWarn(r, relPath, lineNo, "comment-only reference to banned surface")
			}
			continue
		}

		if bannedImport := detectBannedImport(line); bannedImport != "" {
			r.Violations = append(r.Violations, report.Violation{
				Package:     pkgFromBrainInfraRel(relPath),
				File:        relPath,
				Line:        lineNo,
				Rule:        brainInfraBanRule,
				Severity:    string(report.SeverityError),
				MatchedRule: "brain_infra_import_attempt",
				Note:        "forbidden infrastructure import in brain code: " + bannedImport + " (brain must not know Qdrant/SQLite/Drive/FFmpeg/yt-dlp)",
			})
			continue
		}

		if bannedCall := detectBannedCall(line); bannedCall != "" {
			r.Violations = append(r.Violations, report.Violation{
				Package:     pkgFromBrainInfraRel(relPath),
				File:        relPath,
				Line:        lineNo,
				Rule:        brainInfraBanRule,
				Severity:    string(report.SeverityError),
				MatchedRule: "brain_infra_banned_call",
				Note:        "forbidden call in brain code: " + bannedCall + " (brain must not execute infrastructure operations; nervous system does)",
			})
		}
	}
}

func detectBannedImport(line string) string {
	for _, prefix := range brainInfraBannedImportPrefixes {
		if strings.Contains(line, prefix) {
			return prefix
		}
	}
	return ""
}

func containsAnyBannedImport(line string) bool {
	return detectBannedImport(line) != ""
}

// bannedCallPatterns lists literal call forms that brain code must not contain.
// Keep them specific enough to avoid false positives in legitimate domain logic.
var bannedCallPatterns = []string{
	"qdrant.NewClient",
	"db.Exec(",
	"repo.Upsert(",
	"exec.Command(\"ffmpeg\",",
	"exec.Command(\"yt-dlp\",",
	"exec.Command(\"FFmpeg\",",
}

func detectBannedCall(line string) string {
	lower := strings.ToLower(line)
	for _, p := range bannedCallPatterns {
		if strings.Contains(lower, strings.ToLower(p)) {
			return p
		}
	}
	// Also catch case-insensitive exec.Command("ffmpeg"/"yt-dlp") with
	// different spacing/quoting, in case the literal list above misses it.
	if brainInfraBannedExecRe.MatchString(line) {
		return "exec.Command(\"ffmpeg\" / \"yt-dlp\")"
	}
	return ""
}

func brainInfraBanWarn(r *report.Report, relPath string, line int, msg string) {
	r.Warnings = append(r.Warnings, brainInfraBanRule+": comment-only reference in "+relPath+":"+strconv.Itoa(line)+" — "+msg)
}

func pkgFromBrainInfraRel(rel string) string {
	dir := filepath.Dir(rel)
	if dir == "." || dir == "" {
		return "."
	}
	return filepath.ToSlash(dir)
}
