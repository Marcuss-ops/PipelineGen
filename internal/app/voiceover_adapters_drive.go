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

// voiceoverDriveAdapter wraps drive.Admin so it satisfies
// voiceover.DriveUploaderPort. FASE 9 Step 4 (June 2026): migrated
// from *drive.Uploader to drive.Admin (Pattern 0 port).
// Today's port exposes a single method (DeleteFile) used by
// processLanguage's post-commit cleanup goroutine to evict the
// OLD voiceover's Drive file in replace-mode.
type voiceoverDriveAdapter struct {
	drive drive.Admin
}

// Compile-time assertion: voiceoverDriveAdapter satisfies
// voiceover.DriveUploaderPort.
var _ voiceover.DriveUploaderPort = (*voiceoverDriveAdapter)(nil)

// newVoiceoverDriveAdapter wraps the production port. Returns nil
// when the underlying drive.Admin is unwired so callers can keep
// their pre-existing nil-guards.
func newVoiceoverDriveAdapter(admin drive.Admin) voiceover.DriveUploaderPort {
	if admin == nil {
		return nil
	}
	return &voiceoverDriveAdapter{drive: admin}
}

func (a *voiceoverDriveAdapter) DeleteFile(ctx context.Context, fileID string) error {
	if fileID == "" {
		return fmt.Errorf("voiceoverDriveAdapter.DeleteFile: fileID is required")
	}
	if a == nil || a.drive == nil {
		return fmt.Errorf("voiceoverDriveAdapter: drive not wired")
	}
	return a.drive.DeleteFile(ctx, fileID)
}
