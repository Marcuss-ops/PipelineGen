package assets

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	appupload "github.com/Marcuss-ops/PipelineGen/internal/application/clips/upload"

	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
)

// UploadVideoClipResponse is returned after a successful video upload.
type UploadVideoClipResponse struct {
	OK            bool     `json:"ok"`
	ClipID        string   `json:"clip_id"`
	Name          string   `json:"name"`
	Filename      string   `json:"filename"`
	DriveLink     string   `json:"drive_link,omitempty"`
	DriveFileID   string   `json:"drive_file_id,omitempty"`
	LegacyFileMD5 string   `json:"legacy_file_md5"`
	Source        string   `json:"source"`
	Category      string   `json:"category,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	LocalPath     string   `json:"local_path"`
	Indexed       bool     `json:"indexed"`
	Duration      int      `json:"duration,omitempty"`
}

// UploadVideoClip handles POST /api/media/upload-video
// Accepts multipart form data with a video file and metadata fields.
//
// P1.5 CUTOVER (June 2026): the 10-step orchestration previously inlined
// here has been extracted into internal/application/clips/upload/UseCase.
// The handler is now thin transport only (AGENTS.md Pattern 8): it parses
// the multipart form, builds an UploadClipCommand, calls uploadUC.Execute,
// and maps the result to the JSON response.
//
// Form fields:
//   - file:       (required) the video file
//   - name:       clip name (defaults to filename without extension)
//   - description: description / search text for Qdrant indexing
//   - tags:       JSON array of tags, e.g. ["funny","interview"]
//   - source:     source identifier (default "manual")
//   - category:   category
//   - group:      Drive subfolder group name
//   - folder_id:  Drive folder ID (if omitted, uses configured default root)
func (ih *IngestHandler) UploadVideoClip(c *gin.Context) {
	// 1. Parse multipart form (max 500MB)
	if err := c.Request.ParseMultipartForm(500 << 20); err != nil {
		apiutil.BadRequest(c, fmt.Sprintf("failed to parse multipart form: %v", err))
		return
	}

	// 2. Get the video file
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		apiutil.BadRequest(c, "file field is required: "+err.Error())
		return
	}
	defer file.Close()

	// 3. Parse metadata from form fields
	name := strings.TrimSpace(c.PostForm("name"))
	description := strings.TrimSpace(c.PostForm("description"))
	source := strings.TrimSpace(c.PostForm("source"))
	category := strings.TrimSpace(c.PostForm("category"))
	group := strings.TrimSpace(c.PostForm("group"))
	folderID := strings.TrimSpace(c.PostForm("folder_id"))

	// Parse tags as JSON array (fallback: comma-separated)
	var tags []string
	if tagsStr := c.PostForm("tags"); tagsStr != "" {
		if err := json.Unmarshal([]byte(tagsStr), &tags); err != nil {
			for _, t := range strings.Split(tagsStr, ",") {
				if trimmed := strings.TrimSpace(t); trimmed != "" {
					tags = append(tags, trimmed)
				}
			}
		}
	}

	if source == "" {
		source = "manual"
	}
	if name == "" {
		name = strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename))
	}

	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "video/mp4"
	}

	// 4. P1.5 CUTOVER: delegate to upload.UseCase.Execute.
	// The use case absorbs the 10-step pipeline (artifact staging,
	// Drive folder resolve, upload, metadata, asset construction,
	// ffprobe, dispatcher, tree, job enqueue). Handler is thin
	// transport only — if the use case is nil (test fixture wiring
	// gap), surface a clear error.
	uc := ih.uploadUC
	if uc == nil {
		apiutil.InternalError(c, fmt.Errorf("upload use case not wired"))
		return
	}

	result, err := uc.Execute(c.Request.Context(), appupload.UploadClipCommand{
		File:        file,
		Filename:    header.Filename,
		MimeType:    mimeType,
		Name:        name,
		Description: description,
		Tags:        tags,
		Source:      source,
		Category:    category,
		Group:       group,
		FolderID:    folderID,
	})
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	// 5. Map use-case result to legacy response envelope
	apiutil.OK(c, UploadVideoClipResponse{
		OK:            result.OK,
		ClipID:        result.ClipID,
		Name:          result.Name,
		Filename:      result.Filename,
		DriveLink:     result.DriveLink,
		DriveFileID:   result.DriveFileID,
		LegacyFileMD5: result.LegacyFileMD5,
		Source:        result.Source,
		Category:      result.Category,
		Tags:          result.Tags,
		LocalPath:     result.LocalPath,
		Indexed:       result.Indexed,
		Duration:      result.Duration,
	})
}
