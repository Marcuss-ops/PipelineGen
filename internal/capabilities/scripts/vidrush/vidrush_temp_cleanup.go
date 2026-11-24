package vidrush

import (
	"os"
	"path/filepath"
	"strings"
)

// vidRushDownloadTempPrefix is the os.CreateTemp pattern used by the internet
// image provider. Only files with this shape are owned by the download path.
const vidRushDownloadTempPrefix = "vidrush-image-"

// cleanupVidRushDownloadedTempArtifact removes the ephemeral provider download
// once the artifact is finalized.
//
// It is deliberately conservative: it deletes ONLY a file that (a) lives in the
// OS temp directory, (b) carries the vidrush download prefix, and (c) is NOT the
// path the persisted candidate still points at. When CAS promotion is
// unavailable the persisted local path IS the download, so the bytes are kept
// for the render lane instead of being deleted out from under it.
func cleanupVidRushDownloadedTempArtifact(downloadedPath, persistedPath string) {
	downloadedPath = strings.TrimSpace(downloadedPath)
	if downloadedPath == "" {
		return
	}
	if persistedPath = strings.TrimSpace(persistedPath); persistedPath != "" && sameFilePath(downloadedPath, persistedPath) {
		return
	}
	if !sameFilePath(filepath.Dir(downloadedPath), os.TempDir()) {
		return
	}
	if !strings.HasPrefix(filepath.Base(downloadedPath), vidRushDownloadTempPrefix) {
		return
	}
	_ = os.Remove(downloadedPath)
}

func sameFilePath(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
