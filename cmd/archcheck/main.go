// Package main — archcheck (Phase 0, target-tree governance).
//
// Reads `architecture/policy.yaml` (flat key:value, stdlib-parseable — see
// comments in that file), walks the project tree, and emits a JSON
// violation report to stdout. Phase 0 is report-only (exits 0 even when
// violations are present) so existing CI is undisturbed while the policy
// and tooling stabilise. Phase N promotes the gate by passing `--strict`.
//
// Scope: this binary is the **target-tree half** of PipelineGen's
// architectural gating. The **legacy burndown** half lives in
// `scripts/archcheck/` — a regex-heavy (`rg`) transitional ratchet that
// enforces the monotone-decreasing baselines for legacy surface area
// (`database/sql` in api/application/domain, interface{} growth,
// dependency setters, fake 501 routes, Python legacy writers, ...).
// The two binaries are KEEP_BOTH by design (June 2026 Wave 16 PR
// "scripts/archcheck dead-code sweep" decision recorded in
// architecture/current.yaml):
//
//   - cmd/archcheck is **permanent**: it enforces the target tree
//     (kernel/capability/platform subzones, max files per package,
//     forbidden top-level dirs, legacy→target internal roots, ownership
//     rule family) indefinitely.
//   - scripts/archcheck is **transient**: each rule family expires once
//     it reaches `verified_zero: true` in current.yaml; the binary
//     lives until the last legacy family is burned down.
//
// They are NOT overlapping: same name, complementary concerns. The
// shared JSON contract (`passed`, `mode`, `checks`, `violations`) is
// kept structurally identical so downstream dashboards can consume
// BOTH outputs without per-binary plumbing.
//
// Stdlib only — no gopkg.in/yaml.v3 import. The flat policy format is
// documented in architecture/policy.yaml and intentionally simple
// (one key per line, comma-separated lists). The complexity gate is the
// policy file, not the parser.
//
// Exit codes:
//
//	0 — report printed (default; --strict off). Phase 0 mode.
//	1 — violations present while --strict (Phase N mode).
//	2 — load/walk/marshal error.
//
// FASE 1.C (June 2026) layout: main.go is the CLI dispatch + composition
// root only. The data model + parser live in `policy/{model,load}.go`
// and `report/model.go` (separate Go packages). The rule-family
// scanners live in `scan/{packages,roots,documents,constructors}.go`.
// PR4 will extract the orchestration into `runner.go` and trim this
// file to ≤100 LOC.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/scan"
)

func main() {
	var (
		root   = flag.String("root", ".", "Project root to scan (default: cwd)")
		polstr = flag.String("policy", "architecture/policy.yaml", "Path to policy YAML")
		strict = flag.Bool("strict", false, "Phase N gate: exit 1 if any violations present")
		phase  = flag.String("phase", "0", "Phase label (printed in the report)")
	)
	flag.Parse()

	pol, err := policy.Load(*polstr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "archcheck: load policy %q: %v\n", *polstr, err)
		os.Exit(2)
	}

	r := &report.Report{
		Mode:       "target-tree-dry-run",
		PolicyPath: *polstr,
		Root:       *root,
		Phase:      *phase,
		Policy:     pol,
		Summary:    report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}},
	}

	// Constructor-deps scanner (FASE 1.C PR3 — extracted from main.go
	// into cmd/archcheck/scan/constructors.go as ScanConstructors).
	// Runs FIRST because it walks internal/ only; the rules below
	// walk the full tree and would re-traverse internal/ anyway if
	// ordering flipped.
	scan.ScanConstructors(*root, pol, r)

	// Rule-family scanners (PR2 split). Each scan.Scan* function appends
	// report.Violation entries to r; the orchestration order is preserved
	// from the pre-PR2 inline layout (constructor deps first because they
	// look at internal/ only; roots + documents next; file size / pkg
	// size / thin_command last because they share the fileLines map
	// populated by scan.ScanPackages).
	scan.ScanForbiddenDirs(*root, pol, r)
	scan.ScanKernelSubzoneHints(*root, pol, r)
	scan.ScanUnknownInternalRoots(*root, pol, r)
	scan.ScanOwnershipDoc(*root, pol, r)
	scan.ScanLegacyPolicyDoc(*root, pol, r)
	scan.ScanCIGatesDoc(*root, pol, r)
	scan.ScanAgentPlaybookDoc(*root, pol, r)
	scan.ScanRemovalDoc(*root, pol, r)
	scan.ScanStaleProsePaths(*root, pol, r)

	fileLines := map[string]int{}
	scan.ScanPackages(*root, pol, r, fileLines)
	scan.ScanCommandBinaries(*root, pol, r, fileLines)

	r.Summary.TotalViolations = len(r.Violations)
	for _, v := range r.Violations {
		r.Summary.ByReason[v.Rule]++
		r.Summary.BySeverity[v.Severity]++
	}
	r.Passed = len(r.Violations) == 0

	out, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "archcheck: marshal report: %v\n", err)
		os.Exit(2)
	}
	fmt.Println(string(out))

	if *strict && len(r.Violations) > 0 {
		os.Exit(1)
	}
}
