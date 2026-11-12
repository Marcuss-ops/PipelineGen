package atomicwrite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommitFilePublishesExistingBytes(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, ".render.partial.mp4")
	final := filepath.Join(dir, "render.mp4")
	if err := os.WriteFile(tmp, []byte("rendered-video"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CommitFile(tmp, final); err != nil {
		t.Fatalf("CommitFile: %v", err)
	}
	data, err := os.ReadFile(final)
	if err != nil {
		t.Fatalf("published file missing: %v", err)
	}
	if string(data) != "rendered-video" {
		t.Fatalf("published bytes = %q", data)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("staged path still exists after commit: %v", err)
	}
}

func TestCommitFileMissingSourceLeavesDestinationUntouched(t *testing.T) {
	dir := t.TempDir()
	final := filepath.Join(dir, "render.mp4")
	if err := os.WriteFile(final, []byte("previous"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CommitFile(filepath.Join(dir, "missing.mp4"), final); err == nil {
		t.Fatal("CommitFile must fail when the staged file is missing")
	}
	data, err := os.ReadFile(final)
	if err != nil || string(data) != "previous" {
		t.Fatalf("destination was modified by a failed commit: %q, %v", data, err)
	}
}

func TestQuarantineMovesArtifactAndWritesReason(t *testing.T) {
	dir := t.TempDir()
	staged := filepath.Join(dir, ".render.partial.mp4")
	if err := os.WriteFile(staged, []byte("bad-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	qdir := filepath.Join(dir, "quarantine")
	dest, err := Quarantine(staged, qdir, "probe_failed: moov atom not found")
	if err != nil {
		t.Fatalf("Quarantine: %v", err)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Fatalf("rejected artifact still at publication path: %v", err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("quarantined artifact missing: %v", err)
	}
	reason, err := os.ReadFile(dest + ".reason.txt")
	if err != nil {
		t.Fatalf("reason sidecar missing: %v", err)
	}
	if !strings.Contains(string(reason), "moov atom not found") {
		t.Fatalf("reason sidecar = %q", reason)
	}
}
