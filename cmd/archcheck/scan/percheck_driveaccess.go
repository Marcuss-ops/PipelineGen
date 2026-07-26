// Package scan — Check 74: Drive access SSOT (Wave 5, July 2026 + PR-DRIVE-CLEANUP July 2026).
//
// scan/percheck_driveaccess.go owns the forward-prevention gate for
// Drive access. After the Wave 3 Drive cutover, all production code
// paths MUST consume the typed delivery.Publisher port; direct use of
// the low-level drive.Uploader / drive.Admin / drive.FolderManager
// concrete types bypasses the Publisher's destination registry,
// conflict policy, and idempotency logic.
//
// PR-DRIVE-CLEANUP (July 2026) extension: drive.Admin is added to
// the forbidden set alongside drive.Uploader. The legacy Admin
// (folder-CRUD + trash/delete/rename) is fully superseded by
// drive.FileLifecycle (rename/trash/delete) + delivery.Publisher
// (upload). Application callers MUST NOT import drive.Admin
// directly; the canonical surface is delivery.Publisher for writes
// and drive.FileLifecycle for lifecycle ops, both wired exclusively
// at the composition root (internal/app/).
//
// Allowlist:
//   - internal/infrastructure/drive/**              : the Drive infrastructure implementation.
//   - internal/app/**                               : composition-root wiring.
//   - internal/application/assets/delivery/**       : the application-layer delivery port.
//   - *_test.go                                     : tests may construct fakes directly.
//   - cmd/archcheck/scan/**                         : the scanner's own package is exempt
//     (out of the walk scope: ScanDriveAccessSSOT
//     only walks internal/application/** +
//     internal/api/**).
//
// Pattern anchors:
//
//	drive\.NewUploader\(                           — direct uploader construction
//	drive\.NewFolderManager\(                      — direct folder manager construction
//	drive\.NewDriveServiceFromFiles\(              — direct admin constructor (PR-DRIVE-CLEANUP)
//	drive\.NewFileLifecycleAdapter\(               — direct FileLifecycle-port constructor
//	drive\.Uploader                                — direct uploader type reference
//	drive\.FolderManager                           — direct folder manager type reference
//	drive\.Admin                                   — direct admin type reference (PR-DRIVE-CLEANUP)
//	\*drive\.Uploader                              — pointer to concrete uploader
//	\*drive\.FolderManager                         — pointer to concrete folder manager
//	\*drive\.Admin                                 — pointer to concrete admin (PR-DRIVE-CLEANUP)
//	uploaddrive\.Uploader                          — aliased import (catalogsync, July 2026)
//	uploaddrive\.Admin                             — aliased import (catalogsync, July 2026)
//	\*uploaddrive\.Uploader                        — pointer to aliased uploader
//	\*uploaddrive\.Admin                           — pointer to aliased admin
package scan

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// driveAccessPattern is the per-row schema of the forbidden-pattern
// matrix. `allowInPath` is reserved for explicit composition-root
// exceptions; application packages have no concrete Drive exemption.
type driveAccessPattern struct {
	pattern        string
	desc           string
	allowInPath    string // empty = no allowlist override (always forbid)
	allowRationale string // documents the forward-pointer
}

