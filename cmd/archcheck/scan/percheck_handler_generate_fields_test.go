// Package scan — tests for ScanHandlerGenerateFields (Step 9.c
// handler-purity forward-prevention gate, July 2026).
//
// Hermetic (t.TempDir-anchored). Each case builds a synthetic
// internal/api/script/handler_generate_handler.go fixture and
// asserts the report outcome against canonical expectations.
//
// 1. The CURRENT HandlerGenerate shape (5 fields:
//
//	submitter / scriptgenSvc / factory / log / validator)
//
//	is the canonical emit-zero-violations surface.
//
// 2. A forbidden random field trips the gate as SeverityError.
// 3. Application-port pointer fields pass; non-port pointer
// fields fail (selective verification).
// 4. A missing HandlerGenerate declaration emits the
// canonical fail-closed SeverityError under rule id
// percheck_handler_generate_fields_decl_missing.
// 5. A missing target file is silent (deferred to the
// wider file_size/pkg_size gates).
package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// writeHFFixture writes a fixture .go file at the requested
// repo-relative path inside `root`.
func writeHFFixture(t *testing.T, root, relPath, content string) {
	t.Helper()
	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// TestScanHandlerGenerateFields_HappyPath_NoViolations pins the
// CURRENT canonical HandlerGenerate shape as the zero-violation
// emit. Any future drift that adds a non-port field will trip
// the gate.
func TestScanHandlerGenerateFields_HappyPath_NoViolations(t *testing.T) {
	root := t.TempDir()
	writeHFFixture(t, root, handlerGenerateFieldScanScope, `package script

import (
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/submission"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	opsapp "github.com/Marcuss-ops/PipelineGen/internal/application/operations"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

type generationSubmitter interface {
	Submit(ctx context.Context, req opsapp.SubmitRequest) (*opsapp.SubmitResult, error)
}

// Canonical 5-field HandlerGenerate (FASE 2 / AZIONE-1 surface).
type HandlerGenerate struct {
	submitter    generationSubmitter
	scriptgenSvc *scriptgen.GenerationRunStarter
	factory      *submission.SubmitRequestFactory
	log          *zap.Logger
	validator    *usecase.PayloadValidator
}
`)
	rep := &report.Report{}
	ScanHandlerGenerateFields(root, &policy.Policy{}, rep)
	if len(rep.Violations) != 0 {
		t.Fatalf("canonical HandlerGenerate shape must emit 0 violations; got %d.\nFirst: %s",
			len(rep.Violations), rep.Violations[0].Note)
	}
}

// TestScanHandlerGenerateFields_ForbidsDriftField verifies that
// a random additional field on HandlerGenerate trips the gate.
func TestScanHandlerGenerateFields_ForbidsDriftField(t *testing.T) {
	root := t.TempDir()
	writeHFFixture(t, root, handlerGenerateFieldScanScope, `package script

type HandlerGenerate struct {
	submitter int
	validator int
	log       int
	drvClient int  // forbidden random field (godlike/07 NO-FAKE-AVAILABILITY boundary violation)
}
`)
	rep := &report.Report{}
	ScanHandlerGenerateFields(root, &policy.Policy{}, rep)
	if len(rep.Violations) == 0 {
		t.Fatalf("drvClient must trip gate; got 0 violations")
	}
	if rep.Violations[0].Rule != handlerGenerateFieldRule {
		t.Fatalf("violation rule = %q, want %q", rep.Violations[0].Rule, handlerGenerateFieldRule)
	}
	if rep.Violations[0].Severity != string(report.SeverityError) {
		t.Fatalf("violation severity = %q, want SeverityError", rep.Violations[0].Severity)
	}
	if !strings.Contains(rep.Violations[0].Note, "drvClient") {
		t.Fatalf("violation note must mention \"drvClient\" field; got %q", rep.Violations[0].Note)
	}
}

// TestScanHandlerGenerateFields_AcceptsApplicationPortPointer
// pins selective application-port acceptance: a pointer typed
// under an application-port prefix passes; a pointer typed
// under a non-port prefix fails.
func TestScanHandlerGenerateFields_AcceptsApplicationPortPointer(t *testing.T) {
	root := t.TempDir()
	writeHFFixture(t, root, handlerGenerateFieldScanScope, `package script

import opsapp "github.com/Marcuss-ops/PipelineGen/internal/application/operations"

type appPort struct{ x int }

type generationSubmitter interface{ X() }

type HandlerGenerate struct {
	submitter generationSubmitter
	validator int
	log       int
	custom    *opsapp.SubmitRequest // application-port → OK
	iface     *appPort              // not application-port → FAIL
}
`)
	rep := &report.Report{}
	ScanHandlerGenerateFields(root, &policy.Policy{}, rep)
	// Expect 1 violation: only `iface` should fail (the others
	// are strict-named or application-port).
	if len(rep.Violations) != 1 {
		t.Fatalf("expected exactly 1 violation (iface); got %d.\nAll: %v",
			len(rep.Violations), rep.Violations)
	}
	if !strings.Contains(rep.Violations[0].Note, "iface") {
		t.Fatalf("violation note must mention \"iface\" field; got %q", rep.Violations[0].Note)
	}
	if !strings.Contains(rep.Violations[0].Note, "appPort") {
		t.Fatalf("violation note must mention the type \"appPort\"; got %q", rep.Violations[0].Note)
	}
}

// TestScanHandlerGenerateFields_MissingDecl_FailsClosed verifies
// the canonical fail-closed behavior when HandlerGenerate is
// absent from the audit target file.
func TestScanHandlerGenerateFields_MissingDecl_FailsClosed(t *testing.T) {
	root := t.TempDir()
	writeHFFixture(t, root, handlerGenerateFieldScanScope, `package script

// Anchor moved; only OtherStruct remains.
type OtherStruct struct {
	a int
}
`)
	rep := &report.Report{}
	ScanHandlerGenerateFields(root, &policy.Policy{}, rep)
	hits := filterByRule(rep.Violations, handlerGenerateFieldMissingDeclRule)
	if len(hits) != 1 {
		t.Fatalf("expected 1 fail-closed decl_missing violation; got %d. All: %v",
			len(hits), rep.Violations)
	}
	if hits[0].Severity != string(report.SeverityError) {
		t.Fatalf("fail-closed severity = %q, want SeverityError", hits[0].Severity)
	}
	if !strings.Contains(hits[0].Note, "fail-closed") {
		t.Fatalf("fail-closed note missing marker; got %q", hits[0].Note)
	}
}

// TestScanHandlerGenerateFields_FileMissing_Silent verifies the
// gate stays silent on a missing target file (the wider
// file_size/pkg_size gates cover the \"file does not exist\"
// case; reporting twice would duplicate noise).
func TestScanHandlerGenerateFields_FileMissing_Silent(t *testing.T) {
	root := t.TempDir()
	// No fixture written.
	rep := &report.Report{}
	ScanHandlerGenerateFields(root, &policy.Policy{}, rep)
	if len(rep.Violations) != 0 {
		t.Fatalf("missing target file must be silent; got %d violations.\nFirst: %s",
			len(rep.Violations), rep.Violations[0].Note)
	}
	if len(rep.Warnings) != 0 {
		t.Fatalf("missing target file must emit 0 warnings; got %d.\nAll: %v",
			len(rep.Warnings), rep.Warnings)
	}
}

// TestScanHandlerGenerateFields_EmbeddedField_TripsGate pins
// the embedded-field rule: an anonymous embedded field on
// HandlerGenerate is a godlike/07 boundary violation.
func TestScanHandlerGenerateFields_EmbeddedField_TripsGate(t *testing.T) {
	root := t.TempDir()
	writeHFFixture(t, root, handlerGenerateFieldScanScope, `package script

type Base struct{}

type HandlerGenerate struct {
	Base // embedded; no Names → forbidden
	submitter int
	validator int
	log       int
}
`)
	rep := &report.Report{}
	ScanHandlerGenerateFields(root, &policy.Policy{}, rep)
	if len(rep.Violations) == 0 {
		t.Fatalf("embedded field must trip gate; got 0 violations")
	}
	if rep.Violations[0].MatchedRule != "handler_generate_embedded_field" {
		t.Fatalf("violation matched_rule = %q, want handler_generate_embedded_field",
			rep.Violations[0].MatchedRule)
	}
}
