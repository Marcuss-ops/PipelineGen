package remote

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
)

// writeTempFile writes content to a temp file under t.TempDir() and
// returns the path, the file size, and its SHA-256 hex digest.
func writeTestFile(t *testing.T, content string) (path string, size int64, sha string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "test-artifact.bin")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	size = int64(len(content))
	h := sha256.New()
	h.Write([]byte(content))
	sha = hex.EncodeToString(h.Sum(nil))
	return
}

// deriveKey is a test helper that mirrors deriveIdempotencyKey.
func deriveKey(jobID, artifactID, sha256hex string, sourceVersion int64) string {
	return deriveIdempotencyKey(jobID, artifactID, sha256hex, sourceVersion)
}

// ── Test (a): existing-file with correct claims passes verification ──

func TestVerifyArtifact_ExistingFile_Passes(t *testing.T) {
	localPath, size, contentSHA := writeTestFile(t, "hello world artifact content")
	const jobID = "job-abc-123"
	artifactID := "artifact-001"
	sourceVersion := int64(1)

	artifact := finalization.VerifiedArtifact{
		ArtifactID:     artifactID,
		Kind:           finalization.KindVideo,
		Filename:       "clip.mp4",
		LocalPath:      localPath,
		MIMEType:       "video/mp4",
		SizeBytes:      size,
		SHA256:         contentSHA,
		SourceVersion:  sourceVersion,
		Requirement:    finalization.ArtifactRequirementRequired,
		IdempotencyKey: deriveKey(jobID, artifactID, contentSHA, sourceVersion),
	}

	err := VerifyArtifact(context.Background(), jobID, artifact)
	if err != nil {
		t.Fatalf("VerifyArtifact() unexpected error: %v", err)
	}
}

// ── Test (b): wrong-hash fails with typed ErrArtifactVerificationFailed ──

func TestVerifyArtifact_WrongHash_Fails(t *testing.T) {
	localPath, size, _ := writeTestFile(t, "correct content")
	const jobID = "job-xyz-456"
	artifactID := "artifact-002"
	sourceVersion := int64(2)
	wrongSHA := "0000000000000000000000000000000000000000000000000000000000000000"

	artifact := finalization.VerifiedArtifact{
		ArtifactID:     artifactID,
		Kind:           finalization.KindImage,
		Filename:       "thumb.png",
		LocalPath:      localPath,
		MIMEType:       "image/png",
		SizeBytes:      size,
		SHA256:         wrongSHA,
		SourceVersion:  sourceVersion,
		Requirement:    finalization.ArtifactRequirementOptional,
		IdempotencyKey: deriveKey(jobID, artifactID, wrongSHA, sourceVersion),
	}

	err := VerifyArtifact(context.Background(), jobID, artifact)
	if err == nil {
		t.Fatal("expected ErrArtifactVerificationFailed for wrong hash, got nil")
	}
	if !errors.Is(err, ErrArtifactVerificationFailed) {
		t.Errorf("expected ErrArtifactVerificationFailed, got: %v", err)
	}
	if errStr := err.Error(); !strings.Contains(errStr, "SHA-256 mismatch") {
		t.Errorf("expected error to mention 'SHA-256 mismatch', got: %s", errStr)
	}
}

// ── Test (c): missing-file fails with typed ErrArtifactVerificationFailed ──

func TestVerifyArtifact_MissingFile_Fails(t *testing.T) {
	const jobID = "job-missing-789"
	artifactID := "artifact-003"
	sourceVersion := int64(1)
	dummySHA := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

	artifact := finalization.VerifiedArtifact{
		ArtifactID:     artifactID,
		Kind:           finalization.KindDocument,
		Filename:       "doc.pdf",
		LocalPath:      "/nonexistent/path/to/artifact.bin",
		MIMEType:       "application/pdf",
		SizeBytes:      100,
		SHA256:         dummySHA,
		SourceVersion:  sourceVersion,
		Requirement:    finalization.ArtifactRequirementOptional,
		IdempotencyKey: deriveKey(jobID, artifactID, dummySHA, sourceVersion),
	}

	err := VerifyArtifact(context.Background(), jobID, artifact)
	if err == nil {
		t.Fatal("expected ErrArtifactVerificationFailed for missing file, got nil")
	}
	if !errors.Is(err, ErrArtifactVerificationFailed) {
		t.Errorf("expected ErrArtifactVerificationFailed, got: %v", err)
	}
	if errStr := err.Error(); !strings.Contains(errStr, "file not found") {
		t.Errorf("expected error to mention 'file not found', got: %s", errStr)
	}
}
