// Package drive — uploader_put_test.go (Wave B1 + Wave B2 regression, June 2026).
//
// Tests pin the FAIL-CLOSED semantics of *Uploader.PutFile on TWO axes:
//
//	Wave B1 — lookup *error* is hard-failed (no warn-and-fall-through
//	          that previously produced silent Drive file duplicates on
//	          transient lookup failures).
//	Wave B2 — lookup *ambiguity* (more than one non-trashed match for
//	          the same name+parent) is hard-failed via the typed
//	          sentinel ErrAmbiguousDriveFile. Pre-Wave B2 the
//	          first-match truncation silently hid this case, which
//	          made overwrite/skip non-deterministic on sibling copies.
//
// The lookupFunc package-level seam mirrors the existing openFile seam
// in uploader.go / uploader_test.go::TestOpenFileInjection — override
// + restore in t.Cleanup, no constructor changes required.
package drive

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"
	"google.golang.org/api/option"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
)

// newFakeDriveService constructs a *driveapi.Service that is NEVER
// exercised (PutFile's lookup-fail tests short-circuit before any
// Drive API call). This avoids GCP auth setup while still passing
// the PutFile guard `if u.Service == nil { return nil, ... }`.
//
// The endpoint URL is intentionally non-routable ([::1]:1) so even
// an accidental Service.Files.* call by a future test-regression
// would fail-fast with a connection error rather than a slow timeout.
func newFakeDriveService(t *testing.T) *driveapi.Service {
	t.Helper()
	srv, err := driveapi.NewService(context.Background(),
		option.WithoutAuthentication(),
		option.WithEndpoint("http://[::1]:1"),
	)
	if err != nil {
		t.Fatalf("driveapi.NewService: %v", err)
	}
	return srv
}

// TestPutFileLookupErrorFailClosed is the Wave B1 regression anchor.
// Pre-Wave B1, the lookup-error branch at uploader_put.go logged a
// warn and proceeded with `existing == nil`, leading to a duplicate
// Create call. Post-Wave B1, the lookup error is wrapped and
// returned hard — putting the duplicate-create race beyond the call
// boundary.
//
// Ordered invariants asserted (ordered most-important first):
//  1. PutFile returns a non-nil error when lookupFunc returns one.
//  2. The returned error has the canonical prefix
//     "putFile: lookup existing file %q" so callers reading the
//     audit trail can pattern-match the failure mode.
//  3. errors.Is(err, simulatedErr) returns true: the underlying
//     error is preserved via fmt.Errorf %w so callers can
//     pattern-match on the inner Drive-side error too (e.g.
//     `gapi.Error` 429 / 503).
func TestPutFileLookupErrorFailClosed(t *testing.T) {
	// Restore package-level seam at test end.
	origLookup := lookupFunc
	t.Cleanup(func() { lookupFunc = origLookup })

	simulatedErr := errors.New("simulated Drive API outage")
	var lookupCalls int
	lookupFunc = func(_ *Uploader, _ context.Context, _, _ string) (ExistingFileLookup, error) {
		lookupCalls++
		return ExistingFileLookup{}, simulatedErr
	}

	u := &Uploader{
		Service: newFakeDriveService(t),
		Log:     zap.NewNop(),
	}

	// Bounded ctx so any future regression that mistakenly retries
	// can't hang the test forever.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := u.PutFile(ctx, PutFileRequest{
		LocalPath:      "/nonexistent/test.mp4",
		FolderID:       "folder123",
		Filename:       "test.mp4",
		ConflictPolicy: delivery.ConflictOverwrite,
	})

	// 1. Non-nil error.
	if err == nil {
		t.Fatal("expected error when lookupFunc returns error, got nil")
	}

	// 2. Canonical wrapped prefix.
	if !strings.Contains(err.Error(), "putFile: lookup existing file") {
		t.Errorf("expected wrapped 'putFile: lookup existing file' prefix, got: %v", err)
	}
	if !strings.Contains(err.Error(), `"test.mp4"`) {
		t.Errorf("expected filename in wrapped error (for audit triage), got: %v", err)
	}

	// 3. Inner error preserved via %w.
	if !errors.Is(err, simulatedErr) {
		t.Errorf("expected errors.Is to surface simulatedErr through wrap, got: %v", err)
	}

	// Sanity: the wrapped error is non-retryable (no 429/503 token)
	// so pkg/retry's IsRetryable predicate returns false → exactly
	// ONE lookupFunc call, not three.
	if lookupCalls != 1 {
		t.Errorf("non-retryable lookup error should fire exactly once, got %d calls", lookupCalls)
	}
}

