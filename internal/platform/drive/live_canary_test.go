package drive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"

	delivery "github.com/Marcuss-ops/PipelineGen/internal/capabilities/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// TestLiveDriveArtifactCanary is an opt-in credential and write canary. It
// exercises the real Google Drive service and PutFile integrity gate; the
// created file is moved to Drive trash after verification. It is excluded
// from normal tests because it requires operator credentials and a writable
// folder ID.
func TestLiveDriveArtifactCanary(t *testing.T) {
	if os.Getenv("PIPELINEGEN_DRIVE_E2E") != "1" {
		t.Skip("set PIPELINEGEN_DRIVE_E2E=1 to run the live Drive canary")
	}
	folderID := os.Getenv("PIPELINEGEN_DRIVE_CANARY_FOLDER_ID")
	if folderID == "" {
		t.Fatal("PIPELINEGEN_DRIVE_CANARY_FOLDER_ID is required")
	}
	cfg, err := config.GetFromPath("config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	service, err := NewDriveServiceFromFiles(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	uploader := &Uploader{Service: service, Log: zap.NewNop()}

	content := []byte("PipelineGen overlay artifact Drive canary\n")
	sum := sha256.Sum256(content)
	hash := hex.EncodeToString(sum[:])
	file := filepath.Join(t.TempDir(), "pipelinegen-overlay-artifact-canary.txt")
	if err := os.WriteFile(file, content, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := uploader.PutFile(ctx, PutFileRequest{
		LocalPath: file, FolderID: folderID,
		Filename:       "pipelinegen-overlay-artifact-canary.txt",
		Description:    "PipelineGen overlay artifact Drive canary",
		ConflictPolicy: delivery.ConflictSkip,
		IdempotencyKey: "pipelinegen-overlay-artifact-drive-canary-v1",
		ContentHash:    hash, ExpectedSize: int64(len(content)), ExpectedSHA256: hash,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.FileID == "" || result.WebViewLink == "" {
		t.Fatalf("Drive canary returned incomplete result: %+v", result)
	}
	t.Logf("live Drive upload PASS: file_id=%s action=%s link=%s", result.FileID, result.Action, result.WebViewLink)
	if err := uploader.TrashFile(ctx, result.FileID); err != nil {
		t.Fatalf("trash canary file %s: %v", result.FileID, err)
	}
}
