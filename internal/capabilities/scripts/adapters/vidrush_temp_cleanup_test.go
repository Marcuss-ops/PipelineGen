package adapters

import (
	"os"
	"testing"
)

func writeTempDownload(t *testing.T, pattern string) string {
	t.Helper()
	file, err := os.CreateTemp("", pattern)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("downloaded bytes"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(file.Name()) })
	return file.Name()
}

func TestCleanupVidRushDownloadedTempArtifactRemovesPromotedDownload(t *testing.T) {
	path := writeTempDownload(t, "vidrush-image-*")
	cleanupVidRushDownloadedTempArtifact(path, "/data/cas/ab/cd/abcdef")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stat = %v, want the promoted download to be removed", err)
	}
}

func TestCleanupVidRushDownloadedTempArtifactKeepsThePersistedPath(t *testing.T) {
	path := writeTempDownload(t, "vidrush-image-*")
	cleanupVidRushDownloadedTempArtifact(path, path)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the persisted local path must survive cleanup: %v", err)
	}
}

func TestCleanupVidRushDownloadedTempArtifactIgnoresForeignFiles(t *testing.T) {
	path := writeTempDownload(t, "some-other-download-*")
	cleanupVidRushDownloadedTempArtifact(path, "")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("a file the image provider does not own must not be removed: %v", err)
	}
}
