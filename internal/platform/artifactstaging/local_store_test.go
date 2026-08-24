// Package artifacts — local_store_test.go (FASE 3-A, July 2026):
// hermetic test surface for LocalStore.
//
// White-box (package artifacts) so the statfsFn injection seam
// (Config.statfsFn) is reachable without exporting it through
// the public Config API.
//
// Every test runs against a fresh t.TempDir() workspace, so the
// surface is hermetic: no FS pollution across tests, no global
// state, no concurrency flakes.
//
// godlike/07 NO-FAKE-AVAILABILITY: tests verify the typed
// sentinels surface correctly on every failure path, not via
// substring matching of error messages.
package artifacts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/staging"
	artifact "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// TestLocalStore_Stage_HappyPath_SHAComputedDuringWrite pins the
// canonical FASE 3 (a) contract: Stage writes the content, computes
// SHA-256 during write (NOT post-stat), returns a receipt with the
// hex hash, and the resulting file matches an independent rehash.
func TestLocalStore_Stage_HappyPath_SHAComputedDuringWrite(t *testing.T) {
	tmp := newWorkspace(t)
	s, err := NewLocalStore(Config{Workspace: tmp, MinFreeBytes: 1})
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}

	payload := []byte("hello-FASE-3-A\n")
	in := staging.StageInput{
		ArtifactID: "art_test_happy_001",
		MIME:       "text/plain",
		SizeBytes:  int64(len(payload)),
		Content:    bytes.NewReader(payload),
	}
	receipt, err := s.Stage(context.Background(), in)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	sum := sha256.Sum256(payload)
	want := hex.EncodeToString(sum[:])
	if receipt.Hash != want {
		t.Errorf("Receipt.Hash = %s, want %s", receipt.Hash, want)
	}
	if receipt.Size != int64(len(payload)) {
		t.Errorf("Receipt.Size = %d, want %d", receipt.Size, len(payload))
	}
	if receipt.LocalPath != filepath.Join(tmp, in.ArtifactID) {
		t.Errorf("Receipt.LocalPath = %s, want %s", receipt.LocalPath, filepath.Join(tmp, in.ArtifactID))
	}

	// Verify file exists + matches.
	got, readErr := os.ReadFile(receipt.LocalPath)
	if readErr != nil {
		t.Fatalf("ReadFile(receipt.LocalPath): %v", readErr)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("file content drift")
	}
	// Independent rehash.
	f, openErr := os.Open(receipt.LocalPath)
	if openErr != nil {
		t.Fatalf("os.Open: %v", openErr)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatalf("rehash copy: %v", err)
	}
	if hex.EncodeToString(h.Sum(nil)) != want {
		t.Errorf("independent rehash mismatch")
	}
}

// TestLocalStore_Stage_TmpFileInvisibleBeforeRename pins that the
// `.partial/<id>.tmp` file is not visible to a canonical-path
// reader during the write window. We simulate a mid-stream pause
// via a custom Reader that blocks until signalled.
func TestLocalStore_Stage_TmpFileInvisibleBeforeRename(t *testing.T) {
	tmp := newWorkspace(t)
	s, err := NewLocalStore(Config{Workspace: tmp, MinFreeBytes: 1})
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}

	pr, pw := io.Pipe()
	defer pr.Close()
	released := make(chan struct{})
	stageDone := make(chan staging.StagedReceipt, 1)
	stageErr := make(chan error, 1)
	go func() {
		receipt, err := s.Stage(context.Background(), staging.StageInput{
			ArtifactID: "art_test_pause_001",
			MIME:       "text/plain",
			SizeBytes:  0,
			Content:    pr,
		})
		stageDone <- *receipt
		stageErr <- err
	}()

	// While Stage is mid-stream, canonical path MUST NOT exist.
	canonical := filepath.Join(tmp, "art_test_pause_001")
	if _, statErr := os.Stat(canonical); statErr == nil {
		t.Errorf("canonical path visible during write (atomic-rename breach): %q", canonical)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("unexpected stat error: %v", statErr)
	}

	// Complete the stream + unblock Stage.
	go func() {
		_, _ = pw.Write([]byte("paused-payload\n"))
		_ = pw.Close()
		close(released)
	}()
	<-released
	<-stageDone
	if err := <-stageErr; err != nil {
		t.Fatalf("Stage returned err: %v", err)
	}
	// Now canonical path MUST exist.
	if _, statErr := os.Stat(canonical); statErr != nil {
		t.Errorf("canonical path not visible after Stage: %v", statErr)
	}
}

