package script

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/service/scriptdocs"
)

type GenerateTextRequest struct {
	Topic      string `json:"topic" binding:"required"`
	SourceText string `json:"source_text"`
	Duration   int    `json:"duration"`
	Language   string `json:"language"`
	Tone       string `json:"tone"`
	Template   string `json:"template"`
	Model      string `json:"model"`
}

func (h *ScriptPipelineHandler) generateScriptText(ctx context.Context, req GenerateTextRequest) (string, string, error) {
	if req.Duration == 0 {
		req.Duration = 60
	}
	if req.Language == "" {
		req.Language = "italian"
	}
	if req.Tone == "" {
		req.Tone = "professional"
	}
	if req.Template == "" {
		req.Template = "biography"
	}
	if req.Model == "" {
		req.Model = "gemma3:4b"
	}

	normalizedLang := normalizeScriptLanguage(req.Language)
	sourceText := strings.TrimSpace(req.SourceText)
	if sourceText != "" {
		result, err := h.generator.GenerateFromText(ctx, &ollama.TextGenerationRequest{
			SourceText: sourceText,
			Title:      req.Topic,
			Language:   normalizedLang,
			Duration:   req.Duration,
			Tone:       req.Tone,
			Model:      req.Model,
		})
		if err != nil {
			return "", normalizedLang, err
		}
		return result.Script, normalizedLang, nil
	}

	svc := scriptdocs.NewScriptDocService(
		h.generator,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
	script, err := svc.GenerateScriptText(ctx, req.Topic, req.Duration, normalizedLang, req.Template, req.Model)
	if err != nil {
		return "", normalizedLang, err
	}
	return script, normalizedLang, nil
}

func (h *ScriptPipelineHandler) GenerateText(c *gin.Context) {
	var req GenerateTextRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	script, normalizedLang, err := h.generateScriptText(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	wordCount := len(strings.Fields(script))
	segments, _, err := h.buildSemanticSegments(c.Request.Context(), req.Topic, script, req.Duration, normalizedLang, 4)
	if err != nil || len(segments) == 0 {
		sentences := scriptdocs.ExtractSentences(script)
		if len(sentences) > 0 {
			avgDuration := 20
			if req.Duration > 0 {
				avgDuration = req.Duration / len(sentences)
				if avgDuration <= 0 {
					avgDuration = 20
				}
			}
			segments = make([]Segment, 0, len(sentences))
			for i, sentence := range sentences {
				segments = append(segments, Segment{
					Index:     i,
					Text:      sentence,
					StartTime: i * avgDuration,
					EndTime:   (i + 1) * avgDuration,
				})
			}
		}
	}
	segments = enrichSegments(segments)
	frasi, nomi, parole, images := h.extractEntitiesForPipeline(segments)
	stockAssocs, driveAssocs, artlistAssocs, topicFolderID := h.searchClipsForPipeline(c.Request.Context(), req.Topic, segments)
	fullContent := h.BuildDocumentContent(
		req.Topic,
		req.Topic,
		req.Duration,
		req.Language,
		script,
		segments,
		artlistAssocs,
		topicFolderID,
		req.Topic,
		nil,
		driveAssocs,
		frasi,
		nomi,
		parole,
		images,
		nil,
	)

	c.JSON(http.StatusOK, gin.H{
		"ok":             true,
		"title":          req.Topic,
		"script":         script,
		"full_content":   fullContent,
		"word_count":     wordCount,
		"est_duration":   int(float64(wordCount) * 60 / 140),
		"model":          req.Model,
		"language":       normalizedLang,
		"stock_assocs":   stockAssocs,
		"drive_assocs":   driveAssocs,
		"artlist_assocs": artlistAssocs,
	})
}

// GenerateDocument generates the script and publishes a Google Doc in one request.
func (h *ScriptPipelineHandler) GenerateDocument(c *gin.Context) {
	var req GenerateTextRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	script, normalizedLang, err := h.generateScriptText(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	docReq := CreateDocumentRequest{
		Title:      req.Topic,
		Topic:      req.Topic,
		Duration:   req.Duration,
		Template:   req.Template,
		Script:     script,
		SourceText: req.SourceText,
		Language:   normalizedLang,
	}

	docResp, err := h.createDocumentFromRequest(c.Request.Context(), &docReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	wordCount := len(strings.Fields(script))
	c.JSON(http.StatusOK, GenerateDocResponse{
		Ok:          true,
		DocID:       docResp.DocID,
		DocURL:      docResp.DocURL,
		Script:      script,
		WordCount:   wordCount,
		EstDuration: int(float64(wordCount) * 60 / 140),
		Model:       req.Model,
		Language:    normalizedLang,
	})
}

func normalizeScriptLanguage(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	switch {
	case strings.HasPrefix(raw, "it"):
		return "it"
	case strings.HasPrefix(raw, "en"):
		return "en"
	case strings.HasPrefix(raw, "es"):
		return "es"
	case strings.HasPrefix(raw, "fr"):
		return "fr"
	case strings.HasPrefix(raw, "de"):
		return "de"
	case strings.HasPrefix(raw, "pt"):
		return "pt"
	case strings.HasPrefix(raw, "ro"):
		return "ro"
	default:
		return "it"
	}
}

type TranslateRequest struct {
	Text      string   `json:"text" binding:"required"`
	Languages []string `json:"languages"`
	Topic     string   `json:"topic"`
}

type TranslateResponse struct {
	Ok           bool          `json:"ok"`
	Translations []Translation `json:"translations"`
}

func (h *ScriptPipelineHandler) Translate(c *gin.Context) {
	var req TranslateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	if len(req.Languages) == 0 {
		req.Languages = []string{"en", "es", "fr", "de"}
	}

	var translations []Translation
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, lang := range req.Languages {
		wg.Add(1)
		go func(lang string) {
			defer wg.Done()

			prompt := fmt.Sprintf("Translate to %s. Output ONLY the translation, no explanations or options.\n\nText: %s", lang, req.Text)

			resp, err := h.generator.GetClient().Generate(context.Background(), prompt)
			mu.Lock()
			if err == nil {
				cleanText := strings.TrimSpace(resp)
				lines := strings.Split(cleanText, "\n")
				if len(lines) > 1 {
					for i, line := range lines {
						if strings.HasPrefix(line, "**") && strings.HasSuffix(line, "**") {
							cleanText = strings.Trim(line, "*")
							break
						}
						if !strings.Contains(line, ":") && len(line) > 5 {
							cleanText = line
							break
						}
						if i == len(lines)-1 {
							cleanText = line
						}
					}
				}
				translations = append(translations, Translation{
					Language: lang,
					Text:     strings.TrimSpace(cleanText),
				})
			}
			mu.Unlock()
		}(lang)
	}

	wg.Wait()

	c.JSON(http.StatusOK, gin.H{
		"ok":           true,
		"translations": translations,
	})
}
