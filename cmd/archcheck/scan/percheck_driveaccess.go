// Package scan — Check 74: Drive access SSOT (Wave 5, July 2026).
//
// scan/percheck_driveaccess.go owns the forward-prevention gate for
// Drive access. After the Wave 3 Drive cutover, all production code
// paths MUST consume the typed delivery.Publisher port; direct use of
// the low-level drive.Uploader / drive.FolderManager concrete types
// bypasses the Publisher's destination registry, conflict policy, and
// idempotency logic.
//
// Allowlist:
//   - internal/infrastructure/drive/**              : the Drive infrastructure implementation.
//   - internal/app/**                               : composition-root wiring.
//   - internal/application/assets/delivery/**       : the application-layer delivery port.
//   - *_test.go                                     : tests may construct fakes directly.
//
// Pattern anchors:
//
//	drive\.NewUploader\(                             — direct uploader construction
//	drive\.NewFolderManager\(                       — direct folder manager construction
//	drive\.Uploader                                  — direct uploader type reference
//	drive\.FolderManager                             — direct folder manager type reference
//	\*drive\.Uploader                               — pointer to concrete uploader
//	\*drive\.FolderManager                          — pointer to concrete folder manager
package scan

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// driveAccessForbiddenPatterns lists the low-level Drive concrete
// references that are forbidden outside the Drive infrastructure
// package and composition root.
var driveAccessForbiddenPatterns = []struct {
	pattern string
	desc    string
}{
	{"drive.NewUploader(", "direct drive.NewUploader construction"},
	{"drive.NewFolderManager(", "direct drive.NewFolderManager construction"},
	{"drive.Uploader", "direct drive.Uploader type reference"},
	{"drive.FolderManager", "direct drive.FolderManager type reference"},
	{"*drive.Uploader", "pointer to concrete drive.Uploader"},
	{"*drive.FolderManager", "pointer to concrete drive.FolderManager"},
}

// ScanDriveAccessSSOT walks <root>/internal/application/** and
// <root>/internal/api/** for non-test .go files, scanning each line
// for low-level Drive concrete references. The Drive infrastructure
// package, the delivery port package, and the composition root are
// allowlisted.
func ScanDriveAccessSSOT(root string, pol *policy.Policy, r *report.Report) {
	skipDirs := map[string]bool{
		".git": true, "vendor": true, "node_modules": true,
		"node-scraper": true, "examples": true, "scripts": true,
	}

	allowPrefixes := []string{
		"internal/infrastructure/drive/",
		"internal/application/assets/delivery/",
		"internal/app/",
	}

	for _, subdir := range []string{"internal/application", "internal/api"} {
		dir := filepath.Join(root, subdir)
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

			for _, prefix := range allowPrefixes {
				if strings.HasPrefix(relSlash, prefix) {
					return nil
				}
			}

			scanDriveAccessFile(root, path, relSlash, r)
			return nil
		})
	}
}

func scanDriveAccessFile(root, path, relPath string, r *report.Report) {
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
		for _, p := range driveAccessForbiddenPatterns {
			if !strings.Contains(line, p.pattern) {
				continue
			}
			r.Violations = append(r.Violations, report.Violation{
				File:        relPath,
				Line:        lineNum,
				Rule:        "percheck_drive_access_ssot",
				Severity:    string(report.SeverityError),
				MatchedRule: "drive_access_ssot",
				Note:        "forbidden low-level Drive concrete reference: " + p.desc + " — route Drive access through delivery.Publisher (Pattern 0)",
			})
		}
	}
}
