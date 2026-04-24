package script

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"velox/go-master/internal/service/scriptdocs"
)

type TextAnalysisRequest struct {
	Text           string `json:"text"`
	SourceText     string `json:"source_text"`
	Topic          string `json:"topic"`
	SourceLanguage string `json:"source_language,omitempty"`
	Duration       int    `json:"duration"`
	MaxChapters    int    `json:"max_chapters"`
}

type TextAnalysisDocRequest struct {
	Text           string `json:"text"`
	SourceText     string `json:"source_text"`
	Title          string `json:"title"`
	Topic          string `json:"topic"`
	SourceLanguage string `json:"source_language,omitempty"`
	Duration       int    `json:"duration"`
	MaxChapters    int    `json:"max_chapters"`
	Template       string `json:"template"`
	PreviewOnly    bool   `json:"preview_only"`
	MinimalDoc     bool   `json:"minimal_doc"`
}

type TextAnalysisChapter struct {
	Index             int                `json:"index"`
	Title             string             `json:"title"`
	StartTime         int                `json:"start_time"`
	EndTime           int                `json:"end_time"`
	StartPhrase       string             `json:"start_phrase"`
	EndPhrase         string             `json:"end_phrase"`
	SentenceCount     int                `json:"sentence_count"`
	FrasiImportanti   []string           `json:"frasi_importanti,omitempty"`
	NomiSpeciali      []string           `json:"nomi_speciali,omitempty"`
	ParoleImportanti  []string           `json:"parole_importanti,omitempty"`
	EntitaConImmagine []EntityImage      `json:"entita_con_immagine,omitempty"`
	DriveAssocs       []DriveFolderAssoc `json:"drive_assocs,omitempty"`
	StockAssocs       []StockAssoc       `json:"stock_assocs,omitempty"`
	ArtlistAssocs     []ArtlistAssoc     `json:"artlist_assocs,omitempty"`
}

type TextAnalysisResponse struct {
	Ok            bool                  `json:"ok"`
	Topic         string                `json:"topic,omitempty"`
	TotalWords    int                   `json:"total_words"`
	TotalChapters int                   `json:"total_chapters"`
	AllEntities   []string              `json:"all_entities,omitempty"`
	AllKeywords   []string              `json:"all_keywords,omitempty"`
	Chapters      []TextAnalysisChapter `json:"chapters"`
}

type analysisSection struct {
	Title string
	Text  string
}

func (h *ScriptPipelineHandler) AnalyzeText(c *gin.Context) {
	h.handleTextAnalysis(c)
}

func (h *ScriptPipelineHandler) AnalyzeEntities(c *gin.Context) {
	h.handleTextAnalysis(c)
}

func (h *ScriptPipelineHandler) AnalyzeTimestamps(c *gin.Context) {
	h.handleTextAnalysis(c)
}

func (h *ScriptPipelineHandler) AnalyzeAssociations(c *gin.Context) {
	h.handleTextAnalysis(c)
}

