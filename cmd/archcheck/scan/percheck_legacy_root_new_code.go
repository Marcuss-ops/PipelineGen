// Package scan — forward-prevention gate for migration-only internal roots.
//
// The target layout is internal/{app,kernel,capabilities,platform}. Existing
// application/api/infrastructure/domain roots are migration-only and must not
// receive new production structure. This scanner checks added files in the
// commit range under review and the current working diff; edits to existing
// legacy files remain possible for migration, removal, and correctness/security
// fixes.
package scan

import (
	"bufio"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

const legacyRootNewCodeRule = "percheck_legacy_root_new_code"

// ScanLegacyRootNewCode rejects files newly added under a migration-only
// internal root. Git's added-file view is used intentionally: it catches new
// files once they are part of the commit/working diff without treating
// ordinary migration edits to already-owned files as new architecture.
func ScanLegacyRootNewCode(root string, pol *policy.Policy, r *report.Report) {
	added, err := addedFilesFromGit(root)
	if err != nil {
		// A non-Git fixture or archive has no diff to inspect. The structural
		// root scanner remains responsible for unmanaged roots; do not turn
		// absence of Git metadata into fake evidence of a violation.
		return
	}
	for _, rel := range added {
		// Tests are regression guards rather than production architecture;
		// existing legacy packages may add tests while they are migrated.
		if strings.HasSuffix(rel, "_test.go") {
			continue
		}
		legacyRoot := legacyRootForPath(rel, pol.LegacyInternalRoots)
		if legacyRoot == "" {
			continue
		}
		r.Violations = append(r.Violations, report.Violation{
			File:        rel,
			MatchedRule: "new_file_in_legacy_root_" + legacyRoot,
			Rule:        legacyRootNewCodeRule,
			Severity:    string(report.SeverityError),
			Note:        "new production files and packages are forbidden in migration-only internal/" + legacyRoot + "; move new capabilities, public contracts, providers and routes to a target root and keep this legacy root limited to migration, removal, correctness/security fixes, and regression tests",
		})
	}
}

func addedFilesFromGit(root string) ([]string, error) {
	paths := make(map[string]struct{})

	// During pre-push, HEAD already contains the commit being checked. Use
	// the upstream merge base so that newly committed legacy files remain
	// visible. In isolated repositories/tests, fall back to HEAD^.
	base, err := gitReviewBase(root)
	if err == nil {
		if err := collectAddedPaths(root, base, "HEAD", paths); err != nil {
			return nil, err
		}
	}
	// Also inspect staged and unstaged additions that have not reached HEAD.
	if err := collectAddedPaths(root, "HEAD", "", paths); err != nil {
		return nil, err
	}

	outPaths := make([]string, 0, len(paths))
	for path := range paths {
		outPaths = append(outPaths, path)
	}
	sort.Strings(outPaths)
	return outPaths, nil
}

func gitReviewBase(root string) (string, error) {
	upstream := exec.Command("git", "-C", root, "rev-parse", "--verify", "@{upstream}")
	if out, err := upstream.Output(); err == nil {
		mergeBase := exec.Command("git", "-C", root, "merge-base", "HEAD", strings.TrimSpace(string(out)))
		if base, err := mergeBase.Output(); err == nil {
			return strings.TrimSpace(string(base)), nil
		}
	}
	parent := exec.Command("git", "-C", root, "rev-parse", "--verify", "HEAD^")
	out, err := parent.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func collectAddedPaths(root, base, head string, paths map[string]struct{}) error {
	args := []string{"-C", root, "diff", "--name-only", "--diff-filter=ACR", base}
	if head != "" {
		args = append(args, head)
	}
	args = append(args, "--", "internal")
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return err
	}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		if rel := filepath.ToSlash(strings.TrimSpace(scanner.Text())); rel != "" {
			paths[rel] = struct{}{}
		}
	}
	return scanner.Err()
}

func legacyRootForPath(path string, roots []string) string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	for _, root := range roots {
		root = strings.Trim(strings.TrimSpace(root), "/")
		if path == "internal/"+root || strings.HasPrefix(path, "internal/"+root+"/") {
			return root
		}
	}
	return ""
}
