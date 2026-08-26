// Package scan — percheck_slot_strings_ban_test.go pins the
// forward-prevention contract for the slot-string literal ban.
package governance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func slotStringsTestReport() *report.Report {
	return &report.Report{
		Summary: report.Summary{
			ByReason:   map[string]int{},
			BySeverity: map[string]int{},
		},
	}
}

func slotStringsWriteTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for relPath, contents := range files {
		fullPath := filepath.Join(dir, relPath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", filepath.Dir(fullPath), err)
		}
		if err := os.WriteFile(fullPath, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %q: %v", fullPath, err)
		}
	}
}

func slotStringsViolations(r *report.Report) []report.Violation {
	var out []report.Violation
	for _, v := range r.Violations {
		if v.Rule == "percheck_slot_strings_ban" {
			out = append(out, v)
		}
	}
	return out
}

// TestSlotStrings_CanonicalOwnerPasses verifies that the canonical
// SSOT file may contain the literal slot strings.
func TestSlotStrings_CanonicalOwnerPasses(t *testing.T) {
	dir := t.TempDir()
	slotStringsWriteTree(t, dir, map[string]string{
		"internal/kernel/media/slot.go": `package media
const SlotPrimaryVideo SlotKind = "primary_video"
const SlotDocument SlotKind = "document"
`,
	})
	r := slotStringsTestReport()
	ScanSlotStringsBan(dir, &policy.Policy{}, r)
	if got := len(slotStringsViolations(r)); got != 0 {
		t.Fatalf("want 0 violations inside canonical owner, got %d: %+v", got, r.Violations)
	}
}

// TestSlotStrings_DistinctLiteralFailsProduction verifies that a
// distinctive slot literal outside the canonical file trips the gate.
func TestSlotStrings_DistinctLiteralFailsProduction(t *testing.T) {
	dir := t.TempDir()
	slotStringsWriteTree(t, dir, map[string]string{
		"internal/capabilities/brain/types.go": `package brain
var PrimaryVideoSlot = "primary_video"
`,
	})
	r := slotStringsTestReport()
	ScanSlotStringsBan(dir, &policy.Policy{}, r)
	viol := slotStringsViolations(r)
	if len(viol) != 1 {
		t.Fatalf("want 1 violation, got %d: %+v", len(viol), r.Violations)
	}
	if !strings.Contains(viol[0].Note, "primary_video") {
		t.Fatalf("violation note must reference the offending literal; got %q", viol[0].Note)
	}
}

// TestSlotStrings_GenericWithoutSlotContextPasses verifies that
// overloaded slot words are allowed when the line has no slot context.
func TestSlotStrings_GenericWithoutSlotContextPasses(t *testing.T) {
	dir := t.TempDir()
	slotStringsWriteTree(t, dir, map[string]string{
		"internal/application/document/types.go": `package document
const DocumentDestination = "document"
`,
	})
	r := slotStringsTestReport()
	ScanSlotStringsBan(dir, &policy.Policy{}, r)
	if got := len(slotStringsViolations(r)); got != 0 {
		t.Fatalf("want 0 violations for unrelated use of 'document', got %d: %+v", got, r.Violations)
	}
}

// TestSlotStrings_GenericWithSlotContextFails verifies that an
// overloaded slot word is flagged when the line contains a slot
// context.
func TestSlotStrings_GenericWithSlotContextFails(t *testing.T) {
	dir := t.TempDir()
	slotStringsWriteTree(t, dir, map[string]string{
		"internal/capabilities/brain/planner/planner.go": `package planner
var FallbackSlots = map[string]string{"document": "lower_third"}
`,
	})
	r := slotStringsTestReport()
	ScanSlotStringsBan(dir, &policy.Policy{}, r)
	if got := len(slotStringsViolations(r)); got != 1 {
		t.Fatalf("want 1 violation for generic slot with slot context, got %d: %+v", got, r.Violations)
	}
}

// TestSlotStrings_TestFilesExempted verifies that production-gate
// logic does not apply to test files.
func TestSlotStrings_TestFilesExempted(t *testing.T) {
	dir := t.TempDir()
	slotStringsWriteTree(t, dir, map[string]string{
		"internal/capabilities/brain/types_test.go": `package brain
var TestSlot = "primary_video"
`,
	})
	r := slotStringsTestReport()
	ScanSlotStringsBan(dir, &policy.Policy{}, r)
	if got := len(slotStringsViolations(r)); got != 0 {
		t.Fatalf("want 0 violations inside test files, got %d: %+v", got, r.Violations)
	}
}
