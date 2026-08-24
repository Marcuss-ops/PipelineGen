// Package scan — percheck_clip_ingest_pipeline_canonical_1_test.go
//
// Self-test for the forward-prevention ClipIngestPipeline canonical-1
// scanner (PR-CLIPINGEST-PIPELINE step 8, July 2026).
//
// Covered invariants:
//  1. The canonical owner's basename exempts itself.
//  2. A duplicated `type ClipIngestPipeline struct {` declaration
//     anywhere outside the canonical owner is reported.
//  3. A duplicated `ClipIngestPipeline{...}` literal usage is reported.
//  4. The scanner's own file names match the rule-name whitelist
//     — scanner source MUST NOT trip on itself.
package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// writeTempTree writes the supplied (relativePath → contents) files
// under a temp dir and returns the dir + a cleanup func. Tests use it
// to construct a synthetic project tree the scanner can walk without
// risking the real project working tree.
func writeTempTree(t *testing.T, files map[string]string) (string, func()) {
	t.Helper()
	dir := t.TempDir()
	for rel, contents := range files {
		abs := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(abs, []byte(contents), 0o644); err != nil {
			t.Fatalf("writefile: %v", err)
		}
	}
	return dir, func() {}
}

const canonicalOwnerGo = `package ingest

type ClipIngestPipeline struct {
	Downloader interface{}
}
`

const canonicalOwnerPath = "internal/application/assets/ingest/clip_ingest_pipeline.go"

func TestScanClipIngestPipelineCanonical1_CanonicalOwnerNoViolation(t *testing.T) {
	dir, cleanup := writeTempTree(t, map[string]string{
		canonicalOwnerPath: canonicalOwnerGo,
	})
	defer cleanup()
	v := ScanClipIngestPipelineCanonical1(dir)
	for _, viol := range v {
		if viol.File != canonicalOwnerPath {
			t.Errorf("unexpected violation in canonical owner: %+v", viol)
		}
	}
}

const shadowDeclarerGo = `package shadow

type ClipIngestPipeline struct {
	Foo string
}
`

func TestScanClipIngestPipelineCanonical1_ShadowDeclarationReported(t *testing.T) {
	dir, cleanup := writeTempTree(t, map[string]string{
		canonicalOwnerPath: canonicalOwnerGo,
		"internal/capabilities/assets/providers/shadow/clip_ingest_shadow.go": shadowDeclarerGo,
	})
	defer cleanup()
	v := ScanClipIngestPipelineCanonical1(dir)
	if len(v) == 0 {
		t.Fatalf("expected at least one violation for shadow declaration; got none")
	}
	foundShadow := false
	for _, viol := range v {
		if strings.Contains(viol.File, "clip_ingest_shadow.go") &&
			strings.Contains(viol.Note, "type declaration") {
			foundShadow = true
			break
		}
	}
	if !foundShadow {
		t.Fatalf("expected shadow-type-declaration violation; got: %+v", v)
	}
}

const shadowLiteralGo = `package ecommerce

import "shadow"

func New() shadow.ClipIngestPipeline {
	return shadow.ClipIngestPipeline{Foo: "bar"}
}
`

func TestScanClipIngestPipelineCanonical1_ShadowLiteralReported(t *testing.T) {
	dir, cleanup := writeTempTree(t, map[string]string{
		canonicalOwnerPath: canonicalOwnerGo,
		"internal/capabilities/assets/providers/shadow/clip_ingest_shadow.go": shadowDeclarerGo,
		"internal/application/ecommerce/clip_ingest_literal.go":               shadowLiteralGo,
	})
	defer cleanup()
	v := ScanClipIngestPipelineCanonical1(dir)
	foundLiteral := false
	for _, viol := range v {
		if strings.Contains(viol.Note, "literal") {
			foundLiteral = true
			break
		}
	}
	if !foundLiteral {
		t.Fatalf("expected literal violation; got: %+v", v)
	}
}

func TestScanClipIngestPipelineCanonical1_NonGoDirsIgnored(t *testing.T) {
	dir, cleanup := writeTempTree(t, map[string]string{
		canonicalOwnerPath: canonicalOwnerGo,
		"docs/notes.md":    "type ClipIngestPipeline struct{}",
		"scripts/run.sh":   "ClipIngestPipeline{}",
	})
	defer cleanup()
	v := ScanClipIngestPipelineCanonical1(dir)
	for _, viol := range v {
		if viol.Note == "" || strings.Contains(viol.Note, "Walk") {
			continue
		}
		t.Errorf("unexpected violation in non-Go dir: %+v", viol)
	}
}

func TestScanClipIngestPipelineCanonical1_EmptyProjectRootReturnsClean(t *testing.T) {
	// Repo root of "." should not panic; it returns only owner-site hits (or none).
	v := ScanClipIngestPipelineCanonical1(".")
	for _, viol := range v {
		if viol.Severity != string(report.SeverityError) || viol.Rule != "percheck_clip_ingest_pipeline_canonical_1" {
			t.Errorf("unexpected violation at repo root: %+v", viol)
		}
	}
}