func (h *ScriptPipelineHandler) AnalyzeCreateDoc(c *gin.Context) {
	var req TextAnalysisDocRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	req.Text = strings.TrimSpace(req.Text)
	req.SourceText = strings.TrimSpace(req.SourceText)
	if req.Text == "" {
		req.Text = req.SourceText
	}
	if req.Text == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "text is required"})
		return
	}
	if req.Title == "" {
		req.Title = req.Topic
	}
	if req.Title == "" {
		req.Title = inferPlannerTopic(req.Text)
	}
	if req.Template == "" {
		req.Template = "documentary"
	}
	if req.Duration <= 0 {
		req.Duration = estimateDurationFromText(req.Text)
	}
	if req.MaxChapters <= 0 {
		req.MaxChapters = 4
	}

	analysis, err := h.buildTextAnalysis(c.Request.Context(), TextAnalysisRequest{
		Text:           req.Text,
		SourceText:     req.SourceText,
		Topic:          req.Topic,
		SourceLanguage: req.SourceLanguage,
		Duration:       req.Duration,
		MaxChapters:    req.MaxChapters,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	docReq := CreateDocumentRequest{
		Title:          req.Title,
		Topic:          req.Topic,
		Duration:       req.Duration,
		Template:       req.Template,
		Script:         req.Text,
		SourceText:     req.SourceText,
		Language:       "en",
		PreviewOnly:    req.PreviewOnly,
		MinimalDoc:     req.MinimalDoc,
		SkipEnrichment: true,
	}

	docReq.Segments = make([]Segment, 0, len(analysis.Chapters))
	for _, ch := range analysis.Chapters {
		chapterSegment := Segment{
			Index:     ch.Index,
			Text:      strings.TrimSpace(chapterTextFromAnalysis(ch)),
			StartTime: ch.StartTime,
			EndTime:   ch.EndTime,
		}
		docReq.Segments = append(docReq.Segments, chapterSegment)
		docReq.FrasiImportanti = append(docReq.FrasiImportanti, ch.FrasiImportanti...)
		docReq.NomiSpeciali = append(docReq.NomiSpeciali, ch.NomiSpeciali...)
		docReq.ParoleImportanti = append(docReq.ParoleImportanti, ch.ParoleImportanti...)
		docReq.ClipDriveAssocs = append(docReq.ClipDriveAssocs, ch.DriveAssocs...)
		docReq.StockAssocs = append(docReq.StockAssocs, ch.StockAssocs...)

		images := h.extractImagesForPipeline(req.Topic, ch.Title, []Segment{chapterSegment})
		if len(images) > 0 {
			ch.EntitaConImmagine = images
			docReq.EntitaConImmagine = append(docReq.EntitaConImmagine, images...)
			analysis.Chapters[ch.Index].EntitaConImmagine = images
		}
	}

	if len(docReq.StockDriveAssocs) == 0 {
		for _, ch := range analysis.Chapters {
			if folderID, folderName := h.resolveStockFolderForDocument(ch.Title); folderID != "" {
				docReq.StockDriveAssocs = append(docReq.StockDriveAssocs, DriveFolderAssoc{
					Phrase:        ch.Title,
					InitialPhrase: ch.StartPhrase,
					FinalPhrase:   ch.EndPhrase,
					FolderName:    folderName,
					FolderURL:     "https://drive.google.com/drive/folders/" + folderID,
				})
			}
		}
	}

	resp, err := h.createDocumentFromRequest(c.Request.Context(), &docReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":             true,
		"doc_id":         resp.DocID,
		"doc_url":        resp.DocURL,
		"analysis":       analysis,
		"preview_path":   resp.PreviewPath,
		"mode":           resp.Mode,
		"total_chapters": analysis.TotalChapters,
	})
}

func (h *ScriptPipelineHandler) handleTextAnalysis(c *gin.Context) {
	var req TextAnalysisRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	req.Text = strings.TrimSpace(req.Text)
	req.SourceText = strings.TrimSpace(req.SourceText)
	if req.Text == "" {
		req.Text = req.SourceText
	}
	if req.Text == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "text is required"})
		return
	}

	if req.MaxChapters <= 0 {
		req.MaxChapters = 4
	}
	if req.Duration <= 0 {
		req.Duration = estimateDurationFromText(req.Text)
	}

	resp, err := h.buildTextAnalysis(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}

