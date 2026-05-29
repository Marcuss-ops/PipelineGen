package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

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
	if h.generator == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "script generator not initialized")
		return
	}
	if h.imgService == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "image service not initialized")
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
		outputName = images.Slugify(title)
	}
	if outputName == "" {
		outputName = "generated-script"
	}

	textDuration := types.EstimateDuration(types.CountWords(req.SourceText))
	if textDuration < 60 {
		textDuration = 60
	}
	textModel := ""
	if h.cfg != nil {
		textModel = strings.TrimSpace(h.cfg.External.OllamaModel)
	}
	textReq := types.TextGenerationRequest{
		Language:   req.Language,
		Duration:   textDuration,
		Tone:       req.Tone,
		Model:      textModel,
		Prompt:     req.SourceText,
		SourceText: req.SourceText,
		Title:      title,
	}
	if strings.TrimSpace(textReq.Model) == "" {
		textReq.Model = req.Model
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 25*time.Minute)
	defer cancel()

	generated, err := h.generator.GenerateScript(ctx, textReq)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	rewritten := strings.TrimSpace(generated.Script)
	if rewritten == "" {
		rewritten = strings.TrimSpace(req.SourceText)
	}
	rewritten = types.CleanScript(rewritten)

	sentences := splitScriptSentences(rewritten)
	if len(sentences) == 0 {
		sentences = []string{rewritten}
	}
	sentences = groupSentences(sentences, 5)
	if req.SceneCount > 0 && len(sentences) > req.SceneCount {
		sentences = sentences[:req.SceneCount]
	}

	packageData := GeneratedScriptPackage{
		SourceText:        req.SourceText,
		RewrittenScript:   rewritten,
		Language:          req.Language,
		Style:             req.Style,
		VisualStyle:       req.VisualStyle,
		Title:             title,
		OutputName:        outputName,
		WordCount:         generated.WordCount,
		EstimatedDuration: generated.EstDuration,
		Scenes:            make([]GeneratedScene, 0, len(sentences)),
		GeneratedAt:       time.Now().UTC(),
	}

	visualStyle := strings.TrimSpace(req.VisualStyle)
	if visualStyle == "" {
		visualStyle = req.Style
	}
	imageModel := strings.TrimSpace(req.Model)
	if imageModel == "" {
		imageModel = "FLUX.1-schnell"
	}

	for idx, sentence := range sentences {
		sceneID := fmt.Sprintf("scene_%03d", idx+1)
		query := buildVisualQuery(sentence, title, visualStyle, req.Language)
		scene := GeneratedScene{
			ID:    sceneID,
			Index: idx,
			Text:  sentence,
			Query: query,
		}

		asset, genErr := h.imgService.GenerateSmartImage(
			ctx,
			sentence,
			title,
			visualStyle,
			[]string{sentence},
			[]string{req.Style, req.VisualStyle, req.Language},
			req.Width,
			req.Height,
			imageModel,
			false,
		)
		if genErr != nil {
			scene.Error = genErr.Error()
			packageData.Scenes = append(packageData.Scenes, scene)
			continue
		}

		scene.Image = generatedImageFromAsset(asset)
		packageData.Scenes = append(packageData.Scenes, scene)
	}

	videoScenes := make([]VideoScene, 0, len(packageData.Scenes))
	for _, s := range packageData.Scenes {
		link := ""
		if s.Image != nil {
			if s.Image.DriveLink != "" {
				link = s.Image.DriveLink
			} else {
				link = s.Image.LocalPath
			}
		}
		videoScenes = append(videoScenes, VideoScene{
			Text:      s.Text,
			ImageLink: link,
		})
	}

	outputDir, packageData, err := h.writeGeneratedScriptFiles(packageData, videoScenes)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	doc, err := h.createGeneratedGoogleDoc(ctx, packageData, videoScenes)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":            true,
		"output_dir":    outputDir,
		"doc_id":        doc.ID,
		"doc_url":       doc.URL,
		"docs_url":      doc.URL,
		"markdown_path": packageData.Files.Markdown,
		"json_path":     packageData.Files.JSON,
		"script":        packageData.RewrittenScript,
		"word_count":    packageData.WordCount,
		"est_duration":  packageData.EstimatedDuration,
		"language":      packageData.Language,
		"style":         packageData.Style,
		"visual_style":  packageData.VisualStyle,
		"scenes":        packageData.Scenes,
		"files":         packageData.Files,
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
	b.WriteString("## Storyboard Scenes\n\n")
	for _, scene := range pkg.Scenes {
		b.WriteString(fmt.Sprintf("### Scene %s\n", scene.ID))
		b.WriteString(fmt.Sprintf("Text: %s\n\n", scene.Text))
		if scene.Image != nil && scene.Image.DriveLink != "" {
			b.WriteString(fmt.Sprintf("Image Drive Link: %s\n\n", scene.Image.DriveLink))
		} else if scene.Image != nil && scene.Image.LocalPath != "" {
			b.WriteString(fmt.Sprintf("Image Local Path: %s\n\n", scene.Image.LocalPath))
		}
		if scene.Error != "" {
			b.WriteString(fmt.Sprintf("Error: %s\n\n", scene.Error))
		}
	}
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

