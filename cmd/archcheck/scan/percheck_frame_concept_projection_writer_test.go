// Package scan — tests for ScanFrameConceptProjectionWriter
// (PR-HASH-SEMANTICS item 8/16, August 2026).
//
// Hermetic (t.TempDir-anchored). Pins the projection-separation contract:
//  1. A point write to FrameCollectionName/ConceptCollectionName outside the
//     respective writer file trips the gate.
//  2. The frame writer may write ONLY FrameCollectionName; the concept writer
//     may write ONLY ConceptCollectionName (cross-writes trip).
//  3. Read paths (HybridSearchPoints) and collection-lifecycle calls
//     (CreateCollection) are not point writes and are not matched.
//  4. Test files are exempt.
package scan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func makeFrameConceptFile(t *testing.T, root, relPath, content string) {
	t.Helper()
	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

const frameWriterBody = `package qdrantmm

func (i *FrameQdrantIndexer) index() error {
	return i.writer.UpsertProjection(ctx, schema.FrameCollectionName, []schema.Point{point})
}
`

const conceptWriterBody = `package qdrantmm

func (i *QdrantIndexer) index() error {
	return i.writer.UpsertProjection(ctx, schema.ConceptCollectionName, []schema.Point{point})
}
`

func TestScanFrameConceptWriter_NonAuthorizedFileTrips(t *testing.T) {
	root := t.TempDir()
	makeFrameConceptFile(t, root, "internal/application/random_other/write.go",
		`package random_other

func write(w qdrantindexing.ProjectionWriter) error {
	return w.UpsertProjection(ctx, schema.FrameCollectionName, []schema.Point{p})
}
`)
	rep := &report.Report{}
	ScanFrameConceptProjectionWriter(root, nil, rep, true)
	if got := len(rep.Violations); got == 0 {
		t.Fatalf("non-authorized frame write did NOT trip gate; expected ≥ 1 violation")
	}
	if rep.Violations[0].Rule != frameConceptWriterRule {
		t.Fatalf("rule = %q, want %q", rep.Violations[0].Rule, frameConceptWriterRule)
	}
	if rep.Violations[0].MatchedRule != "non_canonical_frame_concept_write" {
		t.Fatalf("MatchedRule = %q, want non_canonical_frame_concept_write", rep.Violations[0].MatchedRule)
	}
}

func TestScanFrameConceptWriter_RawTransportTrips(t *testing.T) {
	root := t.TempDir()
	makeFrameConceptFile(t, root, "internal/application/random_other/write.go",
		`package random_other

func write(c *transport.Client) error {
	return c.UpsertPoints(ctx, schema.ConceptCollectionName, []schema.Point{p})
}
`)
	rep := &report.Report{}
	ScanFrameConceptProjectionWriter(root, nil, rep, true)
	if got := len(rep.Violations); got == 0 {
		t.Fatalf("raw transport concept write did NOT trip gate; expected ≥ 1 violation")
	}
}

func TestScanFrameConceptWriter_RespectiveWriterExempt(t *testing.T) {
	root := t.TempDir()
	makeFrameConceptFile(t, root, "internal/platform/qdrant/qdrantmm/qdrant_frame_indexer.go", frameWriterBody)
	makeFrameConceptFile(t, root, "internal/platform/qdrant/qdrantmm/qdrant_indexer.go", conceptWriterBody)
	rep := &report.Report{}
	ScanFrameConceptProjectionWriter(root, nil, rep, true)
	if got := len(rep.Violations); got != 0 {
		t.Fatalf("respective writers tripped gate: %d violations\nfirst: %s", got, rep.Violations[0].Note)
	}
}

func TestScanFrameConceptWriter_CrossWriteTrips(t *testing.T) {
	// The frame writer writing the concept collection is a violation.
	root := t.TempDir()
	makeFrameConceptFile(t, root, "internal/platform/qdrant/qdrantmm/qdrant_frame_indexer.go",
		`package qdrantmm

func (i *FrameQdrantIndexer) bad() error {
	return i.writer.UpsertProjection(ctx, schema.ConceptCollectionName, []schema.Point{point})
}
`)
	rep := &report.Report{}
	ScanFrameConceptProjectionWriter(root, nil, rep, true)
	if got := len(rep.Violations); got == 0 {
		t.Fatalf("frame-writer cross-write to concept collection did NOT trip gate; expected ≥ 1 violation")
	}
}

func TestScanFrameConceptWriter_ReadAndLifecycleNotMatched(t *testing.T) {
	root := t.TempDir()
	makeFrameConceptFile(t, root, "internal/application/search/read.go",
		`package search

func read(c *transport.Client) error {
	_, err := c.HybridSearchPoints(ctx, schema.ConceptCollectionName, req)
	return err
}
`)
	makeFrameConceptFile(t, root, "internal/app/wire.go",
		`package app

func ensure(mgr *collections.CollectionManager) error {
	return mgr.CreateCollection(ctx, qdrantschema.ConceptCollectionName)
}
`)
	rep := &report.Report{}
	ScanFrameConceptProjectionWriter(root, nil, rep, true)
	if got := len(rep.Violations); got != 0 {
		t.Fatalf("read/lifecycle calls must NOT trip: %d violations\nfirst: %s", got, rep.Violations[0].Note)
	}
}

func TestScanFrameConceptWriter_TestFilesExempt(t *testing.T) {
	root := t.TempDir()
	makeFrameConceptFile(t, root, "internal/application/random_other/write_test.go",
		`package random_other

func TestWrite(t *testing.T) {
	_ = w.UpsertProjection(ctx, schema.FrameCollectionName, []schema.Point{p})
}
`)
	rep := &report.Report{}
	ScanFrameConceptProjectionWriter(root, nil, rep, true)
	if got := len(rep.Violations); got != 0 {
		t.Fatalf("test file tripped gate: %d violations", got)
	}
}
