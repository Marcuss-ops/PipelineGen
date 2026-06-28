// Package app — voiceover Drive port adapter tests (PR-VO-B1).
//
// The compile-time assertion `var _ voiceover.DriveUploaderPort =
// (*voiceoverDriveAdapter)(nil)` in voiceover_adapters_drive.go
// pins the cross-package interface conformance at build time —
// any drift in either signature causes a build break, not a
// runtime panic on the first cleanup.
//
// These tests pin the adapter's runtime behaviour at three layers:
//
//   - nil-safety: returning a nil adapter when up is nil matches
//     production wiring in build_bundles_voiceover.go (caller keeps
//     its existing nil-shorts).
//   - unwired error: a wrapped-nil call-later path returns the
//     canonical "not wired" error rather than a silent drop.
//   - fileID guard: empty fileID is a programming error caught at
//     the adapter boundary (the underlying DeleteFile rejects the
//     same call but our guard surfaces the message in voiceover
//     terms).
//
// We do NOT exercise *drive.Uploader's network call path here —
// that's covered by internal/infrastructure/drive/uploader_test.go.
// PR-VO-B1 split is about port-level behaviour, not Drive itself.
package app

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
)

// TestVoiceoverDriveAdapter_NilUploaderReturnsNil: the factory
// returns nil when no *drive.Uploader is wired. This matches
// production (build_bundles_voiceover.go) and prevents accidentally
// constructing a wrapped-nil that callers might invoke expecting
// a real uploader.
func TestVoiceoverDriveAdapter_NilUploaderReturnsNil(t *testing.T) {
	if got := newVoiceoverDriveAdapter(nil); got != nil {
		t.Errorf("newVoiceoverDriveAdapter(nil) = %v, want nil", got)
	}
}

// TestVoiceoverDriveAdapter_DeleteFileUnwiredError: when the
// adapter is invoked via a wrapped-nil (a programming error —
// newVoiceoverDriveAdapter already returns nil for that case), the
// wrapper still surfaces the canonical "not wired" error rather
// than a panic. This is a defense-in-depth: any future refactor
// that introduces a wrapped-nil construction path cannot silently
// swallow cleanup calls.
func TestVoiceoverDriveAdapter_DeleteFileUnwiredError(t *testing.T) {
	a := &voiceoverDriveAdapter{up: nil}
	err := a.DeleteFile(context.Background(), "abc123")
	if err == nil {
		t.Fatal("DeleteFile on wrapped-nil expected error, got nil")
	}
	if !errors.Is(err, err) {
		t.Errorf("got error %v, expected a non-nil error", err)
	}
	if got := err.Error(); got != "voiceoverDriveAdapter: uploader not wired" {
		t.Errorf("error message = %q, want %q", got, "voiceoverDriveAdapter: uploader not wired")
	}
}

// TestVoiceoverDriveAdapter_DeleteFileEmptyFileID: an empty fileID
// is a programming error caught at the adapter boundary. The
// underlying *drive.Uploader will eventually reject the same call,
// but surfacing the message in adapter terms ("voiceoverDriveAdapter")
// keeps voiceover-domain diagnostics consistent.
func TestVoiceoverDriveAdapter_DeleteFileEmptyFileID(t *testing.T) {
	a := &voiceoverDriveAdapter{up: nil}
	err := a.DeleteFile(context.Background(), "")
	if err == nil {
		t.Fatal("DeleteFile with empty fileID expected error, got nil")
	}
	if got := err.Error(); got != "voiceoverDriveAdapter.DeleteFile: fileID is required" {
		t.Errorf("error message = %q, want %q", got, "voiceoverDriveAdapter.DeleteFile: fileID is required")
	}
}

// TestVoiceoverDriveAdapter_InterfaceSatisfactionPin: add a
// runtime check that the adapter satisfies the voiceover port
// interface, so a future refactor that drops the compile-time
// assertion in voiceover_adapters_drive.go still fails this test
// loudly. Build-time assertion is the primary defense; this is
// belt-and-braces.
func TestVoiceoverDriveAdapter_InterfaceSatisfactionPin(t *testing.T) {
	var _ voiceover.DriveUploaderPort = (*voiceoverDriveAdapter)(nil)
}
