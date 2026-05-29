package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"velox/go-master/internal/core/jobs"
	jobservice "velox/go-master/internal/jobs"
	"velox/go-master/internal/media/images"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/ml/ollama/types"
	"velox/go-master/internal/pkg/apiutil"
)

type GenerateFromSourceRequest struct {
	SourceText  string `json:"source_text" binding:"required"`
	Language    string `json:"language,omitempty"`
	Style       string `json:"style,omitempty"`
	VisualStyle string `json:"visual_style,omitempty"`
	Tone        string `json:"tone,omitempty"`
	Title       string `json:"title,omitempty"`
	OutputName  string `json:"output_name,omitempty"`
	SceneCount  int    `json:"scene_count,omitempty"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
	Model       string `json:"model,omitempty"`
}

type GeneratedImage struct {
	DriveFileID string `json:"drive_file_id,omitempty"`
	DriveLink   string `json:"drive_link,omitempty"`
	LocalPath   string `json:"local_path,omitempty"`
	PathRel     string `json:"path_rel,omitempty"`
	Source      string `json:"source,omitempty"`
	Description string `json:"description,omitempty"`
	Error       string `json:"error,omitempty"`
}

type GeneratedScene struct {
	ID    string          `json:"id"`
	Index int             `json:"index"`
	Text  string          `json:"text"`
	Query string          `json:"query"`
	Image *GeneratedImage `json:"image,omitempty"`
	Error string          `json:"error,omitempty"`
}

type VideoScene struct {
	Text      string `json:"text"`
	ImageLink string `json:"image_link"`
}

type GeneratedScriptFileInfo struct {
	Markdown string `json:"markdown"`
	JSON     string `json:"json"`
}

type GeneratedScriptPackage struct {
	SourceText        string                  `json:"source_text"`
	RewrittenScript   string                  `json:"rewritten_script"`
	Language          string                  `json:"language"`
	Style             string                  `json:"style,omitempty"`
	VisualStyle       string                  `json:"visual_style,omitempty"`
	Title             string                  `json:"title,omitempty"`
	OutputName        string                  `json:"output_name,omitempty"`
	WordCount         int                     `json:"word_count,omitempty"`
	EstimatedDuration int                     `json:"estimated_duration,omitempty"`
	Scenes            []GeneratedScene        `json:"scenes"`
	Files             GeneratedScriptFileInfo `json:"files"`
	GeneratedAt       time.Time               `json:"generated_at"`
}

// GenerateFromSource takes inline source_text, rewrites it, builds a scene JSON and generates images.
func (h *ScriptFlowHandler) GenerateFromSource(c *gin.Context) {
	if h.jobsSvc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "jobs service not initialized")
		return
	}

	req, ok := apiutil.BindJSON[GenerateFromSourceRequest](c)
	if !ok {
		return
	}
	req.SourceText = strings.TrimSpace(req.SourceText)
	if req.SourceText == "" {
		apiutil.BadRequest(c, "source_text is required")
		return
	}
	req.Language = strings.TrimSpace(req.Language)
	if req.Language == "" {
		req.Language = "en"
	}
	req.Style = strings.TrimSpace(req.Style)
	if req.Style == "" {
		req.Style = "documentary"
	}
	req.VisualStyle = strings.TrimSpace(req.VisualStyle)
	if req.Tone == "" {
		req.Tone = req.Style
	}
	if req.SceneCount <= 0 {
		req.SceneCount = 100
	}
	if req.Width <= 0 {
		req.Width = 768
	}
	if req.Height <= 0 {
		req.Height = 1344
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = strings.TrimSpace(req.OutputName)
	}
	if title == "" {
		title = "Generated Script"
	}
	outputName := strings.TrimSpace(req.OutputName)
	if outputName == "" {
		outputName = Slugify(title)
	}
	if outputName == "" {
		outputName = "generated-script"
	}
	req.Title = title
	req.OutputName = outputName

	var payloadMap map[string]any
	reqBytes, err := json.Marshal(req)
	if err == nil {
		_ = json.Unmarshal(reqBytes, &payloadMap)
	}

	h.log.Info("enqueuing script.generate_from_source job", zap.String("title", title))

	job, err := h.jobsSvc.Enqueue(c.Request.Context(), &jobservice.EnqueueRequest{
		Type:       models.JobType(jobs.JobTypeSourceScriptGenerate),
		Payload:    payloadMap,
		MaxRetries: 3,
	})
	if err != nil {
		h.log.Error("failed to enqueue script generate job", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":     true,
		"job_id": job.ID,
		"status": job.Status,
	})
}

// GetJobStatus returns the progress and results of a background script generation job
func (h *ScriptFlowHandler) GetJobStatus(c *gin.Context) {
	if h.jobsSvc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "jobs service not initialized")
		return
	}

	jobID := strings.TrimSpace(c.Param("job_id"))
	if jobID == "" {
		apiutil.BadRequest(c, "job_id is required")
		return
	}

	job, err := h.jobsSvc.Get(c.Request.Context(), jobID)
	if err != nil {
		apiutil.NotFound(c, fmt.Sprintf("job not found: %v", err))
		return
	}

	var result map[string]any
	if len(job.Result) > 0 {
		_ = json.Unmarshal(job.Result, &result)
	}

	apiutil.OK(c, gin.H{
		"ok":           true,
		"job_id":       job.ID,
		"status":       job.Status,
		"progress":     job.Progress,
		"current_step": job.CurrentStep,
		"error":        job.Error,
		"result":       result,
	})
}

func (h *ScriptFlowHandler) writeGeneratedScriptFiles(pkg GeneratedScriptPackage, videoScenes []VideoScene) (string, GeneratedScriptPackage, error) {
	baseDir := "."
	if h.cfg != nil && strings.TrimSpace(h.cfg.Storage.DataDir) != "" {
		baseDir = filepath.Dir(h.cfg.Storage.DataDir)
	}
	outputDir := filepath.Join(baseDir, "docs", "generated", buildTimestampedSlug(pkg.OutputName, pkg.GeneratedAt))
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", pkg, fmt.Errorf("create generated docs dir: %w", err)
	}

	jsonPath := filepath.Join(outputDir, "script.json")
	mdPath := filepath.Join(outputDir, "script.md")

	pkg.Files = GeneratedScriptFileInfo{
		Markdown: mdPath,
		JSON:     jsonPath,
	}
	jsonData, err := json.MarshalIndent(videoScenes, "", "  ")
	if err != nil {
		return "", pkg, fmt.Errorf("marshal generated json with files: %w", err)
	}
	if err := os.WriteFile(jsonPath, jsonData, 0644); err != nil {
		return "", pkg, fmt.Errorf("write generated json: %w", err)
	}

	mdData := renderGeneratedMarkdown(pkg, jsonData)
	if err := os.WriteFile(mdPath, mdData, 0644); err != nil {
		return "", pkg, fmt.Errorf("write generated markdown: %w", err)
	}

	return outputDir, pkg, nil
}

func renderGeneratedMarkdown(pkg GeneratedScriptPackage, jsonData []byte) []byte {
	var b strings.Builder
	b.WriteString("# Generated Script\n\n")
	if strings.TrimSpace(pkg.Title) != "" {
		b.WriteString("## Title\n")
		b.WriteString(pkg.Title)
		b.WriteString("\n\n")
	}
	b.WriteString("## Script\n\n")
	b.WriteString(strings.TrimSpace(pkg.RewrittenScript))
	b.WriteString("\n\n")
	
	b.WriteString("## Scenes JSON\n")
	b.WriteString("```json\n")
	b.Write(jsonData)
	if len(jsonData) == 0 || jsonData[len(jsonData)-1] != '\n' {
		b.WriteByte('\n')
	}
	b.WriteString("```\n")
	return []byte(b.String())
}

