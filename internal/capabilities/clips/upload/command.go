// Package upload — UploadClipCommand is the typed input for UseCase.Execute.
//
// Wave 14 step 1 (June 2026): replaces the 7-form-field reads scattered
// across clip_upload.go with a single typed DTO. The HTTP handler builds
// one of these from the multipart body + post-form fields, then calls
// UseCase.Execute(ctx, cmd) — the rest of the orchestration is hidden
// from the API layer.
package clips

import (
	"io"
)

// UploadClipCommand is the typed input for UseCase.Execute.
//
// Field semantics match the legacy multipart contract verbatim so the
// handler can map 1:1 from PostForm:
//   - File        — already-opened io.Reader (handler hands the multipart.File)
//   - Filename    — header.Filename (used as Drive filename + extension source)
//   - MimeType    — header.Header.Get("Content-Type") with fallback "video/mp4"
//   - Name        — display name (defaults to filename w/o extension when empty)
//   - Description — search text for Qdrant indexing (SearchText)
//   - Source      — source identifier (defaults to "manual" when empty)
//   - Category    — used in BuildDriveDescription + Asset.Category
//   - Group       — Drive subfolder group name; idempotently GetOrCreateFolder'd
//   - FolderID    — explicit Drive folder ID (URL or raw); supercedes root
//   - Tags        — parsed []string (handler decodes JSON/comma-separated form)
//
// The handler is responsible for input-parsing hygiene (multipart size
// cap, JSON array vs comma list, default fallbacks). The use case trusts
// the fields it receives.
type UploadClipCommand struct {
	File        io.Reader
	Filename    string
	MimeType    string
	Name        string
	Description string
	Source      string
	Category    string
	Group       string
	FolderID    string
	Tags        []string
}
