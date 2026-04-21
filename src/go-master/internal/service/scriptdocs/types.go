package scriptdocs

import (
	"context"
	"sync"
	"time"

	"velox/go-master/internal/artlistdb"
	"velox/go-master/internal/clip"
	"velox/go-master/internal/clipsearch"
	"velox/go-master/internal/imagesasset"
	"velox/go-master/internal/imagesdb"
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
	Languages        []string `json:"languages"`
	Template         string   `json:"template"`
	AssociationMode  string   `json:"association_mode,omitempty"`
	BoostKeywords    []string `json:"boost_keywords"`
	SuppressKeywords []string `json:"suppress_keywords"`
}

const (
	MinDuration     = 30
	MaxDuration     = 600
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
	PromptLang string
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
	Language            string             `json:"language"`
	FullText            string             `json:"full_text"`
	Chapters            []ScriptChapter    `json:"chapters,omitempty"`
	FrasiImportanti     []string           `json:"frasi_importanti"`
	NomiSpeciali        []string           `json:"nomi_speciali"`
	ParoleImportant     []string           `json:"parole_importanti"`
	EntitaConImmagine   map[string]string  `json:"entita_con_immagine,omitempty"`
	Associations        []ClipAssociation  `json:"associations"`
	StockAssociations   []ClipAssociation  `json:"stock_associations,omitempty"`
	ArtlistAssociations []ClipAssociation  `json:"artlist_associations,omitempty"`
	ArtlistTimeline     []ArtlistTimeline  `json:"artlist_timeline,omitempty"`
	ImageAssociations   []ImageAssociation `json:"image_associations,omitempty"`
}

// ArtlistTimeline groups content windows by timestamp to direct Artlist folders.
type ArtlistTimeline struct {
	Timestamp  string `json:"timestamp"`
	Keyword    string `json:"keyword"`
	FolderName string `json:"folder_name"`
	FolderURL  string `json:"folder_url"`
}

// ImageAssociation represents a chapter-linked image selected for a script.
type ImageAssociation struct {
	Phrase       string  `json:"phrase"`
	Entity       string  `json:"entity"`
	Query        string  `json:"query,omitempty"`
	ImageURL     string  `json:"image_url"`
	Source       string  `json:"source,omitempty"`
	Title        string  `json:"title,omitempty"`
	PageURL      string  `json:"page_url,omitempty"`
	StartTime    int     `json:"start_time,omitempty"`
	EndTime      int     `json:"end_time,omitempty"`
	ChapterIndex int     `json:"chapter_index,omitempty"`
	Score        float64 `json:"score,omitempty"`
	Cached       bool    `json:"cached,omitempty"`
	LocalPath    string  `json:"local_path,omitempty"`
	MimeType     string  `json:"mime_type,omitempty"`
	FileSize     int64   `json:"file_size,omitempty"`
	AssetHash    string  `json:"asset_hash,omitempty"`
	DownloadedAt string  `json:"downloaded_at,omitempty"`
}

// ImagePlan aggregates the full image routing plan for a script doc request.
type ImagePlan struct {
	Topic             string          `json:"topic"`
	Duration          int             `json:"duration"`
	AssociationMode   string          `json:"association_mode"`
	CreatedAt         string          `json:"created_at"`
	Languages         []ImagePlanLang `json:"languages"`
	TotalAssociations int             `json:"total_associations"`
	TotalCached       int             `json:"total_cached"`
	TotalDownloaded   int             `json:"total_downloaded"`
}

// ImagePlanLang holds the image plan for a single language.
type ImagePlanLang struct {
	Language           string             `json:"language"`
	Chapters           []ImagePlanChapter `json:"chapters,omitempty"`
	Associations       []ImageAssociation `json:"associations"`
	TotalAssociations  int                `json:"total_associations"`
	CachedAssociations int                `json:"cached_associations"`
	Downloaded         int                `json:"downloaded"`
}

// ImagePlanChapter mirrors the semantic chapter used for image selection.
type ImagePlanChapter struct {
	Index            int      `json:"index"`
	Title            string   `json:"title"`
	StartTime        int      `json:"start_time"`
	EndTime          int      `json:"end_time"`
	Confidence       float64  `json:"confidence,omitempty"`
	SourceText       string   `json:"source_text,omitempty"`
	DominantEntities []string `json:"dominant_entities,omitempty"`
}

type imageFinderAPI interface {
	Find(string) string
}

type imageAssetDownloaderAPI interface {
	Download(ctx context.Context, rec imagesdb.ImageRecord) (*imagesasset.Result, error)
}

// ScriptDocResult represents the output of the pipeline.
type ScriptDocResult struct {
	DocID          string           `json:"doc_id"`
	DocURL         string           `json:"doc_url"`
	Title          string           `json:"title"`
	Languages      []LanguageResult `json:"languages"`
	StockFolder    string           `json:"stock_folder"`
	StockFolderURL string           `json:"stock_folder_url"`
	ImagePlan      *ImagePlan       `json:"image_plan,omitempty"`
	ImagePlanPath  string           `json:"image_plan_path,omitempty"`
}

// ClipAssociation represents a phrase-to-clip association.
type ClipAssociation struct {
	Phrase         string                   `json:"phrase"`
	Type           string                   `json:"type"`
	DynamicClip    *clipsearch.SearchResult `json:"dynamic_clip,omitempty"`
	Clip           *ArtlistClip             `json:"clip,omitempty"`
	ClipDB         *stockdb.StockClipEntry  `json:"clip_db,omitempty"`
	StockFolder    *StockFolder             `json:"stock_folder,omitempty"`
	Confidence     float64                  `json:"confidence"`
	MatchedKeyword string                   `json:"matched_keyword,omitempty"`
}

// StockFolder represents a Drive Stock folder.
type StockFolder struct {
	ID   string
	Name string
	URL  string
}

// ScriptDocService orchestrates the full pipeline.
type ScriptDocService struct {
	generator              *ollama.Generator
	docClient              *drive.DocClient
	artlistIndex           *ArtlistIndex
	artlistSrc             *clip.ArtlistSource
	artlistDB              *artlistdb.ArtlistDB
	imagesDB               *imagesdb.ImageDB
	stockDB                *stockdb.StockDB
	stockFolders           map[string]StockFolder
	stockFoldersMu         sync.RWMutex
	stockFoldersCacheTime  time.Time
	stockFoldersCacheTTL   time.Duration
	driveClient            *drive.Client
	stockRootFolderID      string
	currentTemplate        string
	currentAssociationMode string
	currentTopic           string
	folderTopic            string
	clipSearch             *clipsearch.Service
	dynamicClips           []clipsearch.SearchResult
	dynamicClipsMu         sync.Mutex
	imageFinder            imageFinderAPI
	imageDownloader        imageAssetDownloaderAPI
}

// ScriptChapter represents a semantic chapter in a generated script.
type ScriptChapter struct {
	Index            int      `json:"index"`
	Title            string   `json:"title"`
	StartSentence    int      `json:"start_sentence"`
	EndSentence      int      `json:"end_sentence"`
	StartTime        int      `json:"start_time"`
	EndTime          int      `json:"end_time"`
	SentenceCount    int      `json:"sentence_count"`
	DominantEntities []string `json:"dominant_entities,omitempty"`
	Summary          string   `json:"summary,omitempty"`
	Confidence       float64  `json:"confidence,omitempty"`
	SourceText       string   `json:"source_text,omitempty"`
	TranslatedText   string   `json:"translated_text,omitempty"`
}
