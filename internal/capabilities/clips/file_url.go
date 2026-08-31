package clips

import "fmt"

// FileURLFromID returns the canonical Google Drive view URL for a
// file ID. Wave 14 PR2 (June 2026): replaces `driveutil.FileURLFromID`
// (referenced from internal/capabilities/assets/clips/clip_action.go) so the
// API layer no longer imports internal/platform/drive This is
// a pure string function — the legacy alias `driveutil.FileURLFromID`
// produced the same URL shape (https://drive.google.com/file/d/<id>/view)
// so callers see zero behavioral drift.
//
// The companion extractor FolderIDFromDriveLink / FileIDFromDriveLink
// live in pkg/urlutil (the existing canonical YouTube/Drive URL
// helper). This file lives here because the URL-construction helpers
// are domain-specific to clips (no other capability builds Drive URLs).
func FileURLFromID(id string) string {
	if id == "" {
		return ""
	}
	return fmt.Sprintf("https://drive.google.com/file/d/%s/view", id)
}
