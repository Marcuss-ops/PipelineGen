package clips

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api/transport"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// BulkUploadYouTubeClipsRequest is the JSON body for POST /api/media/bulk-upload-youtube-clips.
//
// Scans a local folder (recursively by default) for .mp4 files, optionally
// paired with clip_manifest.json / transcript.txt siblings, and runs them
// through the full pipeline: Drive upload → DB record → embeddings → Qdrant.
//
// Returns a job_id immediately; poll GET /api/jobs/{id}/full for progress.
type BulkUploadYouTubeClipsRequest struct {
	LocalFolder         string   `json:"local_folder"`
	DriveFolderID       string   `json:"drive_folder_id,omitempty"`
	DriveFolderName     string   `json:"drive_folder_name,omitempty"`
	Source              string   `json:"source,omitempty"`
	Category            string   `json:"category,omitempty"`
	SubdirAsDriveSubdir *bool    `json:"subdir_as_drive_subdir,omitempty"` // default true
	Recursive           *bool    `json:"recursive,omitempty"`              // default true
	DryRun              bool     `json:"dry_run"`
	Limit               int      `json:"limit,omitempty"`
	SkipUpload          bool     `json:"skip_upload,omitempty"`
	SkipEmbeddings      bool     `json:"skip_embeddings,omitempty"`
	SkipQdrant          bool     `json:"skip_qdrant,omitempty"`
	Concurrency         int      `json:"concurrency,omitempty"` // default 2
	FilePatterns        []string `json:"file_patterns,omitempty"`
	SkipPatterns        []string `json:"skip_patterns,omitempty"`
}

// BulkUploadYouTubeClipsResponse is the immediate response after enqueueing the job.
type BulkUploadYouTubeClipsResponse struct {
	OK         bool   `json:"ok"`
	JobID      string `json:"job_id"`
	StatusURL  string `json:"status_url"`
	Message    string `json:"message"`
	DryRun     bool   `json:"dry_run"`
	LocalFound int    `json:"local_found,omitempty"`
}

// BulkUploadYouTubeClips handles POST /api/media/bulk-upload-youtube-clips.
//
// Validates the request, scans the local folder to count candidates (so the
// caller can sanity-check the scope), then enqueues a JobTypeBulkUploadYouTubeClips
// job and returns the job_id. All heavy lifting (uploads, embeddings, Qdrant)
// happens in the job worker.
func (h *Handler) BulkUploadYouTubeClips(c *gin.Context) {
	var req BulkUploadYouTubeClipsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	// Defaults
	subdirAsDriveSubdir := true
	if req.SubdirAsDriveSubdir != nil {
		subdirAsDriveSubdir = *req.SubdirAsDriveSubdir
	}
	recursive := true
	if req.Recursive != nil {
		recursive = *req.Recursive
	}
	if req.Source == "" {
		req.Source = "youtube-local"
	}
	if req.Concurrency <= 0 {
		req.Concurrency = 2
	}
	if req.Concurrency > 8 {
		req.Concurrency = 8 // sanity cap to avoid Drive rate limits
	}
	if len(req.FilePatterns) == 0 {
		req.FilePatterns = []string{"*.mp4"}
	}

	// Validation
	if strings.TrimSpace(req.LocalFolder) == "" {
		apiutil.BadRequest(c, "local_folder is required")
		return
	}
	abs, err := filepath.Abs(req.LocalFolder)
	if err != nil {
		apiutil.BadRequest(c, "invalid local_folder: "+err.Error())
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		apiutil.BadRequest(c, fmt.Sprintf("local_folder not accessible: %v", err))
		return
	}
	if !info.IsDir() {
		apiutil.BadRequest(c, "local_folder is not a directory")
		return
	}
	if req.DriveFolderID == "" && req.DriveFolderName == "" {
		apiutil.BadRequest(c, "either drive_folder_id or drive_folder_name is required")
		return
	}

	ctx := c.Request.Context()
	log := h.log.With(
		zap.String("handler", "bulk-upload-youtube-clips"),
		zap.String("local_folder", abs),
	)

	// Scan to count candidates (so the response includes a useful preview).
	candidates, scanErr := scanLocalClips(abs, recursive, req.FilePatterns, req.SkipPatterns, req.Limit)
	if scanErr != nil {
		apiutil.BadRequest(c, "failed to scan local_folder: "+scanErr.Error())
		return
	}
	log.Info("scanned local folder",
		zap.Int("candidates", len(candidates)),
		zap.Bool("dry_run", req.DryRun),
		zap.Bool("recursive", recursive))

	// Dry-run: return preview without enqueueing
	if req.DryRun {
		apiutil.OK(c, BulkUploadYouTubeClipsResponse{
			OK:         true,
			DryRun:     true,
			LocalFound: len(candidates),
			Message:    "dry run: no job enqueued, candidate count returned",
		})
		return
	}

	// Resolve target Drive folder once so the worker doesn't have to.
	targetDriveFolderID := strings.TrimSpace(req.DriveFolderID)
	if targetDriveFolderID == "" {
		if h.driveUploader == nil {
			apiutil.InternalError(c, fmt.Errorf("drive uploader not configured; drive_folder_id is required"))
			return
		}
		if req.DriveFolderName == "" {
			apiutil.BadRequest(c, "either drive_folder_id or drive_folder_name is required")
			return
		}
		root := h.cfg.Drive.ClipsFolder()
		if root == "" {
			root = h.cfg.Drive.RootFolder()
		}
		if root == "" {
			apiutil.InternalError(c, fmt.Errorf("no Drive root folder configured (drive.clips_folder / drive.root_folder)"))
			return
		}
		dirID, err := h.driveUploader.GetOrCreateFolder(ctx, req.DriveFolderName, root)
		if err != nil {
			apiutil.InternalError(c, fmt.Errorf("failed to resolve drive_folder_name: %w", err))
			return
		}
		targetDriveFolderID = dirID
		log.Info("resolved Drive folder by name",
			zap.String("name", req.DriveFolderName),
			zap.String("folder_id", targetDriveFolderID))
	}

	// Security: local_folder must be under a configured clips base path to
	// prevent the endpoint from being used to walk arbitrary directories
	// (e.g. /etc) and upload their contents to Drive.
	if !appclips.IsLocalFolderAllowed(abs, h.cfg.Storage.MediaPath(), h.cfg.Storage.TempPath(), h.cfg.Storage.DataDir) {
		apiutil.BadRequest(c, fmt.Sprintf(
			"local_folder %q is not under any allowed base path (drive.media_dir, drive.temp_dir, drive.data_dir, or a path explicitly added via config)",
			abs))
		return
	}

	// Enqueue the job
	activeKey := fmt.Sprintf("bulk_upload_yt:%s", abs)
	if ok := transport.EnqueueAsync(c, h.jobsSvc, &transport.EnqueueInput{
		Type:    "bulk_upload_youtube_clips",
		Project: "media",
		Payload: map[string]any{
			"local_folder":           abs,
			"drive_folder_id":        targetDriveFolderID,
			"source":                 req.Source,
			"category":               req.Category,
			"subdir_as_drive_subdir": subdirAsDriveSubdir,
			"recursive":              recursive,
			"limit":                  req.Limit,
			"skip_upload":            req.SkipUpload,
			"skip_embeddings":        req.SkipEmbeddings,
			"skip_qdrant":            req.SkipQdrant,
			"concurrency":            req.Concurrency,
			"file_patterns":          req.FilePatterns,
			"skip_patterns":          req.SkipPatterns,
		},
		ActiveKey: activeKey,
	}, fmt.Sprintf("bulk upload job enqueued (%d candidates)", len(candidates))); ok {
		return
	}
	// EnqueueAsync returns false if jobsSvc is nil (503) or on error.
}

