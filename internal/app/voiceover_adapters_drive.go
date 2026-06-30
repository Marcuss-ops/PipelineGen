// Package app — voiceover Drive port adapter (Wave PR-VO-B1).
//
// voiceover.Service reaches Drive through voiceover.DriveUploaderPort
// (a narrow structural interface defined in
// internal/application/voiceover/ports.go). The production concrete
// is *drive.Uploader, which lives in internal/infrastructure/drive/.
//
// The canonical layering rule (AGENTS.md Pattern 8 + godlike-06)
// forbids infrastructure/drive from importing application/voiceover,
// and the port pattern forbids voiceover from importing
// infrastructure/drive directly. The app layer is the only place
// that can import both, so the adapter lives here — same convention
// used by clips_clipsDriveAdapter in clips_adapters_drive.go.
package app

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

// voiceoverDriveAdapter wraps *drive.Uploader so it satisfies
// voiceover.DriveUploaderPort. Today's port exposes a single method
// (DeleteFile) used by processLanguage's post-commit cleanup
// goroutine to evict the OLD voiceover's Drive file in replace-mode.
// Future ports follow the same narrow-target practice.
type voiceoverDriveAdapter struct {
	up *drive.Uploader
}

// Compile-time assertion: voiceoverDriveAdapter satisfies
// voiceover.DriveUploaderPort. Drift in either signature causes a
// build break at the gate, not a runtime panic on the first cleanup.
var _ voiceover.DriveUploaderPort = (*voiceoverDriveAdapter)(nil)

// newVoiceoverDriveAdapter wraps the production concrete. Returns
// nil when the underlying *drive.Uploader is unwired so callers can
// keep their pre-existing nil-guards (processLanguage's
// `if uploader != nil` shorthand); the adapter itself does NOT
// silently swallow nil at the port interface — if a caller
// mistakenly invokes DeleteFile on a wrapped-nil instance, they get
// an unwired-error rather than a no-op.
func newVoiceoverDriveAdapter(up *drive.Uploader) voiceover.DriveUploaderPort {
	if up == nil {
		return nil
	}
	return &voiceoverDriveAdapter{up: up}
}

// DeleteFile forwards to the concrete *drive.Uploader. voiceover
// post-commit cleanup only invokes this method through a
// context.WithoutCancel goroutine (PR-VO-A2 lesson), so the second
// ctx parameter is the cleanup context, not the request context.
func (a *voiceoverDriveAdapter) DeleteFile(ctx context.Context, fileID string) error {
	if fileID == "" {
		return fmt.Errorf("voiceoverDriveAdapter.DeleteFile: fileID is required")
	}
	if a == nil || a.up == nil {
		return fmt.Errorf("voiceoverDriveAdapter: uploader not wired")
	}
	return a.up.DeleteFile(ctx, fileID)
}
