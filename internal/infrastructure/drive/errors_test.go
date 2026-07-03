package drive

import (
	"errors"
	"fmt"
	"testing"
)

// TestErrLegacySurfaceRetired exists as a sanity pin so future renames
// of the sentinel (or message drift) surface as a test failure rather
// than a downstream consumer's silent compile error. Mirrors the
// TestGoogleAPIError_SentinelsLocked surface in pkg/retry (P1.5).
func TestErrLegacySurfaceRetired_Exists(t *testing.T) {
	if ErrLegacySurfaceRetired == nil {
		t.Fatal("ErrLegacySurfaceRetired is nil; sentinel must be a non-nil errors.New value (godlike/07 typed-error contract)")
	}
	want := "legacy drive upload surface retired: use delivery.Publisher.Publish (DRIVE-008)"
	if got := ErrLegacySurfaceRetired.Error(); got != want {
		t.Errorf("message drift: got %q, want %q", got, want)
	}
}

// TestErrLegacySurfaceRetired_ErrorsIsProbe verifies errors.Is is
// probeable through the %w wrap chain callers use at the composition-
// root adapter layer. The shim wraps the sentinel with
// fmt.Errorf("... retired by DRIVE-008: %w", ErrLegacySurfaceRetired);
// downstream callers must be able to detect the retired surface via
// errors.Is even after 1+ wrapping layers.
func TestErrLegacySurfaceRetired_ErrorsIsProbe(t *testing.T) {
	wrapped := fmt.Errorf("clipsDriveAdapter.UploadFile(localPath=%q folderID=%q filename=%q) retired by DRIVE-008: %w", "/tmp/clip.mp4", "folderX", "clip.mp4", ErrLegacySurfaceRetired)
	if !errors.Is(wrapped, ErrLegacySurfaceRetired) {
		t.Errorf("errors.Is through %%w wrap did not resolve to ErrLegacySurfaceRetired; got chain: %v", wrapped)
	}
	// Double-wrap (handler-level aggregation layer) must still probe.
	double := fmt.Errorf("handler-layer: %w", wrapped)
	if !errors.Is(double, ErrLegacySurfaceRetired) {
		t.Errorf("errors.Is through 2x %%w wrap did not resolve to ErrLegacySurfaceRetired; got chain: %v", double)
	}
}
