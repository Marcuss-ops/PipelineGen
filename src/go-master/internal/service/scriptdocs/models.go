package scriptdocs

import (
	"fmt"
	"strings"

	"velox/go-master/internal/clipsearch"
	"velox/go-master/internal/stockdb"
)

// ScriptDocRequest represents the input for script document generation.
type ScriptDocRequest struct {
	Topic            string   `json:"topic" binding:"required"`
	Duration         int      `json:"duration"`
	Languages        []string `json:"languages"` // e.g. ["it", "es"] — default ["it"]
	Template         string   `json:"template"`  // "documentary", "storytelling", "top10", "biography"
	BoostKeywords    []string `json:"boost_keywords"`
	SuppressKeywords []string `json:"suppress_keywords"`
}

const (
	MinDuration      = 30
	MaxDuration      = 180
	DefaultDuration  = 80
	MaxLanguages     = 5

	TemplateDocumentary   = "documentary"
	TemplateStorytelling  = "storytelling"
	TemplateTop10         = "top10"
	TemplateBiography     = "biography"
)

// Validate checks request validity.
func (r *ScriptDocRequest) Validate() error {
	if strings.TrimSpace(r.Topic) == "" {
		return fmt.Errorf("topic is required")
	}

	if r.Duration == 0 {
		r.Duration = DefaultDuration
	}
	if r.Duration < MinDuration || r.Duration > MaxDuration {
		return fmt.Errorf("duration must be between %d and %d seconds", MinDuration, MaxDuration)
	}

	if len(r.Languages) == 0 {
		r.Languages = []string{"it"}
	}
	if len(r.Languages) > MaxLanguages {
		return fmt.Errorf("maximum %d languages allowed", MaxLanguages)
	}

	for _, lang := range r.Languages {
		if _, ok := LanguageInfo[lang]; !ok {
			return fmt.Errorf("unsupported language: %s (supported: it, en, es, fr, de, pt, ro)", lang)
		}
	}

	if r.Template == "" {
		r.Template = TemplateDocumentary
	}
	validTemplates := map[string]bool{
		TemplateDocumentary:  true,
		TemplateStorytelling: true,
		TemplateTop10:        true,
		TemplateBiography:    true,
	}
	if !validTemplates[r.Template] {
		return fmt.Errorf("invalid template: %s (valid: documentary, storytelling, top10, biography)", r.Template)
	}

	return nil
}

// LanguageInfo maps language code to display name and prompt language.
var LanguageInfo = map[string]struct {
	Name       string
	PromptLang string // how to tell Ollama to write
}{
	"it": {"Italiano", "italiano"},
	"en": {"English", "English"},
	"es": {"Español", "español"},
	"fr": {"Français", "français"},
	"de": {"Deutsch", "Deutsch"},
	"pt": {"Português", "português"},
	"ro": {"Română", "română"},
}

// LanguageResult holds the result for a single language.
type LanguageResult struct {
	Language         string            `json:"language"`
	FullText         string            `json:"full_text"`
	FrasiImportanti  []string          `json:"frasi_importanti"`
	NomiSpeciali     []string          `json:"nomi_speciali"`
	ParoleImportant  []string          `json:"parole_importanti"`
	EntitaConImmagine map[string]string `json:"entita_con_immagine,omitempty"`
	Associations     []ClipAssociation `json:"associations"`
}

// ScriptDocResult represents the output of the pipeline.
type ScriptDocResult struct {
	DocID          string            `json:"doc_id"`
	DocURL         string            `json:"doc_url"`
	Title          string            `json:"title"`
	Languages      []LanguageResult  `json:"languages"`
	StockFolder    string            `json:"stock_folder"`
	StockFolderURL string            `json:"stock_folder_url"`
}

// ClipAssociation represents a phrase-to-clip association.
type ClipAssociation struct {
	Phrase         string                  `json:"phrase"`
	Type           string                  `json:"type"` // "DYNAMIC", "STOCK_DB", "ARTLIST", or "STOCK"
	DynamicClip    *clipsearch.SearchResult `json:"dynamic_clip,omitempty"`
	Clip           *ArtlistClip            `json:"clip,omitempty"`
	ClipDB         *stockdb.StockClipEntry `json:"clip_db,omitempty"`
	Confidence     float64                 `json:"confidence"`
	MatchedKeyword string                  `json:"matched_keyword,omitempty"`
}

// StockFolder represents a Drive Stock folder.
type StockFolder struct {
	ID   string
	Name string // e.g., "Stock/Boxe/Andrewtate"
	URL  string
}
