// Package scan — tests for ScanEmbeddingConstantsSSOT
// (PR-HASH-SEMANTICS item 16, August 2026).
//
// Hermetic (t.TempDir-anchored). Pins the forward-prevention contract:
//  1. A NEW embedding model-id declaration outside the canonical
//     internal/kernel/embedding package trips the gate.
//  2. The canonical package is exempt.
//  3. Struct-literal fields (`Model: "..."`) and config-tag defaults
//     (`default:"..."`) are NOT matched (data, not declarations).
//  4. Test files are exempt.
package scan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func makeEmbeddingConstantsFile(t *testing.T, root, relPath, content string) {
	t.Helper()
	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestScanEmbeddingConstantsSSOT_NonCanonicalDeclarationTrips(t *testing.T) {
	root := t.TempDir()
	makeEmbeddingConstantsFile(t, root, "internal/application/random_other/embed.go",
		`package random_other

const embeddingModel = "nomic-embed-text"
`)
	rep := &report.Report{}
	ScanEmbeddingConstantsSSOT(root, nil, rep, true)
	if got := len(rep.Violations); got == 0 {
		t.Fatalf("non-canonical embedding constant did NOT trip gate; expected ≥ 1 violation")
	}
	if rep.Violations[0].Rule != embeddingConstantsRule {
		t.Fatalf("rule = %q, want %q", rep.Violations[0].Rule, embeddingConstantsRule)
	}
	if rep.Violations[0].MatchedRule != "non_canonical_embedding_constant" {
		t.Fatalf("MatchedRule = %q, want non_canonical_embedding_constant", rep.Violations[0].MatchedRule)
	}
}

func TestScanEmbeddingConstantsSSOT_ConstBlockMemberTrips(t *testing.T) {
	root := t.TempDir()
	makeEmbeddingConstantsFile(t, root, "internal/application/random_other/embed.go",
		`package random_other

const (
	modelID = "intfloat/multilingual-e5-base"
)
`)
	rep := &report.Report{}
	ScanEmbeddingConstantsSSOT(root, nil, rep, true)
	if got := len(rep.Violations); got == 0 {
		t.Fatalf("const-block embedding constant did NOT trip gate; expected ≥ 1 violation")
	}
}

func TestScanEmbeddingConstantsSSOT_CanonicalOwnerExempt(t *testing.T) {
	root := t.TempDir()
	makeEmbeddingConstantsFile(t, root, "internal/kernel/embedding/contract.go",
		`package embedding

const ModelIDMultilingualE5 = "intfloat/multilingual-e5-base"
`)
	rep := &report.Report{}
	ScanEmbeddingConstantsSSOT(root, nil, rep, true)
	if got := len(rep.Violations); got != 0 {
		t.Fatalf("canonical embedding package tripped gate: %d violations\nfirst: %s", got, rep.Violations[0].Note)
	}
}

func TestScanEmbeddingConstantsSSOT_StructLiteralNotMatched(t *testing.T) {
	root := t.TempDir()
	makeEmbeddingConstantsFile(t, root, "internal/infrastructure/qdrant/schema/schema.go",
		`package schema

func Default() EmbeddingSpec {
	return EmbeddingSpec{Model: "multilingual-e5-base", Dimensions: 768}
}
`)
	rep := &report.Report{}
	ScanEmbeddingConstantsSSOT(root, nil, rep, true)
	if got := len(rep.Violations); got != 0 {
		t.Fatalf("struct-literal field (Model: ...) must NOT trip: %d violations\nfirst: %s", got, rep.Violations[0].Note)
	}
}

func TestScanEmbeddingConstantsSSOT_ConfigTagNotMatched(t *testing.T) {
	root := t.TempDir()
	makeEmbeddingConstantsFile(t, root, "internal/platform/config/types_external.go",
		"package config\n\ntype ExternalConfig struct {\n\tOllamaEmbedModel string `yaml:\"ollama_embed_model\" env:\"OLLAMA_EMBED_MODEL\" default:\"nomic-embed-text\"`\n}\n")
	rep := &report.Report{}
	ScanEmbeddingConstantsSSOT(root, nil, rep, true)
	if got := len(rep.Violations); got != 0 {
		t.Fatalf("config-tag default (default:...) must NOT trip: %d violations\nfirst: %s", got, rep.Violations[0].Note)
	}
}

func TestScanEmbeddingConstantsSSOT_TestFilesExempt(t *testing.T) {
	root := t.TempDir()
	makeEmbeddingConstantsFile(t, root, "internal/application/random_other/embed_test.go",
		`package random_other

const testEmbeddingModel = "nomic-embed-text"
`)
	rep := &report.Report{}
	ScanEmbeddingConstantsSSOT(root, nil, rep, true)
	if got := len(rep.Violations); got != 0 {
		t.Fatalf("test file tripped gate: %d violations", got)
	}
}