func (h *ScriptPipelineHandler) buildTextAnalysis(ctx context.Context, req TextAnalysisRequest) (*TextAnalysisResponse, error) {
	topic := strings.TrimSpace(req.Topic)
	if topic == "" {
		topic = inferPlannerTopic(req.Text)
	}
	sourceLanguage := strings.TrimSpace(req.SourceLanguage)
	if sourceLanguage == "" {
		sourceLanguage = "english"
	}

	sections := splitAnalysisSections(req.Text, req.MaxChapters)
	var chapters []ChapterPlan
	if h.generator != nil && h.generator.GetClient() != nil {
		_, plannedChapters, err := h.buildSemanticSegments(ctx, topic, req.Text, req.Duration, sourceLanguage, req.MaxChapters)
		if err == nil && len(plannedChapters) > 0 {
			chapters = plannedChapters
		}
	}

	if len(chapters) == 0 {
		if len(sections) == 0 {
			return nil, fmt.Errorf("no meaningful text sections found")
		}
		chapters = make([]ChapterPlan, 0, len(sections))
		runningWords := 0
		totalWords := 0
		for _, sec := range sections {
			totalWords += countWords(sec.Text)
		}
		if totalWords <= 0 {
			totalWords = countWords(req.Text)
		}
		if totalWords <= 0 {
			totalWords = 1
		}
		for i, sec := range sections {
			chapterText := strings.TrimSpace(sec.Text)
			if chapterText == "" {
				continue
			}
			chapterWords := countWords(chapterText)
			if chapterWords <= 0 {
				chapterWords = 1
			}
			startTime := int(float64(req.Duration) * float64(runningWords) / float64(totalWords))
			runningWords += chapterWords
			endTime := int(float64(req.Duration) * float64(runningWords) / float64(totalWords))
			if i == len(sections)-1 {
				endTime = req.Duration
			}
			if endTime < startTime {
				endTime = startTime
			}
			chapters = append(chapters, ChapterPlan{
				Index:         i,
				Title:         strings.TrimSpace(sec.Title),
				StartTime:     startTime,
				EndTime:       endTime,
				SentenceCount: len(scriptdocs.ExtractSentences(chapterText)),
				SourceText:    chapterText,
			})
		}
	}

	totalWords := 0
	for _, ch := range chapters {
		totalWords += countWords(strings.TrimSpace(ch.SourceText))
	}
	if totalWords <= 0 {
		totalWords = countWords(req.Text)
	}
	if totalWords <= 0 {
		totalWords = 1
	}

	analysis := &TextAnalysisResponse{
		Ok:            true,
		Topic:         topic,
		TotalWords:    totalWords,
		TotalChapters: len(chapters),
		Chapters:      make([]TextAnalysisChapter, 0, len(chapters)),
	}

	allEntitiesSeen := make(map[string]bool)
	allEntities := make([]string, 0, 32)
	allKeywordsSeen := make(map[string]bool)
	allKeywords := make([]string, 0, 32)

	for i, ch := range chapters {
		chapterText := strings.TrimSpace(ch.SourceText)
		if chapterText == "" {
			continue
		}

		title := strings.TrimSpace(ch.Title)
		if title == "" {
			title = deriveSectionTitle(chapterText)
		}
		if title == "" {
			title = fmt.Sprintf("Chapter %d", i+1)
		}

		startPhrase, endPhrase := chapterBoundaryPhrases(chapterText)
		chapterSegment := Segment{
			Index:     i,
			Text:      chapterText,
			StartTime: ch.StartTime,
			EndTime:   ch.EndTime,
		}
		enriched := enrichSegments([]Segment{chapterSegment})
		frasi, nomi, parole := h.extractEntitiesForPipelineNoImages(enriched)

		searchTopic := strings.TrimSpace(title)
		if searchTopic == "" {
			searchTopic = topic
		}
		stockAssocs, driveAssocs, artlistAssocs, _ := h.searchClipsForPipeline(ctx, searchTopic, enriched)

		for _, n := range nomi {
			key := strings.ToLower(strings.TrimSpace(n))
			if key != "" && !allEntitiesSeen[key] {
				allEntitiesSeen[key] = true
				allEntities = append(allEntities, n)
			}
		}
		for _, p := range parole {
			key := strings.ToLower(strings.TrimSpace(p))
			if key != "" && !allKeywordsSeen[key] {
				allKeywordsSeen[key] = true
				allKeywords = append(allKeywords, p)
			}
		}

		analysis.Chapters = append(analysis.Chapters, TextAnalysisChapter{
			Index:            i,
			Title:            title,
			StartTime:        ch.StartTime,
			EndTime:          ch.EndTime,
			StartPhrase:      startPhrase,
			EndPhrase:        endPhrase,
			SentenceCount:    ch.SentenceCount,
			FrasiImportanti:  frasi,
			NomiSpeciali:     nomi,
			ParoleImportanti: parole,
			DriveAssocs:      driveAssocs,
			StockAssocs:      stockAssocs,
			ArtlistAssocs:    artlistAssocs,
		})
	}

	analysis.AllEntities = allEntities
	analysis.AllKeywords = allKeywords

	return analysis, nil
}