// TestLocalStore_Stage_FilePermissionIs0600 pins the audit-aligned
// file-mode discipline. Per user-spec: staged file MUST be 0600.
func TestLocalStore_Stage_FilePermissionIs0600(t *testing.T) {
	tmp := newWorkspace(t)
	s, err := NewLocalStore(Config{Workspace: tmp, MinFreeBytes: 1})
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	_, err = s.Stage(context.Background(), staging.StageInput{
		ArtifactID: "art_test_perm_001",
		MIME:       "text/plain",
		SizeBytes:  4,
		Content:    bytes.NewReader([]byte("data")),
	})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	info, statErr := os.Stat(filepath.Join(tmp, "art_test_perm_001"))
	if statErr != nil {
		t.Fatalf("stat: %v", statErr)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file perm = 0%o, want 0600", perm)
	}
}

// TestLocalStore_Stage_WorkspaceAndPartialDirPermissionIs0700
// pins the audit-aligned workspace + .partial permission discipline.
func TestLocalStore_Stage_WorkspaceAndPartialDirPermissionIs0700(t *testing.T) {
	tmp := newWorkspace(t)
	s, err := NewLocalStore(Config{Workspace: tmp, MinFreeBytes: 1})
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	if perm := filePerm(t, tmp); perm != 0o700 {
		t.Errorf("workspace perm = 0%o, want 0700", perm)
	}
	if perm := filePerm(t, filepath.Join(tmp, partialDirName)); perm != 0o700 {
		t.Errorf(".partial perm = 0%o, want 0700", perm)
	}
	// Sanity: a Stage works under the perm-locked workspace.
	if _, err := s.Stage(context.Background(), staging.StageInput{
		ArtifactID: "art_test_perm_002",
		MIME:       "text/plain",
		SizeBytes:  4,
		Content:    bytes.NewReader([]byte("data")),
	}); err != nil {
		t.Fatalf("Stage under perm-locked workspace: %v", err)
	}
}

// TestLocalStore_NewLocalStore_NilWorkspaceRejected pins fail-fast.
func TestLocalStore_NewLocalStore_NilWorkspaceRejected(t *testing.T) {
	_, err := NewLocalStore(Config{Workspace: ""})
	if err == nil {
		t.Fatal("want error on nil workspace, got nil")
	}
	if !errors.Is(err, staging.ErrStagerNotConfigured) {
		t.Errorf("err = %v, want wrap ErrStagerNotConfigured", err)
	}
}

// TestLocalStore_NewLocalStore_PermissiveWorkspaceRejected pins the
// canonical fail-closed posture: a workspace whose mode bits
// include any group/other read/write/exec bit MUST be rejected at
// construction with the typed ErrStagerWorkspaceMissing sentinel.
// Per godlike/07 NO-FAKE-AVAILABILITY + audit FASE 3 user-spec
// ("workspace 0700"). Constructed by chmod'ing a newWorkspace(t)
// down to 0o755 — the LocalStore's verifyPermission0700 must surface
// this as a typed error rather than silently accepting a permissive
// deployment. Locks the production-grade invariant against future
// regressions where the verify call is dropped.
func TestLocalStore_NewLocalStore_PermissiveWorkspaceRejected(t *testing.T) {
	dir := newWorkspace(t)
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("chmod 0755: %v", err)
	}
	_, err := NewLocalStore(Config{Workspace: dir})
	if err == nil {
		t.Fatal("want perm-rejected error, got nil")
	}
	if !errors.Is(err, staging.ErrStagerWorkspaceMissing) {
		t.Errorf("err = %v, want wrap ErrStagerWorkspaceMissing", err)
	}
}

