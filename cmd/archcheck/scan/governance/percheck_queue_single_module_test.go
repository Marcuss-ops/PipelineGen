// Package scan — pins the DEMOL-05 single Queue module gate.
package governance

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func TestScanQueueSingleModule_PublishedVersionPasses(t *testing.T) {
	root := t.TempDir()
	writeDeprecationFile(t, root, "go.mod",
		"module example.test/pipeline\n\ngo 1.26.0\n\n"+
			"require github.com/Marcuss-ops/RenderingGen/queue v0.1.0\n")

	r := &report.Report{}
	ScanQueueSingleModule(root, &policy.Policy{}, r)

	if len(r.Violations) != 0 {
		t.Fatalf("expected no violations, got %+v", r.Violations)
	}
}

func TestScanQueueSingleModule_VendoredCopyViolates(t *testing.T) {
	root := t.TempDir()
	writeDeprecationFile(t, root, "go.mod",
		"module example.test/pipeline\n\n"+
			"require github.com/Marcuss-ops/RenderingGen/queue v0.1.0\n")
	writeDeprecationFile(t, root, "third_party/queue/go.mod", "module queue\n")

	r := &report.Report{}
	ScanQueueSingleModule(root, &policy.Policy{}, r)

	if !strings.Contains(violationNotes(r), "vendored Queue module must not exist") {
		t.Fatalf("expected vendored-copy violation, got %+v", r.Violations)
	}
}

func TestScanQueueSingleModule_SnapshotLockViolates(t *testing.T) {
	root := t.TempDir()
	writeDeprecationFile(t, root, "go.mod",
		"module example.test/pipeline\n\n"+
			"require github.com/Marcuss-ops/RenderingGen/queue v0.1.0\n")
	writeDeprecationFile(t, root, "third_party/queue.sync.json", "{}")

	r := &report.Report{}
	ScanQueueSingleModule(root, &policy.Policy{}, r)

	if !strings.Contains(violationNotes(r), "snapshot lock must not exist") {
		t.Fatalf("expected snapshot-lock violation, got %+v", r.Violations)
	}
}

func TestScanQueueSingleModule_LocalReplacementViolates(t *testing.T) {
	root := t.TempDir()
	writeDeprecationFile(t, root, "go.mod",
		"module example.test/pipeline\n\n"+
			"replace github.com/Marcuss-ops/RenderingGen/queue => ./third_party/queue\n")

	r := &report.Report{}
	ScanQueueSingleModule(root, &policy.Policy{}, r)

	if !strings.Contains(violationNotes(r), "local replacements are forbidden") {
		t.Fatalf("expected local replacement violation, got %+v", r.Violations)
	}
}

func TestScanQueueSingleModule_MissingPublishedDependencyViolates(t *testing.T) {
	root := t.TempDir()
	writeDeprecationFile(t, root, "go.mod", "module example.test/pipeline\n")

	r := &report.Report{}
	ScanQueueSingleModule(root, &policy.Policy{}, r)

	if !strings.Contains(violationNotes(r), "missing published Queue dependency") {
		t.Fatalf("expected missing dependency violation, got %+v", r.Violations)
	}
}

func TestScanQueueSingleModule_UnpinnedVersionViolates(t *testing.T) {
	root := t.TempDir()
	writeDeprecationFile(t, root, "go.mod", "module example.test/pipeline\n\n"+
		"require github.com/Marcuss-ops/RenderingGen/queue v0.1.1\n")

	r := &report.Report{}
	ScanQueueSingleModule(root, &policy.Policy{}, r)

	if !strings.Contains(violationNotes(r), "must be pinned to v0.1.0") {
		t.Fatalf("expected version pin violation, got %+v", r.Violations)
	}
}
