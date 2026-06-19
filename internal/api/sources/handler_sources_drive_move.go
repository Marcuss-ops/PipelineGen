package sources

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	textutil "github.com/Marcuss-ops/PipelineGen/internal/platform"
)

// MoveDriveFilesRequest is the payload for POST /api/media/drive/move-files.
type MoveDriveFilesRequest struct {
	SourceFolderID string `json:"source_folder_id" binding:"required"`
	DestFolderID   string `json:"dest_folder_id" binding:"required"`
	DryRun         bool   `json:"dry_run"`
}

// MoveDriveFilesResponse reports which files were moved (or would be moved).
type MoveDriveFilesResponse struct {
	Moved   []string `json:"moved"`
	Skipped []string `json:"skipped"`
	Errors  []string `json:"errors"`
}

// MoveDriveFiles lists all files in source_folder_id, checks which ones are
// already in dest_folder_id (by name), and moves the missing ones.
// Use dry_run=true to preview without actually moving.
func (h *Handler) MoveDriveFiles(c *gin.Context) {
	var req MoveDriveFilesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	log := h.log.With(
		zap.String("handler", "move-drive-files"),
		zap.String("source", req.SourceFolderID),
		zap.String("dest", req.DestFolderID),
	)

	// 1. List files in source folder
	srcFiles, err := h.driveUploader.ListFiles(ctx, req.SourceFolderID)
	if err != nil {
		log.Error("failed to list source folder", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list source folder: " + err.Error()})
		return
	}
	log.Info("listed source folder", zap.Int("count", len(srcFiles)))

	// 2. List files in dest folder
	destFiles, err := h.driveUploader.ListFiles(ctx, req.DestFolderID)
	if err != nil {
		log.Error("failed to list dest folder", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list dest folder: " + err.Error()})
		return
	}
	destNames := make(map[string]bool, len(destFiles))
	for _, f := range destFiles {
		destNames[f.Name] = true
	}

	// 3. Filter: only move video files not already in dest
	var toMove []struct{ ID, Name string }
	var skipped []string
	for _, f := range srcFiles {
		if f.MimeType != "video/mp4" && f.MimeType != "video/webm" {
			continue
		}
		if destNames[f.Name] {
			skipped = append(skipped, f.Name)
			continue
		}
		toMove = append(toMove, struct{ ID, Name string }{f.ID, f.Name})
	}

	log.Info("files to move",
		zap.Int("total_src", len(srcFiles)),
		zap.Int("to_move", len(toMove)),
		zap.Int("skipped", len(skipped)),
	)

	if req.DryRun {
		names := make([]string, len(toMove))
		for i, f := range toMove {
			names[i] = f.Name
		}
		c.JSON(http.StatusOK, MoveDriveFilesResponse{
			Moved:   names,
			Skipped: skipped,
			Errors:  nil,
		})
		return
	}

	// 4. Move files
	var moved []string
	var moveErrors []string
	for _, f := range toMove {
		if err := h.driveUploader.MoveFile(ctx, f.ID, req.SourceFolderID, req.DestFolderID); err != nil {
			log.Error("failed to move file",
				zap.String("file_id", f.ID),
				zap.String("name", f.Name),
				zap.Error(err),
			)
			moveErrors = append(moveErrors, f.Name+": "+err.Error())
			continue
		}
		moved = append(moved, f.Name)
		log.Info("moved file", zap.String("name", f.Name))
	}

	c.JSON(http.StatusOK, MoveDriveFilesResponse{
		Moved:   moved,
		Skipped: skipped,
		Errors:  moveErrors,
	})
}

// CreateDriveFoldersRequest is the payload for POST /api/media/drive/create-folders.
type CreateDriveFoldersRequest struct {
	ParentFolderID string   `json:"parent_folder_id" binding:"required"`
	FolderNames    []string `json:"folder_names" binding:"required,min=1"`
}

// CreateDriveFoldersResponse reports which folders were created.
type CreateDriveFoldersResponse struct {
	Created []CreatedFolder `json:"created"`
	Errors  []string        `json:"errors"`
}

// CreatedFolder is a single created folder result.
type CreatedFolder struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

// CreateDriveFolders creates multiple subfolders inside a parent folder.
// Skips folders that already exist (by name). Returns folder IDs.
func (h *Handler) CreateDriveFolders(c *gin.Context) {
	var req CreateDriveFoldersRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	log := h.log.With(
		zap.String("handler", "create-drive-folders"),
		zap.String("parent", req.ParentFolderID),
	)

	// List existing folders to avoid duplicates
	existing, err := h.driveUploader.ListFiles(ctx, req.ParentFolderID)
	if err != nil {
		log.Error("failed to list parent folder", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list parent folder: " + err.Error()})
		return
	}
	existingNames := make(map[string]bool, len(existing))
	for _, f := range existing {
		existingNames[f.Name] = true
	}

	var created []CreatedFolder
	var createErrors []string
	for _, name := range req.FolderNames {
		if existingNames[name] {
			// Find the existing folder ID
			for _, f := range existing {
				if f.Name == name {
					created = append(created, CreatedFolder{Name: name, ID: f.ID})
					log.Info("folder already exists", zap.String("name", name), zap.String("id", f.ID))
					break
				}
			}
			continue
		}

		folderID, err := h.driveUploader.GetOrCreateFolder(ctx, name, req.ParentFolderID)
		if err != nil {
			log.Error("failed to create folder", zap.String("name", name), zap.Error(err))
			createErrors = append(createErrors, name+": "+err.Error())
			continue
		}
		created = append(created, CreatedFolder{Name: name, ID: folderID})
		log.Info("created folder", zap.String("name", name), zap.String("id", folderID))
	}

	c.JSON(http.StatusOK, CreateDriveFoldersResponse{
		Created: created,
		Errors:  createErrors,
	})
}

// SyncToSubfoldersRequest is the payload for POST /api/media/drive/sync-to-subfolders.
type SyncToSubfoldersRequest struct {
	ParentFolderID string `json:"parent_folder_id" binding:"required"`
	DryRun         bool   `json:"dry_run"`
}

// SyncToSubfoldersResponse reports which files were moved.
type SyncToSubfoldersResponse struct {
	Moved  []string `json:"moved"`
	Errors []string `json:"errors"`
}

// SyncToSubfolders finds video files directly in parent_folder_id (not inside
// any subfolder), creates a per-video subfolder (videoID-title-slug), and moves
// the file into it. Also moves matching metadata.json files.
func (h *Handler) SyncToSubfolders(c *gin.Context) {
	var req SyncToSubfoldersRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	log := h.log.With(
		zap.String("handler", "sync-to-subfolders"),
		zap.String("parent", req.ParentFolderID),
	)

	// 1. List all items in the parent folder
	allItems, err := h.driveUploader.ListFiles(ctx, req.ParentFolderID)
	if err != nil {
		log.Error("failed to list parent folder", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list parent folder: " + err.Error()})
		return
	}

	// 2. Separate: video files (to move) vs subfolders (already organized)
	var videoFiles []struct{ ID, Name string }
	for _, f := range allItems {
		if f.MimeType == "video/mp4" || f.MimeType == "video/webm" {
			videoFiles = append(videoFiles, struct{ ID, Name string }{f.ID, f.Name})
		}
	}

	log.Info("found video files to organize", zap.Int("count", len(videoFiles)))

	if req.DryRun {
		names := make([]string, len(videoFiles))
		for i, f := range videoFiles {
			names[i] = f.Name
		}
		c.JSON(http.StatusOK, SyncToSubfoldersResponse{Moved: names})
		return
	}

	// 3. For each video file, create subfolder and move
	var moved []string
	var moveErrors []string
	for _, f := range videoFiles {
		// Extract video ID from filename (format: "videoID - title.mp4" or "videoID-title.mp4")
		parts := strings.SplitN(f.Name, " - ", 2)
		if len(parts) == 0 {
			moveErrors = append(moveErrors, f.Name+": cannot parse video ID")
			continue
		}
		videoID := strings.TrimSpace(parts[0])
		titleSlug := ""
		if len(parts) > 1 {
			// Remove extension and slugify
			nameWithoutExt := strings.TrimSuffix(parts[1], ".mp4")
			nameWithoutExt = strings.TrimSuffix(nameWithoutExt, ".webm")
			titleSlug = textutil.SlugifyWithMax(nameWithoutExt, 60)
		}

		// Build subfolder name
		slug := videoID
		if titleSlug != "" {
			slug = videoID + "-" + titleSlug
		}

		// Create subfolder
		folderID, err := h.driveUploader.GetOrCreateFolder(ctx, slug, req.ParentFolderID)
		if err != nil {
			log.Error("failed to create subfolder", zap.String("slug", slug), zap.Error(err))
			moveErrors = append(moveErrors, f.Name+": "+err.Error())
			continue
		}

		// Move video file
		if err := h.driveUploader.MoveFile(ctx, f.ID, req.ParentFolderID, folderID); err != nil {
			log.Error("failed to move video", zap.String("name", f.Name), zap.Error(err))
			moveErrors = append(moveErrors, f.Name+": "+err.Error())
			continue
		}

		// Also move metadata.json if it exists in parent
		for _, item := range allItems {
			if item.Name == "metadata.json" {
				_ = h.driveUploader.MoveFile(ctx, item.ID, req.ParentFolderID, folderID)
				break
			}
		}

		moved = append(moved, f.Name)
		log.Info("moved video to subfolder", zap.String("name", f.Name), zap.String("slug", slug))
	}

	c.JSON(http.StatusOK, SyncToSubfoldersResponse{
		Moved:  moved,
		Errors: moveErrors,
	})
}
