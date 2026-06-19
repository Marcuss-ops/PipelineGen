package api

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	ollamatypes "github.com/Marcuss-ops/PipelineGen/internal/platform/ai/ollama/types"
	textutil "github.com/Marcuss-ops/PipelineGen/internal/platform"
)

type RegenerateSectionRequest struct {
	Instruction string `json:"instruction" binding:"required"`
	Model       string `json:"model,omitempty"`
}

func (h *ScriptFlowHandler) RegenerateSection(c *gin.Context) {
	scriptIDStr := c.Param("id")
	sectionIDStr := c.Param("section_id")

	scriptID, err := strconv.ParseInt(scriptIDStr, 10, 64)
	if err != nil {
		apiutil.Error(c, http.StatusBadRequest, "invalid script ID")
		return
	}

	sectionID, err := strconv.ParseInt(sectionIDStr, 10, 64)
	if err != nil {
		apiutil.Error(c, http.StatusBadRequest, "invalid section ID")
		return
	}

	var req RegenerateSectionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	if h.scriptsRepo == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "scripts repository not initialized")
		return
	}

	// Retrieve target section
	section, err := h.scriptsRepo.GetSectionByID(c.Request.Context(), sectionID)
	if err != nil {
		h.log.Error("failed to get section from DB", zap.Int64("section_id", sectionID), zap.Error(err))
		apiutil.Error(c, http.StatusNotFound, "section not found")
		return
	}

	if section.ScriptID != scriptID {
		apiutil.Error(c, http.StatusBadRequest, "section does not belong to the specified script")
		return
	}

	// Retrieve script metadata to get the model or channel
	script, allSections, _, err := h.scriptsRepo.GetScriptByID(scriptID)
	if err != nil {
		apiutil.Error(c, http.StatusNotFound, "script not found")
		return
	}

	// Get adjacent sections
	prev, next, err := h.scriptsRepo.GetAdjacentSections(c.Request.Context(), scriptID, section.SortOrder)
	if err != nil {
		h.log.Warn("failed to get adjacent sections", zap.Error(err))
	}

	// Construct context prompt for Ollama
	var promptBuilder strings.Builder
	promptBuilder.WriteString(fmt.Sprintf("Devi rigenerare la sezione intitolata '%s' per il libro o script sul tema '%s'.\n", section.SectionTitle, script.Topic))
	promptBuilder.WriteString("Il testo precedente e successivo sono forniti di seguito solo come contesto per garantire la fluidità del testo, evitare ripetizioni e garantire transizioni naturali.\n\n")

	if prev != nil {
		promptBuilder.WriteString(fmt.Sprintf("--- CONTESTO SEZIONE PRECEDENTE (%s) ---\n%s\n\n", prev.SectionTitle, prev.Content))
	}
	promptBuilder.WriteString(fmt.Sprintf("--- SEZIONE CORRENTE DA RIGENERARE (usa come base) ---\n%s\n\n", section.Content))
	if next != nil {
		promptBuilder.WriteString(fmt.Sprintf("--- CONTESTO SEZIONE SUCCESSIVA (%s) ---\n%s\n\n", next.SectionTitle, next.Content))
	}

	promptBuilder.WriteString(fmt.Sprintf("Istruzione specifica di rigenerazione:\n\"%s\"\n\n", req.Instruction))
	promptBuilder.WriteString("Rispondi DIRETTAMENTE ed ESCLUSIVAMENTE con il nuovo testo per questa sezione. Non includere preamboli, note o etichette come 'Ecco il testo rigenerato:' o markdown di intestazione per il capitolo.")

	model := req.Model
	if model == "" {
		model = script.ModelUsed
	}
	if model == "" && h.cfg != nil {
		model = h.cfg.External.OllamaModel
	}

	h.log.Info("calling ollama to regenerate section", zap.Int64("section_id", sectionID), zap.String("model", model))
	res, err := h.generator.GenerateScript(c.Request.Context(), ollamatypes.TextGenerationRequest{
		Language: script.Language,
		Duration: script.Duration / len(allSections), // approximate proportional duration
		Tone:     script.Template,
		Model:    model,
		Prompt:   promptBuilder.String(),
	})
	if err != nil {
		h.log.Error("failed to generate script section update", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	newContent := strings.TrimSpace(res.Script)
	if newContent == "" {
		apiutil.Error(c, http.StatusInternalServerError, "received empty response from generator")
		return
	}

	// Update database
	err = h.scriptsRepo.UpdateSectionContent(c.Request.Context(), sectionID, newContent)
	if err != nil {
		h.log.Error("failed to update section content in database", zap.Error(err))
		apiutil.InternalError(c, err)
		return
	}

	// Fetch updated sections to reconstruct full document and update Google Doc
	_, updatedSections, _, err := h.scriptsRepo.GetScriptByID(scriptID)
	if err == nil {
		// Reconstruct Merged content HTML
		// Parse metadata to extract google_doc_id
		var meta map[string]any
		if err := json.Unmarshal([]byte(script.MetadataJSON), &meta); err == nil {
			docID, _ := meta["google_doc_id"].(string)
			if docID != "" && h.docClient != nil {
				h.log.Info("updating associated google doc", zap.String("doc_id", docID))
				// Prepare generated parts to reconstruct HTML
			titles := make([]string, len(updatedSections))
			contents := make([]string, len(updatedSections))
			for idx, s := range updatedSections {
				titles[idx] = s.SectionTitle
				contents[idx] = s.Content
			}
			noChapters, _ := meta["no_chapters"].(bool)
			language := script.Language
			if language == "" {
				language = "en"
			}
			htmlMergedContent := buildSectionDocHTML(script.Topic, titles, contents, noChapters, language)
				// Re-upload/update the document
				docErr := h.docClient.UpdateDoc(c.Request.Context(), docID, script.Topic, htmlMergedContent)
				if docErr != nil {
					h.log.Error("failed to update google doc content", zap.String("doc_id", docID), zap.Error(docErr))
				}
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":         true,
		"section_id": sectionID,
		"title":      section.SectionTitle,
		"content":    newContent,
	})
}

type EvictCacheRequest struct {
	Titles []string `json:"titles,omitempty"`
}

// buildSectionDocHTML generates Google Doc HTML from section titles and contents.
// Mirrors the logic in application/scriptflow/batch/doc.go but operates on
// plain string slices instead of batch-internal types.
func buildSectionDocHTML(title string, sectionTitles []string, sectionContents []string, noChapters bool, language string) string {
	cl := "Chapter"
	switch language {
	case "it":
		cl = "Capitolo"
	case "fr":
		cl = "Chapitre"
	case "es":
		cl = "Capítulo"
	case "de":
		cl = "Kapitel"
	}

	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html><head><meta charset=\"utf-8\"></head><body>")
	b.WriteString(fmt.Sprintf("<h1>%s</h1>", html.EscapeString(strings.TrimSpace(title))))

	if !noChapters {
		b.WriteString(fmt.Sprintf("<h2>%s</h2>", html.EscapeString("Table of Contents")))
		b.WriteString("<ol>")
		for idx := range sectionTitles {
			b.WriteString(fmt.Sprintf("<li><a href=\"#ch-%d\">%s %d: %s</a></li>", idx+1, cl, idx+1, html.EscapeString(strings.TrimSpace(sectionTitles[idx]))))
		}
		b.WriteString("</ol>")
		b.WriteString("<hr>")
	}
	for idx := range sectionTitles {
		if !noChapters {
			b.WriteString(fmt.Sprintf("<section id=\"ch-%d\">", idx+1))
			b.WriteString(fmt.Sprintf("<h2>%s %d: %s</h2>", cl, idx+1, html.EscapeString(strings.TrimSpace(sectionTitles[idx]))))
		}
		for _, para := range strings.Split(sectionContents[idx], "\n\n") {
			para = strings.TrimSpace(para)
			if para == "" {
				continue
			}
			para = textutil.CleanForVoiceover(para)
			para = html.EscapeString(para)
			para = strings.ReplaceAll(para, "\n", "<br>")
			b.WriteString(fmt.Sprintf("<p>%s</p>", para))
		}
		if !noChapters {
			b.WriteString("</section>")
			if idx < len(sectionTitles)-1 {
				b.WriteString("<hr>")
			}
		} else {
			b.WriteString("<br>")
		}
	}
	b.WriteString("</body></html>")
	return b.String()
}

func (h *ScriptFlowHandler) EvictCache(c *gin.Context) {
	var req EvictCacheRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// Only EOF (empty body) is treated as "evict all".
		// Malformed JSON still gets a 400 so callers can debug.
		if err.Error() != "EOF" {
			apiutil.Error(c, http.StatusBadRequest, err.Error())
			return
		}
		// Empty body — evict everything.
		req.Titles = nil
	}

	// First, reset all circuit breakers by clearing the breaker map on
	// the generator client. This is the most impactful operation:
	// it unblocks requests that were rejected by an open breaker.
	if h.generator != nil && h.generator.GetClient() != nil {
		resetCount := h.generator.GetClient().ResetCircuitBreakers()
		h.log.Info("circuit breakers reset on cache evict", zap.Int("models_reset", resetCount))
	}

	if h.memorySvc == nil {
		if len(req.Titles) == 0 {
			// No memory service and no titles means just the breaker reset
			// above is enough — no-op is fine.
			c.JSON(http.StatusOK, gin.H{
				"ok":               true,
				"deleted_count":    0,
				"circuit_breakers": "reset",
			})
			return
		}
		apiutil.Error(c, http.StatusServiceUnavailable, "memory service not initialized")
		return
	}

	var count int64
	var evictErr error
	if len(req.Titles) > 0 {
		count, evictErr = h.memorySvc.EvictExactOutputs(c.Request.Context(), req.Titles)
	}
	if evictErr != nil {
		h.log.Error("failed to evict cache", zap.Error(evictErr))
		apiutil.InternalError(c, evictErr)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":               true,
		"deleted_count":    count,
		"evicted_titles":   req.Titles,
		"circuit_breakers": "reset",
	})
}
