// Package handlers provides HTTP handlers for the API.
package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"velox/go-master/internal/core/entities"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/nlp"
)

// NLPHandler gestisce gli endpoint per NLP
type NLPHandler struct {
	ollamaClient  *ollama.Client
	entityService *entities.EntityService
}

// NewNLPHandler crea un nuovo handler per NLP
func NewNLPHandler(ollamaClient *ollama.Client, entityService *entities.EntityService) *NLPHandler {
	return &NLPHandler{
		ollamaClient:  ollamaClient,
		entityService: entityService,
	}
}

// RegisterRoutes registra le route per NLP
func (h *NLPHandler) RegisterRoutes(rg *gin.RouterGroup) {
	nlpGroup := rg.Group("/nlp")
	{
		nlpGroup.POST("/extract-moments", h.ExtractMoments)
		nlpGroup.POST("/analyze", h.Analyze)
		nlpGroup.POST("/keywords", h.Keywords)
		nlpGroup.POST("/summarize", h.Summarize)
		nlpGroup.POST("/tokenize", h.Tokenize)
		nlpGroup.POST("/segment", h.Segment)
		if h.entityService != nil {
			nlpGroup.POST("/entities", h.ExtractEntities)
		}
	}
}

// ExtractMomentsRequest represents a request to extract moments from VTT
type ExtractMomentsRequest struct {
	VTTContent string   `json:"vtt_content" binding:"required"`
	Keywords   []string `json:"keywords"`
	Topic      string   `json:"topic"`
	MaxMoments int      `json:"max_moments"`
}

// AnalyzeRequest represents a request to analyze text
type AnalyzeRequest struct {
	Text string `json:"text" binding:"required"`
}

// SummarizeRequest represents a request to summarize text
type SummarizeRequest struct {
	Text         string `json:"text" binding:"required"`
	MaxSentences int    `json:"max_sentences"`
}

// ExtractMoments estrae momenti chiave da VTT
// @Summary Estrai momenti chiave
// @Description Estrae momenti chiave da contenuto WebVTT
// @Tags nlp
// @Accept json
// @Produce json
// @Param request body ExtractMomentsRequest true "Parametri estrazione"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Router /nlp/extract-moments [post]
func (h *NLPHandler) ExtractMoments(c *gin.Context) {
	var req ExtractMomentsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Invalid request: " + err.Error(),
		})
		return
	}

	// Defaults
	if req.MaxMoments <= 0 {
		req.MaxMoments = 5
	}

	// Parse VTT
	vtt, err := nlp.ParseVTT(req.VTTContent)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Failed to parse VTT: " + err.Error(),
		})
		return
	}

	// Estrai keyword dal topic se fornito
	keywords := req.Keywords
	if req.Topic != "" {
		topicKeywords := nlp.Tokenize(req.Topic)
		keywords = append(keywords, topicKeywords...)
	}

	// Estrai momenti
	moments := nlp.ExtractMoments(vtt, keywords, req.MaxMoments)

	c.JSON(http.StatusOK, gin.H{
		"ok":           true,
		"moments":      moments,
		"count":        len(moments),
		"total_segments": len(vtt.Segments),
	})
}

// Analyze analizza un testo
// @Summary Analizza testo
// @Description Esegue analisi completa su un testo
// @Tags nlp
// @Accept json
// @Produce json
// @Param request body AnalyzeRequest true "Testo da analizzare"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Router /nlp/analyze [post]
func (h *NLPHandler) Analyze(c *gin.Context) {
	var req AnalyzeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Invalid request: " + err.Error(),
		})
		return
	}

	// Tokenizza
	tokens := nlp.Tokenize(req.Text)
	tokensAll := nlp.TokenizeAll(req.Text)
	sentences := nlp.GetSentences(req.Text)

	// Calcola statistiche
	wordCount := len(tokensAll)
	sentenceCount := len(sentences)
	avgWordLength := nlp.AverageWordLength(req.Text)

	// Estrai keyword
	keywords := nlp.ExtractKeywords(req.Text, 10)

	// Calcola readability (semplificato)
	readability := 0.0
	if wordCount > 0 && sentenceCount > 0 {
		// Flesch-like score semplificato
		readability = 206.835 - 1.015*(float64(wordCount)/float64(sentenceCount)) - 84.6*(avgWordLength/5)
		if readability > 100 {
			readability = 100
		} else if readability < 0 {
			readability = 0
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"ok": true,
		"analysis": gin.H{
			"word_count":      wordCount,
			"sentence_count":  sentenceCount,
			"avg_word_length": avgWordLength,
			"unique_words":    len(tokens),
			"keywords":        keywords,
			"readability":     readability,
		},
	})
}

// Keywords estrae keyword da testo
// @Summary Estrai keyword
// @Description Estrae le keyword principali da un testo
// @Tags nlp
// @Accept json
// @Produce json
// @Param request body AnalyzeRequest true "Testo"
// @Param top_n query int false "Numero di keyword (default 10)"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Router /nlp/keywords [post]
func (h *NLPHandler) Keywords(c *gin.Context) {
	var req AnalyzeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Invalid request: " + err.Error(),
		})
		return
	}

	topN := 10
	if n := c.Query("top_n"); n != "" {
		// Parse topN from query
	}

	keywords := nlp.ExtractKeywords(req.Text, topN)

	c.JSON(http.StatusOK, gin.H{
		"ok":       true,
		"keywords": keywords,
		"count":    len(keywords),
	})
}