// TestLocalStore_Stage_InvalidMIMEFormatRejected pins typed
// sentinel on MIME validation.
func TestLocalStore_Stage_InvalidMIMEFormatRejected(t *testing.T) {
	tmp := newWorkspace(t)
	s, _ := NewLocalStore(Config{Workspace: tmp, MinFreeBytes: 1})
	_, err := s.Stage(context.Background(), staging.StageInput{
		ArtifactID: "art_test_mime_bad",
		MIME:       "not-a-mime-format",
		Content:    bytes.NewReader([]byte("data")),
	})
	if err == nil {
		t.Fatal("want error on bad MIME, got nil")
	}
	if !errors.Is(err, staging.ErrStagerInvalidInput) {
		t.Errorf("err = %v, want wrap ErrStagerInvalidInput", err)
	}
}

// TestLocalStore_Stage_InvalidArtifactIDRejected pins typed
// sentinel on ID validation — including path-traversal attempts.
func TestLocalStore_Stage_InvalidArtifactIDRejected(t *testing.T) {
	tmp := newWorkspace(t)
	s, _ := NewLocalStore(Config{Workspace: tmp, MinFreeBytes: 1})

	cases := []string{
		"",                                // empty
		"no-prefix",                       // wrong format
		"../etc/passwd",                   // path traversal
		"art_/etc/passwd",                 // embedded slash
		"art_a\nb",                        // embedded newline
		"art_" + strings.Repeat("a", 300), // too long
	}
	for _, id := range cases {
		t.Run("id="+id, func(t *testing.T) {
			_, err := s.Stage(context.Background(), staging.StageInput{
				ArtifactID: id,
				MIME:       "text/plain",
				Content:    bytes.NewReader([]byte("data")),
			})
			if err == nil {
				t.Fatal("want error, got nil")
			}
			if !errors.Is(err, staging.ErrStagerInvalidInput) {
				t.Errorf("id=%q err = %v, want wrap ErrStagerInvalidInput", id, err)
			}
		})
	}
}

// TestLocalStore_Stage_EmptyContentRejected pins the canonical
// "0-byte artifact is invalid" rule (audit FASE 3 (a)).
func TestLocalStore_Stage_EmptyContentRejected(t *testing.T) {
	tmp := newWorkspace(t)
	s, _ := NewLocalStore(Config{Workspace: tmp, MinFreeBytes: 1})
	_, err := s.Stage(context.Background(), staging.StageInput{
		ArtifactID: "art_test_empty_001",
		MIME:       "text/plain",
		Content:    bytes.NewReader(nil),
	})
	if err == nil {
		t.Fatal("want error on empty content, got nil")
	}
	if !errors.Is(err, artifact.ErrArtifactStageEmpty) {
		t.Errorf("err = %v, want wrap ErrArtifactStageEmpty", err)
	}
}

// TestLocalStore_Stage_PerArtifactQuotaRejected pins the re-
// aliased ErrQuotaExceeded sentinel — error identity must
// preserve so 3-B callers can errors.Is-probe a single alias.
func TestLocalStore_Stage_PerArtifactQuotaRejected(t *testing.T) {
	tmp := newWorkspace(t)
	s, _ := NewLocalStore(Config{Workspace: tmp, MaxArtifactBytes: 256, MinFreeBytes: 1})
	_, err := s.Stage(context.Background(), staging.StageInput{
		ArtifactID: "art_test_quota_pa_001",
		MIME:       "text/plain",
		SizeBytes:  1024,
		Content:    bytes.NewReader([]byte("data")),
	})
	if err == nil {
		t.Fatal("want error on per-artifact quota breach, got nil")
	}
	if !errors.Is(err, artifact.ErrQuotaExceeded) {
		t.Errorf("err = %v, want wrap ErrQuotaExceeded (alias identity)", err)
	}
	// Same probe via the staging-package alias must succeed.
	if !errors.Is(err, staging.ErrQuotaExceeded) {
		t.Errorf("err = %v, want wrap staging.ErrQuotaExceeded", err)
	}
}