// ─── Job processing ────────────────────────────────────────────────────────────

// clipCandidate describes one .mp4 found on disk ready for processing.
type clipCandidate struct {
	LocalPath  string // absolute path to the .mp4
	Subdir     string // relative subdir under the scan root ("" = root)
	Name       string // base name without extension
	Size       int64
	Manifest   map[string]any // parsed clip_manifest.json, or nil
	Transcript string         // raw transcript.txt content, or ""
}

func (c clipCandidate) DisplayName() string {
	if c.Manifest != nil {
		if n, ok := c.Manifest["name"].(string); ok && strings.TrimSpace(n) != "" {
			return strings.TrimSpace(n)
		}
		if n, ok := c.Manifest["title"].(string); ok && strings.TrimSpace(n) != "" {
			return strings.TrimSpace(n)
		}
	}
	return c.Name
}

// scanLocalClips walks a folder and returns one clipCandidate per .mp4 matching the patterns.
func scanLocalClips(root string, recursive bool, patterns, skipPatterns []string, limit int) ([]clipCandidate, error) {
	skipMatch := func(p string) bool {
		for _, s := range skipPatterns {
			if s == "" {
				continue
			}
			if strings.Contains(p, s) {
				return true
			}
		}
		return false
	}

	var candidates []clipCandidate
	walk := func(path string, info os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if info.IsDir() {
			if !recursive && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !matchesAnyPattern(info.Name(), patterns) {
			return nil
		}
		abs := path
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			rel = filepath.Base(path)
		}
		if skipMatch(abs) || skipMatch(rel) {
			return nil
		}
		fi, ferr := info.Info()
		if ferr != nil {
			return nil
		}
		cand := clipCandidate{
			LocalPath: abs,
			Subdir:    filepath.ToSlash(filepath.Dir(rel)),
			Name:      strings.TrimSuffix(info.Name(), filepath.Ext(info.Name())),
			Size:      fi.Size(),
		}
		// Look for siblings
		dir := filepath.Dir(abs)
		baseNoExt := strings.TrimSuffix(info.Name(), filepath.Ext(info.Name()))
		if mf, ok := readManifestJSON(filepath.Join(dir, "clip_manifest.json")); ok {
			cand.Manifest = mf
		}
		if txt, ok := readTextFile(filepath.Join(dir, baseNoExt+".txt")); ok {
			cand.Transcript = txt
		} else if txt, ok := readTextFile(filepath.Join(dir, "transcript.txt")); ok {
			cand.Transcript = txt
		}
		candidates = append(candidates, cand)
		if limit > 0 && len(candidates) >= limit {
			return filepath.SkipAll
		}
		return nil
	}
	if err := filepath.WalkDir(root, walk); err != nil {
		return nil, err
	}
	return candidates, nil
}

func matchesAnyPattern(name string, patterns []string) bool {
	for _, p := range patterns {
		if p == "" {
			continue
		}
		// glob match against the filename
		ok, err := filepath.Match(p, name)
		if err == nil && ok {
			return true
		}
	}
	return false
}

func readManifestJSON(path string) (map[string]any, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, false
	}
	return m, true
}

func readTextFile(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(data)), true
}
