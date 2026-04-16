package scriptdocs

import (
	"sync"
	"time"

	"velox/go-master/internal/artlistdb"
	"velox/go-master/internal/clip"
	"velox/go-master/internal/clipsearch"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/stockdb"
	"velox/go-master/internal/upload/drive"
)

// ArtlistClip represents an Artlist clip uploaded to Drive Stock.
type ArtlistClip struct {
	Name     string `json:"name"`
	Term     string `json:"term"`
	URL      string `json:"url"`
	Folder   string `json:"folder"`
	FolderID string `json:"folder_id"`
}

// ArtlistIndex holds all Artlist clips available for association.
type ArtlistIndex struct {
	FolderID  string                   `json:"folder_id"`
	Clips     []ArtlistClip            `json:"clips"`
	CreatedAt string                   `json:"created_at,omitempty"`
	ByTerm    map[string][]ArtlistClip `json:"-"`
}

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
	MinDuration     = 30
	MaxDuration     = 180
	DefaultDuration = 80
	MaxLanguages    = 5

	TemplateDocumentary  = "documentary"
	TemplateStorytelling = "storytelling"
	TemplateTop10        = "top10"
	TemplateBiography    = "biography"
)

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
	Language          string            `json:"language"`
	FullText          string            `json:"full_text"`
	FrasiImportanti   []string          `json:"frasi_importanti"`
	NomiSpeciali      []string          `json:"nomi_speciali"`
	ParoleImportant   []string          `json:"parole_importanti"`
	EntitaConImmagine map[string]string `json:"entita_con_immagine,omitempty"`
	Associations      []ClipAssociation `json:"associations"`
}

// ScriptDocResult represents the output of the pipeline.
type ScriptDocResult struct {
	DocID          string           `json:"doc_id"`
	DocURL         string           `json:"doc_url"`
	Title          string           `json:"title"`
	Languages      []LanguageResult `json:"languages"`
	StockFolder    string           `json:"stock_folder"`
	StockFolderURL string           `json:"stock_folder_url"`
}

// ClipAssociation represents a phrase-to-clip association.
type ClipAssociation struct {
	Phrase         string                   `json:"phrase"`
	Type           string                   `json:"type"` // "DYNAMIC", "STOCK_DB", "ARTLIST", or "STOCK"
	DynamicClip    *clipsearch.SearchResult `json:"dynamic_clip,omitempty"`
	Clip           *ArtlistClip             `json:"clip,omitempty"`
	ClipDB         *stockdb.StockClipEntry  `json:"clip_db,omitempty"`
	Confidence     float64                  `json:"confidence"`
	MatchedKeyword string                   `json:"matched_keyword,omitempty"`
}

// StockFolder represents a Drive Stock folder.
type StockFolder struct {
	ID   string
	Name string // e.g., "Stock/Boxe/Andrewtate"
	URL  string
}

// ScriptDocService orchestrates the full pipeline.
type ScriptDocService struct {
	generator             *ollama.Generator
	docClient             *drive.DocClient
	artlistIndex          *ArtlistIndex
	artlistSrc            *clip.ArtlistSource
	artlistDB             *artlistdb.ArtlistDB
	stockDB               *stockdb.StockDB
	stockFolders          map[string]StockFolder
	stockFoldersMu        sync.RWMutex
	stockFoldersCacheTime time.Time
	stockFoldersCacheTTL  time.Duration
	driveClient           *drive.Client
	stockRootFolderID     string
	currentTemplate       string
	clipSearch            *clipsearch.Service
	dynamicClips          []clipsearch.SearchResult
	dynamicClipsMu        sync.Mutex
}