func generatedImageFromAsset(asset *models.ImageAsset) *GeneratedImage {
	if asset == nil {
		return nil
	}
	out := &GeneratedImage{
		DriveFileID: strings.TrimSpace(asset.DriveFileID),
		LocalPath:   asset.PathRel,
		PathRel:     asset.PathRel,
		Source:      asset.SourceURL,
		Description: asset.Description,
	}
	out.DriveLink = driveLinkFromImageAsset(asset)
	return out
}

func driveLinkFromImageAsset(asset *models.ImageAsset) string {
	if asset == nil {
		return ""
	}
	fileID := strings.TrimSpace(asset.DriveFileID)
	if fileID == "" {
		return ""
	}
	return "https://drive.google.com/file/d/" + fileID + "/view"
}

func buildTimestampedSlug(name string, t time.Time) string {
	slug := images.Slugify(name)
	if slug == "" {
		slug = "generated-script"
	}
	return fmt.Sprintf("%s_%s", t.Format("20060102_150405"), slug)
}

func groupSentences(sentences []string, size int) []string {
	if size <= 0 {
		size = 5
	}
	var grouped []string
	var current []string
	for _, s := range sentences {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		current = append(current, s)
		if len(current) == size {
			grouped = append(grouped, strings.Join(current, " "))
			current = nil
		}
	}
	if len(current) > 0 {
		grouped = append(grouped, strings.Join(current, " "))
	}
	return grouped
}