// TestPutFileAmbiguousMatchError is the Wave B2 regression anchor.
// Pre-Wave B2, FindFileByName returned only the first match — silently
// truncating the second/third/... matches. A user who manually
// uploaded a sibling copy with the same name+parent would trigger
// non-deterministic overwrite/skip behaviour. Post-Wave B2 the
// surface is exhaustive (ExistingFileLookup.Matches) and PutFile
// fail-closes on len > 1 with the typed sentinel ErrAmbiguousDriveFile.
//
// Ordered invariants asserted (ordered most-important first):
//  1. PutFile returns a non-nil error when lookupFunc reports >1
//     match.
//  2. errors.Is(err, ErrAmbiguousDriveFile) returns true so callers
//     can branch on the ambiguous-state case specifically (vs a
//     generic lookup error which is wrapped with %w too but
//     errors.Is to a different sentinel).
//  3. The wrapped prefix "putFile: lookup existing file" is present
//     for audit trail parity with the Wave B1 fail-closed path.
func TestPutFileAmbiguousMatchError(t *testing.T) {
	// Restore package-level seam at test end.
	origLookup := lookupFunc
	t.Cleanup(func() { lookupFunc = origLookup })

	// Two sibling matches. The field values are zeroed — the
	// fail-closed guard fires on len, not on the contents, so
	// zero-value RemoteFile entries are sufficient to drive the
	// >1 branch.
	lookupFunc = func(_ *Uploader, _ context.Context, _, _ string) (ExistingFileLookup, error) {
		return ExistingFileLookup{
			Matches: []RemoteFile{
				{FileID: "id-A", Name: "test.mp4"},
				{FileID: "id-B", Name: "test.mp4"},
			},
		}, nil
	}

	u := &Uploader{
		Service: newFakeDriveService(t),
		Log:     zap.NewNop(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := u.PutFile(ctx, PutFileRequest{
		LocalPath:      "/nonexistent/test.mp4",
		FolderID:       "folder123",
		Filename:       "test.mp4",
		ConflictPolicy: delivery.ConflictOverwrite,
	})

	// 1. Non-nil error.
	if err == nil {
		t.Fatal("expected error on ambiguous match, got nil")
	}

	// 2. errors.Is surfaces ErrAmbiguousDriveFile specifically.
	if !errors.Is(err, ErrAmbiguousDriveFile) {
		t.Errorf("expected errors.Is to surface ErrAmbiguousDriveFile, got: %v", err)
	}

	// 3. Canonical wrapped prefix (same as Wave B1 fail-closed path,
	//    for audit-trail consistency).
	if !strings.Contains(err.Error(), "putFile: lookup existing file") {
		t.Errorf("expected wrapped 'putFile: lookup existing file' prefix, got: %v", err)
	}
	if !strings.Contains(err.Error(), `"test.mp4"`) {
		t.Errorf("expected filename in wrapped error, got: %v", err)
	}
}

// TestPutFileValidatesServiceUnchanged pins the Surface invariant
// from godlike/06 §1: Service=nil must still hard-fail BEFORE the
// lookup step. Otherwise a misconfigured composition root
// (composition-time nil panic) would explode at the lookup seam
// instead of surfacing a typed "drive service not configured"
// error to the caller.
//
// Pre-Wave B1: this was tested implicitly via Uploader.UploadFile in
// uploader_test.go::TestUploaderValidatesService. Post-Wave B1 the
// CanPutFile reuses the SAME Service=nil guard so the surface is
// symmetric across PutFile / UploadFile.
func TestPutFileValidatesServiceUnchanged(t *testing.T) {
	u := &Uploader{Service: nil, Log: zap.NewNop()}

	// Bypass the lookupFunc seam entirely (it's never reached
	// because Service=nil fails before the retry wrapper).
	_, err := u.PutFile(context.Background(), PutFileRequest{
		LocalPath:      "/nonexistent/test.mp4",
		FolderID:       "folder123",
		Filename:       "test.mp4",
		ConflictPolicy: delivery.ConflictOverwrite,
	})
	if err == nil || !strings.Contains(err.Error(), "drive service not configured") {
		t.Errorf("expected 'drive service not configured', got: %v", err)
	}
}
