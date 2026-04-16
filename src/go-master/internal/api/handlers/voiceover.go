// Package handlers provides HTTP handlers for the API.
package handlers

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"velox/go-master/internal/audio/tts"
)

// VoiceoverHandler gestisce gli endpoint per la generazione voiceover
type VoiceoverHandler struct {
	generator *tts.EdgeTTS
}

// NewVoiceoverHandler crea un nuovo handler per voiceover
func NewVoiceoverHandler(generator *tts.EdgeTTS) *VoiceoverHandler {
	return &VoiceoverHandler{generator: generator}
}

// RegisterRoutes registra le route per voiceover
func (h *VoiceoverHandler) RegisterRoutes(rg *gin.RouterGroup) {
	voiceover := rg.Group("/voiceover")
	{
		voiceover.POST("/generate", h.Generate)
		voiceover.GET("/languages", h.ListLanguages)
		voiceover.GET("/voices", h.ListVoices)
		voiceover.GET("/download/:file", h.Download)
		voiceover.GET("/health", h.Health)
	}
}

// Generate genera un voiceover
// @Summary Genera voiceover
// @Description Genera un file audio voiceover da testo
// @Tags voiceover
// @Accept json
// @Produce json
// @Param request body tts.GenerateRequest true "Parametri generazione"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /voiceover/generate [post]
func (h *VoiceoverHandler) Generate(c *gin.Context) {
	var req tts.GenerateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Invalid request: " + err.Error(),
		})
		return
	}

	// Validazione
	if req.Text == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "text is required",
		})
		return
	}

	// Defaults
	if req.Language == "" {
		req.Language = "it"
	}

	var result *tts.GenerationResult
	var err error

	// Genera con voce specifica o lingua
	if req.Voice != "" {
		result, err = h.generator.GenerateWithVoice(c.Request.Context(), req.Text, req.Voice)
	} else {
		result, err = h.generator.Generate(c.Request.Context(), req.Text, req.Language)
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":         true,
		"file_name":  result.FileName,
		"file_path":  result.FilePath,
		"duration":   result.Duration,
		"word_count": result.WordCount,
		"voice":      result.VoiceUsed,
		"language":   result.Language,
	})
}

// ListLanguages lista le lingue disponibili
// @Summary Lista lingue
// @Description Restituisce la lista delle lingue disponibili per voiceover
// @Tags voiceover
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /voiceover/languages [get]
func (h *VoiceoverHandler) ListLanguages(c *gin.Context) {
	languages := tts.ListLanguages()

	// Formatta per risposta
	result := make([]map[string]interface{}, len(languages))
	for i, lang := range languages {
		result[i] = map[string]interface{}{
			"code":   lang.Code,
			"name":   lang.Name,
			"voices": lang.Voices,
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":        true,
		"languages": result,
		"count":     len(result),
	})
}

// ListVoices lista tutte le voci disponibili
// @Summary Lista voci
// @Description Restituisce la lista delle voci disponibili
// @Tags voiceover
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /voiceover/voices [get]
func (h *VoiceoverHandler) ListVoices(c *gin.Context) {
	voices := make(map[string]string)
	for lang, voice := range tts.DefaultVoices {
		voices[lang] = voice
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":    true,
		"voices": voices,
	})
}

// Download scarica un file voiceover
// @Summary Scarica voiceover
// @Description Scarica un file voiceover generato
// @Tags voiceover
// @Param file path string true "Nome file"
// @Success 200 {file} binary
// @Failure 400 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /voiceover/download/{file} [get]
func (h *VoiceoverHandler) Download(c *gin.Context) {
	fileName := c.Param("file")

	// Security: previeni directory traversal
	if strings.Contains(fileName, "..") || strings.Contains(fileName, "/") || strings.Contains(fileName, "\\") {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Invalid filename",
		})
		return
	}

	// Verifica che sia un file MP3
	if !strings.HasSuffix(fileName, ".mp3") {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Only MP3 files are allowed",
		})
		return
	}

	filePath := filepath.Join(h.generator.GetOutputDir(), fileName)

	// Verifica esistenza
	if _, err := os.Stat(filePath); err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"ok":    false,
			"error": "File not found",
		})
		return
	}

	c.File(filePath)
}

// Health verifica lo stato del servizio voiceover
// @Summary Health check
// @Description Verifica se edge-tts è disponibile
// @Tags voiceover
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /voiceover/health [get]
func (h *VoiceoverHandler) Health(c *gin.Context) {
	available := tts.CheckEdgeTTSAvailable()

	status := "healthy"
	if !available {
		status = "degraded"
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":               true,
		"status":           status,
		"edge_tts_available": available,
		"output_dir":       h.generator.GetOutputDir(),
	})
}