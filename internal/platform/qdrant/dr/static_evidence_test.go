package dr

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMeasureStaticRemovalEvidenceScopesQdrantRows(t *testing.T) {
	root := t.TempDir()
	allowlist := filepath.Join(root, "allowlist.txt")
	if err := os.WriteFile(allowlist, []byte("# comment\ninternal/qdrant/qdrant:SnapshotDescription # owner\ninternal/other/other:Thing # owner\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testRoot := filepath.Join(root, "legacy-tests")
	if err := os.MkdirAll(testRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(testRoot, "legacy_test.go"), []byte("package legacy"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := MeasureStaticRemovalEvidence(root, "allowlist.txt", []string{"legacy-tests"})
	if err != nil {
		t.Fatal(err)
	}
	if got.QdrantAllowlistEntries != 1 || got.LegacyProductionTests != 1 {
		t.Fatalf("unexpected evidence: %+v", got)
	}
}

func TestMeasureStaticRemovalEvidenceFailsClosedOnMissingRoot(t *testing.T) {
	_, err := MeasureStaticRemovalEvidence(t.TempDir(), "missing.txt", nil)
	if err == nil {
		t.Fatal("expected missing allowlist error")
	}
}