// Summarize riassume un testo
// @Summary Riassumi testo
// @Description Genera un riassunto del testo
// @Tags nlp
// @Accept json
// @Produce json
// @Param request body SummarizeRequest true "Testo da riassumere"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Router /nlp/summarize [post]
func (h *NLPHandler) Summarize(c *gin.Context) {
	var req SummarizeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Invalid request: " + err.Error(),
		})
		return
	}

	// Defaults
	if req.MaxSentences <= 0 {
		req.MaxSentences = 3
	}

	// Ottieni frasi
	sentences := nlp.GetSentences(req.Text)
	if len(sentences) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"ok":       true,
			"summary":  "",
			"sentence_count": 0,
		})
		return
	}

	// Se abbiamo poche frasi, ritorna tutto
	if len(sentences) <= req.MaxSentences {
		c.JSON(http.StatusOK, gin.H{
			"ok":       true,
			"summary":  strings.Join(sentences, " "),
			"sentence_count": len(sentences),
		})
		return
	}

	// Estrai keyword per scoring
	keywords := nlp.ExtractKeywords(req.Text, 10)
	keywordMap := make(map[string]float64)
	for _, kw := range keywords {
		keywordMap[kw.Word] = kw.Score
	}

	// Score each sentence
	type scoredSentence struct {
		text  string
		score float64
		index int
	}

	var scored []scoredSentence
	for i, sent := range sentences {
		tokens := nlp.Tokenize(sent)
		score := 0.0
		for _, t := range tokens {
			if s, ok := keywordMap[t]; ok {
				score += s
			}
		}
		// Bonus per frasi all'inizio
		if i < 2 {
			score += 5
		}
		scored = append(scored, scoredSentence{sent, score, i})
	}

	// Sort by score
	for i := 0; i < len(scored)-1; i++ {
		for j := i + 1; j < len(scored); j++ {
			if scored[j].score > scored[i].score {
				scored[i], scored[j] = scored[j], scored[i]
			}
		}
	}

	// Take top sentences
	topSentences := scored[:req.MaxSentences]

	// Sort by original order
	for i := 0; i < len(topSentences)-1; i++ {
		for j := i + 1; j < len(topSentences); j++ {
			if topSentences[j].index < topSentences[i].index {
				topSentences[i], topSentences[j] = topSentences[j], topSentences[i]
			}
		}
	}

	// Build summary
	var summaryParts []string
	for _, s := range topSentences {
		summaryParts = append(summaryParts, s.text)
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":       true,
		"summary":  strings.Join(summaryParts, " "),
		"sentence_count": len(topSentences),
		"total_sentences": len(sentences),
	})
}

// Tokenize tokenizza un testo
// @Summary Tokenizza testo
// @Description Tokenizza un testo in parole
// @Tags nlp
// @Accept json
// @Produce json
// @Param request body AnalyzeRequest true "Testo da tokenizzare"
// @Success 200 {object} map[string]interface{}
// @Router /nlp/tokenize [post]
func (h *NLPHandler) Tokenize(c *gin.Context) {
	var req AnalyzeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Invalid request: " + err.Error(),
		})
		return
	}

	tokens := nlp.Tokenize(req.Text)
	tokensAll := nlp.TokenizeAll(req.Text)

	c.JSON(http.StatusOK, gin.H{
		"ok":           true,
		"tokens":       tokens,
		"tokens_all":   tokensAll,
		"count":        len(tokens),
		"count_all":    len(tokensAll),
	})
}

// SegmentRequest represents a request to segment text
type SegmentRequest struct {
	Text               string `json:"text" binding:"required"`
	TargetWordsPerSegment int `json:"target_words_per_segment"`
}

// Segment segments text into chunks
// POST /api/nlp/segment
func (h *NLPHandler) Segment(c *gin.Context) {
	var req SegmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Invalid request: " + err.Error(),
		})
		return
	}

	config := entities.SegmentConfig{
		TargetWordsPerSegment: req.TargetWordsPerSegment,
	}
	if config.TargetWordsPerSegment <= 0 {
		config.TargetWordsPerSegment = 800
	}

	segments := h.entityService.Segmenter().Split(req.Text, config)

	c.JSON(http.StatusOK, gin.H{
		"ok":              true,
		"segments":        segments,
		"count":           len(segments),
		"words_per_segment": config.TargetWordsPerSegment,
		"total_words":     h.entityService.Segmenter().CountWords(req.Text),
	})
}

// EntityAnalysisRequest represents a request for complete entity analysis (with segmentation)
type EntityAnalysisRequest struct {
	Text                string `json:"text" binding:"required"`
	EntityCount         int    `json:"entity_count"`          // Per categoria (default: 12)
	TargetWordsPerSegment int  `json:"target_words_per_segment"` // Default: 800
}

// ExtractEntities extracts entities from text segments using entity service
// POST /api/nlp/entities
func (h *NLPHandler) ExtractEntities(c *gin.Context) {
	if h.entityService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"ok":    false,
			"error": "Entity service not available",
		})
		return
	}

	var req EntityAnalysisRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok":    false,
			"error": "Invalid request: " + err.Error(),
		})
		return
	}

	entityCount := req.EntityCount
	if entityCount <= 0 {
		entityCount = 12
	}

	wordsPerSegment := req.TargetWordsPerSegment
	if wordsPerSegment <= 0 {
		wordsPerSegment = 800
	}

	analysis, err := h.entityService.AnalyzeScript(
		c.Request.Context(),
		req.Text,
		entityCount,
		entities.SegmentConfig{
			TargetWordsPerSegment: wordsPerSegment,
		},
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"ok":    false,
			"error": "Entity analysis failed: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":                     true,
		"total_segments":         analysis.TotalSegments,
		"entity_count_per_segment": analysis.EntityCountPerSegment,
		"total_entities":         analysis.TotalEntities,
		"segment_entities":       analysis.SegmentEntities,
	})
}