// driveAccessForbiddenPatterns lists the low-level Drive concrete
// references that are forbidden outside the Drive infrastructure
// package and composition root.
//
// PR-DRIVE-CLEANUP (July 2026): drive.Admin + *drive.Admin +
// drive.NewDriveServiceFromFiles( + drive.NewFileLifecycleAdapter(
// added to the matrix alongside the pre-existing drive.Uploader +
// drive.FolderManager bans.
//
// Constructor patterns (drive.NewUploader( + drive.NewFolderManager(
// + drive.NewDriveServiceFromFiles( + drive.NewFileLifecycleAdapter()
// + drive.FolderManager + *drive.FolderManager) have NO substring
// overlap with `uploaddrive.*` (the alias preserves the constructor
// name's `.` boundary differently — `uploaddrive.New<X>(` does not
// contain `drive.New<X>(` as a contiguous substring because the
// alias adds the `.upload` prefix), so the global file-level
// allowlist in ScanDriveAccessSSOT is sufficient — no per-pattern
// allowInPath is needed for these rows.
var driveAccessForbiddenPatterns = []driveAccessPattern{
	// drive.Uploader + drive.FolderManager type-reference surface
	// (catalogsync-allowlist attached to the substring-overlap subset
	// `drive.Uploader` + `*drive.Uploader`; `drive.FolderManager` +
	// `*drive.FolderManager` have no overlap and rely on the global
	// file-level allowlist).
	{"drive.NewUploader(", "direct drive.NewUploader construction", "", ""},
	{"drive.NewFolderManager(", "direct drive.NewFolderManager construction", "", ""},
	{"drive.Uploader", "direct drive.Uploader type reference", "", ""},
	{"drive.FolderManager", "direct drive.FolderManager type reference", "", ""},
	{"*drive.Uploader", "pointer to concrete drive.Uploader", "", ""},
	{"*drive.FolderManager", "pointer to concrete drive.FolderManager", "", ""},

	// PR-DRIVE-CLEANUP additions: drive.Admin surface + direct
	// constructors (catalogsync-allowlist mirrors the drive.Uploader
	// strategy: drive.Admin + *drive.Admin carry the prefix; the
	// drive.New<...> constructors don't substring-overlap with
	// uploaddrive.* and rely on the global file-level allowlist).
	{"drive.NewDriveServiceFromFiles(", "direct drive.NewDriveServiceFromFiles construction (creates *drive.Uploader with Admin methods)", "", ""},
	{"drive.NewFileLifecycleAdapter(", "direct drive.NewFileLifecycleAdapter construction (composition root wraps this; outside root, route through internal/app/)", "", ""},
	{"drive.Admin", "direct drive.Admin type reference", "", ""},
	{"*drive.Admin", "pointer to concrete drive.Admin", "", ""},

	// PR-DRIVE-CLEANUP aliased-import coverage: uploaddrive alias
	// (used by catalogsync subscriber; forward-pointer to the
	// typed-port migration ticket). Catalogsync-allowlist repeats
	// on these rows so the loadbearing surface is uniform across
	// the matrix; the general `drive.*` patterns above already
	// carry the same prefix so this rowset is belt-and-braces.
	{"uploaddrive.Uploader", "aliased drive.Uploader type reference", "", ""},
	{"uploaddrive.Admin", "aliased drive.Admin type reference", "", ""},
	{"*uploaddrive.Uploader", "pointer to aliased drive.Uploader", "", ""},
	{"*uploaddrive.Admin", "pointer to aliased drive.Admin", "", ""},
}

// ScanDriveAccessSSOT walks <root>/internal/application/** and
// <root>/internal/api/** for non-test .go files, scanning each line
// for low-level Drive concrete references. The Drive infrastructure
// package, the delivery port package, and the composition root are
// globally allowlisted; application packages have no concrete Drive
// exemption.
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
		raw := sc.Text()
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		for _, p := range driveAccessForbiddenPatterns {
			if !strings.Contains(raw, p.pattern) {
				continue
			}
			// Forward-pointer allowlist (PR-DRIVE-CLEANUP, July 2026):
			// a small set of patterns carries a scoped allowInPath that
			// documents the catalogsync subscriber's pending typed-port
			// migration (uploaddrive.* aliased imports). The pattern is
			// only flagged if the file is OUTSIDE the allowInPath prefix.
			// The forward-pointer itself lives in
			// architecture/deprecations.yaml (DRIVE-ADMIN-TO-FILELIFECYCLE-
			// MIGRATION + new CATALOGSYNC-DRIVE-PORT-MIGRATION entry)
			// per godlike/07 audit-pin convention; the scanner stays
			// minimal per AGENTS.md §Simplicity & Minimalism.
			if p.allowInPath != "" && strings.HasPrefix(relPath, p.allowInPath) {
				continue
			}
			r.Violations = append(r.Violations, report.Violation{
				File:        relPath,
				Line:        lineNum,
				Rule:        "percheck_drive_access_ssot",
				Severity:    string(report.SeverityError),
				MatchedRule: "drive_access_ssot",
				Note:        "forbidden low-level Drive concrete reference: " + p.desc + " — route Drive access through delivery.Publisher (Pattern 0) or, for lifecycle ops, through drive.FileLifecycle wired at composition root (internal/app/)",
			})
		}
	}
}
