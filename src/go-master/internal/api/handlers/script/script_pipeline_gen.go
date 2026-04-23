package script

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"velox/go-master/internal/service/scriptdocs"
)

type GenerateTextRequest struct {
	Topic    string `json:"topic" binding:"required"`
	Duration int    `json:"duration"`
	Language string `json:"language"`
	Tone     string `json:"tone"`
	Template string `json:"template"`
	Model    string `json:"model"`
}

type GenerateTextResponse struct {
	Ok          bool   `json:"ok"`
	Script      string `json:"script"`
	WordCount   int    `json:"word_count"`
	EstDuration int    `json:"est_duration"`
	Model       string `json:"model"`
	Language    string `json:"language"`
}

func (h *ScriptPipelineHandler) GenerateText(c *gin.Context) {
	var req GenerateTextRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

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

	svc := scriptdocs.NewScriptDocService(
		h.generator,
		nil,
		nil,
		h.stockDB,
		nil,
		nil,
		h.artlistSrc,
		h.artlistDB,
	)

	script, err := svc.GenerateScriptText(c.Request.Context(), req.Topic, req.Duration, req.Language, req.Template, req.Model)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	wordCount := len(strings.Fields(script))

	c.JSON(http.StatusOK, gin.H{
		"ok":           true,
		"script":       script,
		"word_count":   wordCount,
		"est_duration": int(float64(wordCount) * 60 / 140),
		"model":        req.Model,
		"language":     req.Language,
	})
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