func splitAnalysisSections(text string, maxChapters int) []analysisSection {
	if sec := splitHeadedSections(text); len(sec) > 0 {
		return sec
	}

	sentences := scriptdocs.ExtractSentences(text)
	if len(sentences) == 0 {
		return nil
	}
	chapters := fallbackChapters(sentences, estimateDurationFromText(text), maxChapters)
	sections := make([]analysisSection, 0, len(chapters))
	for _, ch := range chapters {
		sections = append(sections, analysisSection{
			Title: ch.Title,
			Text:  strings.TrimSpace(ch.SourceText),
		})
	}
	return sections
}

func splitHeadedSections(text string) []analysisSection {
	lines := strings.Split(text, "\n")
	var sections []analysisSection
	var currentTitle string
	var bodyLines []string
	headingSeen := false

	flush := func() {
		body := strings.TrimSpace(strings.Join(bodyLines, "\n"))
		if currentTitle == "" && body == "" {
			bodyLines = nil
			return
		}
		if currentTitle == "" {
			currentTitle = deriveSectionTitle(body)
		}
		sections = append(sections, analysisSection{
			Title: currentTitle,
			Text:  strings.TrimSpace(body),
		})
		bodyLines = nil
		currentTitle = ""
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if isMarkdownHeading(trimmed) {
			headingSeen = true
			flush()
			currentTitle = cleanHeadingText(trimmed)
			continue
		}
		bodyLines = append(bodyLines, line)
	}
	flush()

	if !headingSeen || len(sections) == 0 {
		return nil
	}

	// Prefix each section body with its heading so entity and clip matching can see the subject.
	for i := range sections {
		if sections[i].Title != "" {
			sections[i].Text = strings.TrimSpace(sections[i].Title + "\n\n" + sections[i].Text)
		}
	}

	return sections
}

func isMarkdownHeading(line string) bool {
	return strings.HasPrefix(line, "###") || strings.HasPrefix(line, "##") || strings.HasPrefix(line, "# ")
}

func cleanHeadingText(line string) string {
	cleaned := strings.TrimSpace(strings.TrimLeft(line, "#"))
	return strings.TrimSpace(cleaned)
}

func deriveSectionTitle(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	sentences := scriptdocs.ExtractSentences(text)
	if len(sentences) > 0 {
		return shortPhrase(sentences[0], 10)
	}
	return shortPhrase(text, 10)
}

func chapterBoundaryPhrases(text string) (string, string) {
	sentences := scriptdocs.ExtractSentences(text)
	if len(sentences) == 0 {
		trimmed := shortPhrase(text, 12)
		return trimmed, trimmed
	}
	start := shortPhrase(sentences[0], 16)
	end := shortPhrase(sentences[len(sentences)-1], 16)
	return start, end
}

func countWords(text string) int {
	return len(strings.Fields(text))
}

func chapterTextFromAnalysis(ch TextAnalysisChapter) string {
	parts := []string{}
	if strings.TrimSpace(ch.StartPhrase) != "" {
		parts = append(parts, ch.StartPhrase)
	}
	if strings.TrimSpace(ch.EndPhrase) != "" && ch.EndPhrase != ch.StartPhrase {
		parts = append(parts, ch.EndPhrase)
	}
	if len(parts) == 0 {
		return ch.Title
	}
	return strings.Join(parts, ". ")
}
