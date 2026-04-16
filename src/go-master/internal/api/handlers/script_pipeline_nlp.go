package handlers

import (
	"net/http"
	"strings"
	"unicode"

	"github.com/gin-gonic/gin"
	"velox/go-master/internal/service/scriptdocs"
)

type DivideRequest struct {
	Script      string `json:"script" binding:"required"`
	MaxSegments int    `json:"max_segments"`
}

type DivideResponse struct {
	Ok       bool      `json:"ok"`
	Segments []Segment `json:"segments"`
	Count    int       `json:"count"`
}

func (h *ScriptPipelineHandler) DivideIntoSegments(c *gin.Context) {
	var req DivideRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	if req.MaxSegments == 0 {
		req.MaxSegments = 3
	}

	sentences := scriptdocs.ExtractSentences(req.Script)
	if len(sentences) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "no sentences found"})
		return
	}

	var segments []Segment
	avgDuration := 60 / len(sentences)
	for i, sentence := range sentences {
		if i >= req.MaxSegments {
			break
		}
		segments = append(segments, Segment{
			Index:     i,
			Text:      sentence,
			StartTime: i * avgDuration,
			EndTime:   (i + 1) * avgDuration,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":       true,
		"segments": segments,
		"count":    len(segments),
	})
}

type ExtractEntitiesRequest struct {
	Segments    []Segment `json:"segments" binding:"required"`
	MaxEntities int       `json:"max_entities"`
}

type SegmentEntities struct {
	SegmentIndex int      `json:"segment_index"`
	Text         string   `json:"text"`
	Entities     []Entity `json:"entities"`
}

type ExtractEntitiesResponse struct {
	Ok                bool              `json:"ok"`
	SegmentData       []SegmentEntities `json:"segment_data"`
	AllEntities       []string          `json:"all_entities"`
	Keywords          []string          `json:"keywords"`
	FrasiImportanti   []string          `json:"frasi_importanti"`
	NomiSpeciali      []string          `json:"nomi_speciali"`
	ParoleImportanti  []string          `json:"parole_importanti"`
	EntitaConImmagine []EntityWithImage `json:"entita_con_immagine"`
}

type EntityWithImage struct {
	Entity   string `json:"entity"`
	ImageURL string `json:"image_url"`
}

