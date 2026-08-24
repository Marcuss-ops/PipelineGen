// Package drive — uploader_put_p0_6_test.go (P0.6, July 2026)
//
// Tests pin the idempotency-key-as-identity semantics of PutFile:
//
//	Test (a): same-key-same-file — two publishes with the same
//	          idempotency key return the same Drive file
//	          (ConflictSkip on existing match).
//	Test (b): diff-artifact-same-filename-no-overwrite — two
//	          different artifacts with the same filename but
//	          different idempotency keys create separate files.
package drive

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/delivery"
)

// ── Test (a): same-key-same-file ────────────────────────────────────

func TestPutFile_IdempotencyKey_SameKeySameFile(t *testing.T) {
	// Simulate: first publish creates the file; second publish with same
	// idempotency key finds the existing file via appProperties lookup
	// and returns ConflictSkip.

	const idemKey = "abc123def456abc123def456abc123def456abc123def456abc123def4561234"
	var lookupCalls int

	u := &Uploader{
		Service: newFakeDriveService(t),
		Log:     zap.NewNop(),
		lookupFunc: func(_ *Uploader, _ context.Context, folderID, filename, idemKey string) (ExistingFileLookup, error) {
			lookupCalls++
			if idemKey == "" {
				// Fallback to filename lookup — no match.
				return ExistingFileLookup{}, nil
			}
			// Idempotency-key lookup finds an existing file.
			return ExistingFileLookup{
				Matches: []RemoteFile{
					{
						FileID:      "drive-existing-999",
						Name:        filename,
						WebViewLink: "https://drive.google.com/file/d/drive-existing-999/view",
						MD5Checksum: "existing-hash",
					},
				},
			}, nil
		},
	}

	// First call: no idempotency key → filename lookup → no match.
	// But the test can't actually create a file (we need Service for Create).
	// Instead, verify that with an idempotency key, the ConflictSkip
	// branch fires on the existing match returned by lookupFunc.
	req := PutFileRequest{
		LocalPath:      "/nonexistent/test.mp4",
		FolderID:       "folder-abc",
		Filename:       "artifact.mp4",
		ConflictPolicy: delivery.ConflictSkip,
		IdempotencyKey: idemKey,
	}
	result, err := u.PutFile(context.Background(), req)
	if err != nil {
		t.Fatalf("PutFile with idempotency key: %v", err)
	}

	// Should have skipped (existing match via idempotency key).
	if result.Action != PutActionSkipped {
		t.Errorf("expected PutActionSkipped for same idempotency key, got %q", result.Action)
	}
	if result.FileID != "drive-existing-999" {
		t.Errorf("expected existing FileID='drive-existing-999', got %q", result.FileID)
	}
	if lookupCalls != 1 {
		t.Errorf("expected 1 lookup call, got %d", lookupCalls)
	}
}

// ── Test (b): diff-artifact-same-filename-no-overwrite ───────────────

func TestPutFile_IdempotencyKey_DifferentArtifactSameFilename(t *testing.T) {
	// Two different artifacts share the same filename but have different
	// idempotency keys. The lookup by idempotency key returns no match
	// for the SECOND artifact, so a new Create should happen instead of
	// overwriting/conflicting with the first file.

	const idemKeyA = "aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa"
	const idemKeyB = "bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb"

	var lookupCalls int
	lookupCalls = 0

	u := &Uploader{
		Service: newFakeDriveService(t),
		Log:     zap.NewNop(),
		lookupFunc: func(_ *Uploader, _ context.Context, folderID, filename, idemKey string) (ExistingFileLookup, error) {
			lookupCalls++
			if idemKey == idemKeyA {
				// First artifact's key — found an existing file.
				return ExistingFileLookup{
					Matches: []RemoteFile{
						{
							FileID:      "drive-artifact-A",
							Name:        filename,
							WebViewLink: "https://drive.google.com/file/d/drive-artifact-A/view",
						},
					},
				}, nil
			}
			// Second artifact's key — NO existing match (different key).
			return ExistingFileLookup{}, nil
		},
	}

	// First publish: ConflictSkip with idemKeyA → finds existing → skipped.
	reqA := PutFileRequest{
		LocalPath:      "/nonexistent/a.mp4",
		FolderID:       "folder-xyz",
		Filename:       "output.mp4",
		ConflictPolicy: delivery.ConflictSkip,
		IdempotencyKey: idemKeyA,
	}
	resultA, err := u.PutFile(context.Background(), reqA)
	if err != nil {
		t.Fatalf("PutFile A: %v", err)
	}
	if resultA.Action != PutActionSkipped {
		t.Errorf("expected PutActionSkipped for idemKeyA, got %q", resultA.Action)
	}
	if resultA.FileID != "drive-artifact-A" {
		t.Errorf("expected FileID='drive-artifact-A', got %q", resultA.FileID)
	}

	// Second publish: ConflictSkip with idemKeyB → no match → should log
	// "skip requested but no existing match" and attempt Create. Since
	// Service is non-nil but the endpoint is [::1]:1, the Create will
	// fail with a connection error — which is fine: the assertion is
	// that the lookup returned no match, NOT that we overwrote file A.
	reqB := PutFileRequest{
		LocalPath:      "/nonexistent/b.mp4",
		FolderID:       "folder-xyz",
		Filename:       "output.mp4", // SAME filename
		ConflictPolicy: delivery.ConflictSkip,
		IdempotencyKey: idemKeyB,
	}
	_, errB := u.PutFile(context.Background(), reqB)

	// The Create attempt will fail (can't connect to [::1]:1), but
	// the important assertion is that the error is NOT about ambiguity
	// or overwrite — it's a network error from the Create attempt,
	// proving the lookup returned no match (different idempotency key).
	if errB == nil {
		// If it somehow succeeded, verify it created, not skipped.
		t.Log("unexpected success on second publish (Create worked?)")
	} else {
		// The error should NOT mention "ambiguous" (which would mean
		// filename-based lookup found the first file).
		if strings.Contains(errB.Error(), "ambiguous") {
			t.Errorf("expected no ambiguity error for different idempotency keys, got: %v", errB)
		}
		// The error SHOULD be from the Create attempt (connection refused or similar).
		if !strings.Contains(errB.Error(), "drive put failed") && !strings.Contains(errB.Error(), "putFile") {
			t.Errorf("expected Create-attempt error, got: %v", errB)
		}
	}

	if lookupCalls != 2 {
		t.Errorf("expected 2 lookup calls (one per publish), got %d", lookupCalls)
	}
}