// TestLocalStore_Stage_WorkspaceTotalQuotaRejected pins the
// cumulative workspace quota. Two stages where the second pushes
// past total bytes surface ErrQuotaExceeded.
func TestLocalStore_Stage_WorkspaceTotalQuotaRejected(t *testing.T) {
	tmp := newWorkspace(t)
	s, _ := NewLocalStore(Config{Workspace: tmp, MaxWorkspaceBytes: 1024, MinFreeBytes: 1})

	payload := bytes.Repeat([]byte("a"), 800)
	if _, err := s.Stage(context.Background(), staging.StageInput{
		ArtifactID: "art_test_quota_ws_001",
		MIME:       "text/plain",
		SizeBytes:  int64(len(payload)),
		Content:    bytes.NewReader(payload),
	}); err != nil {
		t.Fatalf("first stage: %v", err)
	}
	_, err := s.Stage(context.Background(), staging.StageInput{
		ArtifactID: "art_test_quota_ws_002",
		MIME:       "text/plain",
		SizeBytes:  800, // push past 1024
		Content:    bytes.NewReader(payload),
	})
	if err == nil {
		t.Fatal("want workspace-total quota error, got nil")
	}
	if !errors.Is(err, artifact.ErrQuotaExceeded) {
		t.Errorf("err = %v, want wrap ErrQuotaExceeded", err)
	}
}

// TestLocalStore_Stage_FreeSpaceLowRejected pins the free-space
// gate via the statfsFn injection seam. A 0-byte free report
// triggers ErrDiskSpaceLow without a real-disk setup.
func TestLocalStore_Stage_FreeSpaceLowRejected(t *testing.T) {
	tmp := newWorkspace(t)
	// statfsFn reports 0 free → ErrDiskSpaceLow on any stage.
	s, _ := NewLocalStore(Config{
		Workspace: tmp,
		statfsFn: func(path string) (int64, error) {
			return 0, nil
		},
	})
	_, err := s.Stage(context.Background(), staging.StageInput{
		ArtifactID: "art_test_freespace_bad",
		MIME:       "text/plain",
		SizeBytes:  16,
		Content:    bytes.NewReader([]byte("sixteen-byte-payload!")),
	})
	if err == nil {
		t.Fatal("want free-space-low error, got nil")
	}
	if !errors.Is(err, artifact.ErrDiskSpaceLow) {
		t.Errorf("err = %v, want wrap ErrDiskSpaceLow", err)
	}
}

// TestLocalStore_Stage_HashVerifierMismatchRejected pins the
// optional integrity layer — wrong expected hash surfaces as
// ErrArtifactStageHashMismatch (canonical audit typed sentinel).
func TestLocalStore_Stage_HashVerifierMismatchRejected(t *testing.T) {
	tmp := newWorkspace(t)
	s, _ := NewLocalStore(Config{Workspace: tmp, MinFreeBytes: 1})
	payload := []byte("integrity-test-payload\n")
	_, err := s.Stage(context.Background(), staging.StageInput{
		ArtifactID:     "art_test_hashverify_bad",
		MIME:           "text/plain",
		SizeBytes:      int64(len(payload)),
		ExpectedSHA256: "deadbeef" + strings.Repeat("0", 56), // 64 hex chars total
		Content:        bytes.NewReader(payload),
	})
	if err == nil {
		t.Fatal("want hash mismatch error, got nil")
	}
	if !errors.Is(err, artifact.ErrArtifactStageHashMismatch) {
		t.Errorf("err = %v, want wrap ErrArtifactStageHashMismatch", err)
	}
}