func (h *ScriptPipelineHandler) ExtractEntities(c *gin.Context) {
	var req ExtractEntitiesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	if req.MaxEntities == 0 {
		req.MaxEntities = 3
	}

	var segmentData []SegmentEntities
	var allEntities []string
	seenEntity := make(map[string]bool)

	for _, seg := range req.Segments {
		text := seg.Text

		nomiSpeciali := scriptdocs.ExtractProperNouns([]string{text})
		paroleImportanti := scriptdocs.ExtractKeywords(text)

		var entities []Entity
		for _, name := range nomiSpeciali {
			if len(entities) >= req.MaxEntities {
				break
			}
			entityType := "person"
			lower := strings.ToLower(name)
			if strings.Contains(lower, "arena") || strings.Contains(lower, "stadium") ||
				strings.Contains(lower, "city") || strings.Contains(lower, "baltimore") {
				entityType = "place"
			}
			entities = append(entities, Entity{
				Type:   entityType,
				Value:  name,
				Source: "proper_noun",
			})
			if !seenEntity[name] {
				seenEntity[name] = true
				allEntities = append(allEntities, name)
			}
		}

		for _, kw := range paroleImportanti {
			if len(entities) >= req.MaxEntities {
				break
			}
			entities = append(entities, Entity{
				Type:   "keyword",
				Value:  kw,
				Source: "keyword",
			})
			if !seenEntity[kw] {
				seenEntity[kw] = true
				allEntities = append(allEntities, kw)
			}
		}

		segmentData = append(segmentData, SegmentEntities{
			SegmentIndex: seg.Index,
			Text:         text,
			Entities:     entities,
		})
	}

	frasiImportanti := make([]string, 0)
	nomiSpecialiAll := make([]string, 0)
	paroleImportantiAll := make([]string, 0)

	for _, seg := range req.Segments {
		if len(seg.Text) > 20 {
			frasiImportanti = append(frasiImportanti, seg.Text)
		}
	}

	for _, seg := range req.Segments {
		nomiSpecialiAll = append(nomiSpecialiAll, scriptdocs.ExtractProperNouns([]string{seg.Text})...)
		paroleImportantiAll = append(paroleImportantiAll, scriptdocs.ExtractKeywords(seg.Text)...)
	}

	uniqueNomi := make([]string, 0)
	seenNomi := make(map[string]bool)
	for _, n := range nomiSpecialiAll {
		lower := strings.ToLower(n)
		if !seenNomi[lower] && len(n) > 2 {
			seenNomi[lower] = true
			uniqueNomi = append(uniqueNomi, n)
		}
	}

	uniqueParole := make([]string, 0)
	seenParole := make(map[string]bool)
	for _, p := range paroleImportantiAll {
		lower := strings.ToLower(p)
		if !seenParole[lower] && len(p) > 2 {
			seenParole[lower] = true
			uniqueParole = append(uniqueParole, p)
		}
	}

	entitaConImmagine := make([]EntityWithImage, 0)
	allSentences := make([]string, 0)
	for _, seg := range req.Segments {
		allSentences = append(allSentences, seg.Text)
	}
	if len(allSentences) > 0 {
		entityImages := scriptdocs.ExtractEntitiesWithImages(allSentences)
		for entity, imageURL := range entityImages {
			if imageURL != "" {
				entitaConImmagine = append(entitaConImmagine, EntityWithImage{
					Entity:   entity,
					ImageURL: imageURL,
				})
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":                  true,
		"segment_data":        segmentData,
		"all_entities":        allEntities,
		"keywords":            allEntities,
		"frasi_importanti":    frasiImportanti,
		"nomi_speciali":       uniqueNomi,
		"parole_importanti":   uniqueParole,
		"entita_con_immagine": entitaConImmagine,
	})
}

type FindKeyPhrasesRequest struct {
	Script   string   `json:"script" binding:"required"`
	Entities []string `json:"entities"`
}

type KeyPhrase struct {
	Phrase     string  `json:"phrase"`
	Type       string  `json:"type"` // "direct", "synonym", "related"
	Confidence float64 `json:"confidence"`
}

type FindKeyPhrasesResponse struct {
	Ok         bool        `json:"ok"`
	KeyPhrases []KeyPhrase `json:"key_phrases"`
	Count      int         `json:"count"`
}

func (h *ScriptPipelineHandler) FindKeyPhrases(c *gin.Context) {
	var req FindKeyPhrasesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}

	sentences := scriptdocs.ExtractSentences(req.Script)

	var keyPhrases []KeyPhrase
	seen := make(map[string]bool)

	for _, entity := range req.Entities {
		if !seen[strings.ToLower(entity)] {
			seen[strings.ToLower(entity)] = true
			keyPhrases = append(keyPhrases, KeyPhrase{
				Phrase:     entity,
				Type:       "direct",
				Confidence: 1.0,
			})
		}
	}

	for _, sentence := range sentences {
		words := strings.Fields(sentence)
		for _, word := range words {
			clean := strings.TrimFunc(word, func(r rune) bool {
				return !unicode.IsLetter(r) && !unicode.IsDigit(r)
			})
			if len(clean) > 5 && !seen[strings.ToLower(clean)] {
				seen[strings.ToLower(clean)] = true
				keyPhrases = append(keyPhrases, KeyPhrase{
					Phrase:     clean,
					Type:       "related",
					Confidence: 0.7,
				})
			}
		}
	}

	if len(keyPhrases) > 20 {
		keyPhrases = keyPhrases[:20]
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":          true,
		"key_phrases": keyPhrases,
		"count":       len(keyPhrases),
	})
}
