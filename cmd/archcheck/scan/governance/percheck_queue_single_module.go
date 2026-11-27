// Package scan — percheck_queue_single_module.go enforces DEMOL-05/RGA-02:
// the Queue module has exactly one owner and PipelineGen consumes the
// published, versioned module.
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
//   - `go.mod` requires the reviewed Queue module version;
//   - no local replace can mask an unavailable or mismatched publication.
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

const queueModuleVersion = "v0.1.0"
const queueCanonicalRequire = queueModulePath + " " + queueModuleVersion

func queueSingleModuleViolation(r *report.Report, file, note string) {
	r.Violations = append(r.Violations, report.Violation{
		File:        file,
		MatchedRule: queueSingleModuleRule,
		Rule:        queueSingleModuleRule,
		Severity:    string(report.SeverityError),
		Note:        note,
	})
}

// ScanQueueSingleModule validates the published Queue dependency and ensures
// that a sibling checkout or vendored copy cannot mask a broken release.
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
		if strings.HasPrefix(trimmed, "replace "+queueModulePath) {
			queueSingleModuleViolation(r, "go.mod",
				"Queue module must be consumed from the published version; local replacements are forbidden")
			continue
		}
		if strings.HasPrefix(trimmed, "require "+queueModulePath+" ") || strings.HasPrefix(trimmed, queueModulePath+" ") {
			if trimmed == "require "+queueCanonicalRequire || trimmed == queueCanonicalRequire {
				foundCanonical = true
			} else {
				queueSingleModuleViolation(r, "go.mod",
					"Queue dependency must be pinned to "+queueModuleVersion)
			}
		}
	}
	if !foundCanonical {
		queueSingleModuleViolation(r, "go.mod",
			"missing published Queue dependency: "+queueCanonicalRequire)
	}
}
