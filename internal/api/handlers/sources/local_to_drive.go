package sources

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	jobservice "github.com/Marcuss-ops/PipelineGen/internal/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

type LocalToDriveRequest struct {
	LocalFolder   string `json:"local_folder"`
	DriveFolderID string `json:"drive_folder_id"`
	Source        string `json:"source,omitempty"`
	Limit         int    `json:"limit,omitempty"`
	Concurrency   int    `json:"concurrency,omitempty"`
	DryRun        bool   `json:"dry_run"`
}

type LocalToDriveResponse struct {
	OK         bool     `json:"ok"`
	JobID      string   `json:"job_id"`
	Message    string   `json:"message"`
	DryRun     bool     `json:"dry_run"`
	LocalFound int      `json:"local_found,omitempty"`
	Actors     []string `json:"actors,omitempty"`
}

func (h *Handler) LocalToDrive(c *gin.Context) {
	var req LocalToDriveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request: "+err.Error())
		return
	}

	abs, err := filepath.Abs(req.LocalFolder)
	if err != nil {
		apiutil.BadRequest(c, "invalid local_folder: "+err.Error())
		return
	}
	if _, err := os.Stat(abs); err != nil {
		apiutil.BadRequest(c, fmt.Sprintf("local_folder not accessible: %v", err))
		return
	}
	if strings.TrimSpace(req.DriveFolderID) == "" {
		apiutil.BadRequest(c, "drive_folder_id is required")
		return
	}

	candidates, err := scanLocalMp4(abs, req.Limit)
	if err != nil {
		apiutil.BadRequest(c, "scan failed: "+err.Error())
		return
	}

	// Group by actor name (first subdir level)
	actorMap := groupByActorLocal(candidates)
	actorNames := make([]string, 0, len(actorMap))
	for name := range actorMap {
		actorNames = append(actorNames, name)
	}

	h.log.Info("scanned local folder",
		zap.Int("clips", len(candidates)),
		zap.Int("actors", len(actorNames)),
		zap.Bool("dry_run", req.DryRun))

	if req.DryRun {
		apiutil.OK(c, LocalToDriveResponse{
			OK:         true,
			DryRun:     true,
			LocalFound: len(candidates),
			Actors:     actorNames,
		})
		return
	}

	if h.jobsSvc == nil {
		apiutil.InternalError(c, fmt.Errorf("jobs service not available"))
		return
	}

	source := req.Source
	if source == "" {
		source = "youtube-local"
	}
	conc := req.Concurrency
	if conc <= 0 {
		conc = 3
	}

	payload := map[string]any{
		"local_folder":           abs,
		"drive_folder_id":        strings.TrimSpace(req.DriveFolderID),
		"source":                 source,
		"subdir_as_drive_subdir": true,
		"recursive":              true,
		"concurrency":            conc,
		"limit":                  req.Limit,
		"file_patterns":          []string{"*.mp4"},
	}

	job, err := h.jobsSvc.Enqueue(c.Request.Context(), &jobservice.EnqueueRequest{
		Type:    models.JobTypeBulkUploadYouTubeClips,
		Project: "media",
		Payload: payload,
	})
	if err != nil {
		apiutil.InternalError(c, fmt.Errorf("enqueue: %w", err))
		return
	}

	apiutil.OK(c, LocalToDriveResponse{
		OK:         true,
		JobID:      job.ID,
		Message:    fmt.Sprintf("job enqueued (%d clips, %d actors)", len(candidates), len(actorNames)),
		LocalFound: len(candidates),
		Actors:     actorNames,
	})
}

type localClip struct {
	LocalPath    string
	RelPath      string
	Name         string
	ActorName    string
	Size         int64
	MetadataPath string
	Transcript   string
}

func scanLocalMp4(root string, limit int) ([]localClip, error) {
	var out []localClip
	walk := func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".mp4") {
			return nil
		}
		if limit > 0 && len(out) >= limit {
			return filepath.SkipAll
		}

		rel, _ := filepath.Rel(root, path)
		base := strings.TrimSuffix(d.Name(), filepath.Ext(d.Name()))
		dir := filepath.Dir(path)

		// Extract actor name from first subdir
		parts := strings.Split(filepath.ToSlash(rel), "/")
		actorName := ""
		if len(parts) > 1 {
			actorName = parts[0]
		}

		// Look for metadata sibling
		metaPath := ""
		for _, candidate := range []string{
			filepath.Join(dir, "metadata_"+base+".json"),
			filepath.Join(dir, base+".metadata.json"),
			filepath.Join(dir, "metadata.json"),
		} {
			if _, e := os.Stat(candidate); e == nil {
				metaPath = candidate
				break
			}
		}

		// Look for transcript sibling
		transcript := ""
		for _, candidate := range []string{
			filepath.Join(dir, base+".txt"),
			filepath.Join(dir, "transcript.txt"),
		} {
			if data, e := os.ReadFile(candidate); e == nil {
				transcript = string(data)
				break
			}
		}

		fi, _ := d.Info()
		out = append(out, localClip{
			LocalPath:    path,
			RelPath:      filepath.ToSlash(rel),
			Name:         base,
			ActorName:    actorName,
			Size:         fi.Size(),
			MetadataPath: metaPath,
			Transcript:   transcript,
		})
		return nil
	}
	if err := filepath.WalkDir(root, walk); err != nil {
		return nil, err
	}
	return out, nil
}

func groupByActorLocal(clips []localClip) map[string][]localClip {
	groups := make(map[string][]localClip)
	for _, c := range clips {
		actor := c.ActorName
		if actor == "" {
			actor = "uncategorized"
		}
		groups[actor] = append(groups[actor], c)
	}
	return groups
}

func loadMetaFromFile(clip *models.MediaAsset, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var meta map[string]any
	if err := json.Unmarshal(data, &meta); err != nil {
		return
	}
	if clip.Metadata == nil {
		clip.Metadata = make(map[string]any)
	}
	for k, v := range meta {
		clip.Metadata[k] = v
	}
	if v, ok := meta["clean_title"].(string); ok && v != "" {
		clip.Name = v
	}
	if v, ok := meta["youtube_video_id"].(string); ok {
		clip.SetMetadataString("youtube_video_id", v)
	}
	if v, ok := meta["youtube_url"].(string); ok {
		clip.SetMetadataString("youtube_url", v)
	}
	if v, ok := meta["youtube_title"].(string); ok {
		clip.SetMetadataString("youtube_title", v)
	}
	if v, ok := meta["topics"].([]any); ok {
		clip.Metadata["topics"] = v
	}
	if v, ok := meta["speakers"].([]any); ok {
		clip.Metadata["speakers"] = v
	}
	if v, ok := meta["clean_transcript"].(string); ok && v != "" {
		clip.Metadata["clean_transcript"] = v
		clip.SearchText = v
	}
	if v, ok := meta["hook"].(string); ok {
		clip.Metadata["hook"] = v
	}
}
