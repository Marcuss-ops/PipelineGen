// Package scan — percheck_queue_single_module.go enforces DEMOL-05: the Queue
// module has exactly one owner (`RenderingGen/queue`) and PipelineGen consumes
// it through a sibling path replacement.
//
// BACKGROUND. Until 2026-11-26 `refactored/third_party/queue/` was a
// byte-for-byte copy of `RenderingGen/queue`, kept in sync by
// `scripts/ci/check_queue_vendor.py` and two lock files. Every contract change
// (031_worker_capabilities, 032_render_job_execution_policy) had to be applied
// twice, and the copy was already carrying uncommitted drift when it was
// removed. A synchronization check is not a single source of truth.
//
// WHAT THIS GATE ASSERTS
//   - `third_party/queue/` does not exist;
//   - no `third_party/queue.sync.json` / `queue.compatibility.json` snapshot
//     lock reappears;
//   - `go.mod` replaces the Queue module with exactly `../RenderingGen/queue`;
//   - no other path (vendored or network) is used for that module.
//
// The replacement is required because the published repository name does not
// match the historical module path, so a versioned require cannot resolve it.
// A sibling checkout is the only resolvable form inside this workspace.
//
// matched rule_id: `percheck_queue_single_module`.
package governance

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

const queueSingleModuleRule = "percheck_queue_single_module"

const queueModulePath = "github.com/Marcuss-ops/RenderingGen/queue"

const queueSiblingReplace = "replace " + queueModulePath + " => ../RenderingGen/queue"

func queueSingleModuleViolation(r *report.Report, file, note string) {
	r.Violations = append(r.Violations, report.Violation{
		File:        file,
		MatchedRule: queueSingleModuleRule,
		Rule:        queueSingleModuleRule,
		Severity:    string(report.SeverityError),
		Note:        note,
	})
}

// ScanQueueSingleModule validates that the vendored Queue copy stays dead.
func ScanQueueSingleModule(root string, _ *policy.Policy, r *report.Report) {
	if _, err := os.Stat(filepath.Join(root, "third_party", "queue")); err == nil {
		queueSingleModuleViolation(r, "third_party/queue",
			"vendored Queue module must not exist; use the sibling RenderingGen/queue checkout")
	}
	for _, lock := range []string{"queue.sync.json", "queue.compatibility.json"} {
		path := filepath.Join(root, "third_party", lock)
		if _, err := os.Stat(path); err == nil {
			queueSingleModuleViolation(r, filepath.Join("third_party", lock),
				"Queue snapshot lock must not exist; it pins a second copy")
		}
	}

	goModPath := filepath.Join(root, "go.mod")
	data, err := os.ReadFile(goModPath)
	if err != nil {
		queueSingleModuleViolation(r, "go.mod", "cannot read go.mod: "+err.Error())
		return
	}

	foundCanonical := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		if !strings.HasPrefix(trimmed, "replace "+queueModulePath) {
			continue
		}
		if trimmed == queueSiblingReplace {
			foundCanonical = true
			continue
		}
		queueSingleModuleViolation(r, "go.mod",
			"Queue module replacement must be exactly "+queueSiblingReplace)
	}
	if !foundCanonical {
		queueSingleModuleViolation(r, "go.mod",
			"missing canonical replacement: "+queueSiblingReplace)
	}
}