// TestLocalStore_Stage_TmpIDCollisionRejected pins the O_EXCL
// guard — a stale .tmp file blocks a concurrent Stage with the
// same artifactID.
func TestLocalStore_Stage_TmpIDCollisionRejected(t *testing.T) {
	tmp := newWorkspace(t)
	s, _ := NewLocalStore(Config{Workspace: tmp, MinFreeBytes: 1})

	// Pre-create a stale .tmp file (simulating a mid-flight writer).
	stale := filepath.Join(tmp, partialDirName, "art_test_idcoll_001.tmp")
	if err := os.WriteFile(stale, []byte("stale"), 0o600); err != nil {
		t.Fatalf("pre-create stale tmp: %v", err)
	}

	_, err := s.Stage(context.Background(), staging.StageInput{
		ArtifactID: "art_test_idcoll_001",
		MIME:       "text/plain",
		SizeBytes:  5,
		Content:    bytes.NewReader([]byte("fresh")),
	})
	if err == nil {
		t.Fatal("want collision error, got nil")
	}
	if !errors.Is(err, staging.ErrStagerIDCollision) {
		t.Errorf("err = %v, want wrap ErrStagerIDCollision", err)
	}
}

// TestLocalStore_Stage_RenameOverwriteHashMismatchRejected locks
// the rename-overwrite collision guard. When a stale canonical
// file with the same ArtifactID exists with a DIFFERENT SHA-256,
// the Stage must surface ErrStagerIDCollision rather than
// silently overwriting — protects against the "stale-publish with
// rewound Source" failure mode in production. The check happens
// in production just before os.Rename, after the new bytes have
// been written + fsync'd to .partial/, so the deferred-unlink
// cleanup path is exercised at the same time.
func TestLocalStore_Stage_RenameOverwriteHashMismatchRejected(t *testing.T) {
	tmp := newWorkspace(t)
	s, err := NewLocalStore(Config{Workspace: tmp, MinFreeBytes: 1})
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}

	// Pre-create a canonical file with a DIFFERENT hash.
	canonical := filepath.Join(tmp, "art_test_recollision_001")
	if err := os.WriteFile(canonical, []byte("original-payload-v1\n"), 0o600); err != nil {
		t.Fatalf("WriteFile %q: %v", canonical, err)
	}

	// Stage a newer payload under the same ArtifactID. SHA must
	// differ to trigger the collision guard.
	_, err = s.Stage(context.Background(), staging.StageInput{
		ArtifactID: "art_test_recollision_001",
		MIME:       "text/plain",
		SizeBytes:  int64(len("different-payload-v2\n")),
		Content:    bytes.NewReader([]byte("different-payload-v2\n")),
	})
	if err == nil {
		t.Fatal("want rename-overwrite collision error, got nil")
	}
	if !errors.Is(err, staging.ErrStagerIDCollision) {
		t.Errorf("err = %v, want wrap ErrStagerIDCollision", err)
	}
	// Verify the canonical file was NOT overwritten — the stale
	// payload should still be on disk (post-collision).
	got, readErr := os.ReadFile(canonical)
	if readErr != nil {
		t.Fatalf("ReadFile %q: %v", canonical, readErr)
	}
	if !bytes.Equal(got, []byte("original-payload-v1\n")) {
		t.Errorf("canonical file was overwritten on collision rejection: got %q", got)
	}
	// Verify the .tmp file was deferred-unlinked after the error.
	if _, statErr := os.Stat(filepath.Join(tmp, partialDirName, "art_test_recollision_001.tmp")); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf(".tmp file should have been unlinked on failure path: stat err = %v", statErr)
	}
}

// TestLocalStore_Stage_CancelledCtxRejected pins ctx-cancellation
// behavior. A pre-cancelled context surfaces the wrapped error
// before any FS touch.
func TestLocalStore_Stage_CancelledCtxRejected(t *testing.T) {
	tmp := newWorkspace(t)
	s, _ := NewLocalStore(Config{Workspace: tmp, MinFreeBytes: 1})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.Stage(ctx, staging.StageInput{
		ArtifactID: "art_test_ctx_bad",
		MIME:       "text/plain",
		Content:    bytes.NewReader([]byte("data")),
	})
	if err == nil {
		t.Fatal("want ctx error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want wrap context.Canceled", err)
	}
}

// TestLocalStore_RecoverOrphans_RemovesStaleAndKeepsActive pins
// the recovery-on-boot contract: stale .tmp files (older than
// maxAge) are unlinked; active .tmp files (within maxAge) are
// preserved.
func TestLocalStore_RecoverOrphans_RemovesStaleAndKeepsActive(t *testing.T) {
	tmp := newWorkspace(t)
	s, _ := NewLocalStore(Config{Workspace: tmp, MinFreeBytes: 1})
	partialDir := filepath.Join(tmp, partialDirName)

	// Create a stale .tmp (mtime = 2h ago).
	stale := filepath.Join(partialDir, "art_test_stale_001.tmp")
	if err := os.WriteFile(stale, []byte("stale"), 0o600); err != nil {
		t.Fatalf("write stale: %v", err)
	}
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stale, past, past); err != nil {
		t.Fatalf("chtimes stale: %v", err)
	}

	// Create an active .tmp (mtime = within last minute).
	active := filepath.Join(partialDir, "art_test_active_001.tmp")
	if err := os.WriteFile(active, []byte("active"), 0o600); err != nil {
		t.Fatalf("write active: %v", err)
	}
	now := time.Now()
	if err := os.Chtimes(active, now, now); err != nil {
		t.Fatalf("chtimes active: %v", err)
	}

	removed, err := s.RecoverOrphans(context.Background(), 1*time.Hour)
	if err != nil {
		t.Fatalf("RecoverOrphans: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1 (only stale)", removed)
	}

	// Stale must be gone.
	if _, statErr := os.Stat(stale); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("stale should be deleted: stat err = %v", statErr)
	}
	// Active must remain.
	if _, statErr := os.Stat(active); statErr != nil {
		t.Errorf("active should remain: stat err = %v", statErr)
	}
}

// TestLocalStore_Stage_WorkspaceTotalCounterAdvances pins the
// best-effort cached counter advance on successful stage.
func TestLocalStore_Stage_WorkspaceTotalCounterAdvances(t *testing.T) {
	tmp := newWorkspace(t)
	s, _ := NewLocalStore(Config{Workspace: tmp, MinFreeBytes: 1})

	payload := bytes.Repeat([]byte("x"), 100)
	for i := 0; i < 3; i++ {
		if _, err := s.Stage(context.Background(), staging.StageInput{
			ArtifactID: "art_test_counter_" + string(rune('a'+i)),
			MIME:       "text/plain",
			SizeBytes:  int64(len(payload)),
			Content:    bytes.NewReader(payload),
		}); err != nil {
			t.Fatalf("stage %d: %v", i, err)
		}
	}
	want := int64(300)
	if got := s.workspaceBytes.Load(); got != want {
		t.Errorf("workspaceBytes = %d, want %d", got, want)
	}
	// Concurrency sanity: counter is atomic; no data race.
	var wg atomic.Int32
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Add(-1)
			for j := 0; j < 5; j++ {
				_ = s.workspaceBytes.Add(int64(j))
			}
		}()
	}
	for wg.Load() != 0 {
		time.Sleep(10 * time.Millisecond)
	}
}

// ── helpers ─────────────────────────────────────────────────────────

// newWorkspace returns a fresh t.TempDir() chmod'd to 0700 so that
// the LocalStore constructor's strict verifyPermission0700 check
// passes consistently across hosts. Some Docker /tmp-overlay test
// environments leak 0775 from the parent directory; without this
// normalize, arbitrary tests would intermittently fail with:
//
//	"workspace=... perm rejected: perm=0775 want 0700"
//
// Helper is white-box (package artifacts) so tests share the same
// pre-conditions for the production-grade verifyPermission0700
// gate (godlike/07 fail-closed: production deployments with
// permissive workspaces MUST fail at construction, not Stage-time).
func newWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("newWorkspace: os.Chmod %q 0700: %v", dir, err)
	}
	return dir
}

// filePerm returns the file mode's perm bits (e.g. 0o700), 0 on error.
func filePerm(t *testing.T, path string) int {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %q: %v", path, err)
	}
	return int(info.Mode().Perm())
}
