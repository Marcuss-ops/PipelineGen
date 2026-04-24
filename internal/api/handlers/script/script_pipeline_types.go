package script

// --- SHARED TYPES ---

type Segment struct {
	Index     int      `json:"index"`
	Text      string   `json:"text"`
	StartTime int      `json:"start_time,omitempty"`
	EndTime   int      `json:"end_time,omitempty"`
	Keywords  []string `json:"keywords,omitempty"`
	Entities  []Entity `json:"entities,omitempty"`
}

type Entity struct {
	Type   string `json:"type"` // "person", "place", "organization", "date", "event"
	Value  string `json:"value"`
	Source string `json:"source"` // "proper_noun", "keyword", "extracted"
}

type Translation struct {
	Language string `json:"language"`
	Text     string `json:"text"`
}

type EntityImage struct {
	Entity   string `json:"entity"`
	ImageURL string `json:"image_url"`
}

// ImageAssociation represents a chapter-linked image selected for a script.
type ImageAssociation struct {
	Phrase       string           `json:"phrase"`
	Entity       string           `json:"entity"`
	Query        string           `json:"query,omitempty"`
	ImageURL     string           `json:"image_url"`
	Source       string           `json:"source,omitempty"`
	Title        string           `json:"title,omitempty"`
	PageURL      string           `json:"page_url,omitempty"`
	StartTime    int              `json:"start_time,omitempty"`
	EndTime      int              `json:"end_time,omitempty"`
	ChapterIndex int              `json:"chapter_index,omitempty"`
	Score        float64          `json:"score,omitempty"`
	Cached       bool             `json:"cached,omitempty"`
	LocalPath    string           `json:"local_path,omitempty"`
	MimeType     string           `json:"mime_type,omitempty"`
	FileSize     int64            `json:"file_size,omitempty"`
	AssetHash    string           `json:"asset_hash,omitempty"`
	DownloadedAt string           `json:"downloaded_at,omitempty"`
	Resolution   *AssetResolution `json:"resolution,omitempty"`
}

// MixedSegment represents the chosen source for a chapter in mixed mode.
type MixedSegment struct {
	ChapterIndex int               `json:"chapter_index"`
	StartTime    int               `json:"start_time"`
	EndTime      int               `json:"end_time"`
	Phrase       string            `json:"phrase,omitempty"`
	SourceKind   string            `json:"source_kind"`
	Reason       string            `json:"reason,omitempty"`
	Confidence   float64           `json:"confidence,omitempty"`
	Clip         *ClipAssociation  `json:"clip,omitempty"`
	Image        *ImageAssociation `json:"image,omitempty"`
	Resolution   *AssetResolution  `json:"resolution,omitempty"`
}

// ClipAssociation is a minimal clip reference for mixed segments.
type ClipAssociation struct {
	Title  string  `json:"title,omitempty"`
	URL    string  `json:"url,omitempty"`
	Folder string  `json:"folder,omitempty"`
	Score  float64 `json:"score,omitempty"`
}

// AssetResolution describes how an asset was selected.
type AssetResolution struct {
	SelectedFrom   string   `json:"selected_from,omitempty"`
	SelectionOrder []string `json:"selection_order,omitempty"`
}

// --- STOCK TYPES ---

type StockClip struct {
	ClipID      string  `json:"clip_id"`
	Filename    string  `json:"filename"`
	FolderPath  string  `json:"folder_path"`
	DriveLink   string  `json:"drive_link"`
	Confidence  float64 `json:"confidence"`
	MatchedTerm string  `json:"matched_term"`
	Term        string  `json:"term"`
}

type StockAssoc struct {
	Phrase        string      `json:"phrase"`
	InitialPhrase string      `json:"initial_phrase"`
	FinalPhrase   string      `json:"final_phrase"`
	Clips         []StockClip `json:"clips"`
}

type SegmentStock struct {
	SegmentIndex  int         `json:"segment_index"`
	InitialPhrase string      `json:"initial_phrase"`
	FinalPhrase   string      `json:"final_phrase"`
	Clips         []StockClip `json:"clips"`
}

// --- DRIVE TYPES ---

type DriveFolderAssoc struct {
	Phrase        string `json:"phrase"`
	InitialPhrase string `json:"initial_phrase,omitempty"`
	FinalPhrase   string `json:"final_phrase,omitempty"`
	FolderName    string `json:"folder_name"`
	FolderURL     string `json:"folder_url"`
}

// --- ARTLIST TYPES ---

type ArtlistClipRef struct {
	ClipID    string  `json:"clip_id"`
	Name      string  `json:"name"`
	Term      string  `json:"term"`
	URL       string  `json:"url"`
	Folder    string  `json:"folder"`
	Timestamp string  `json:"timestamp"`
	Score     float64 `json:"score"`
	Source    string  `json:"source"`
}

type ArtlistAssoc struct {
	Phrase string           `json:"phrase"`
	Clips  []ArtlistClipRef `json:"clips"`
}

type SegmentArtlistRef struct {
	SegmentIndex int              `json:"segment_index"`
	Clips        []ArtlistClipRef `json:"clips"`
	SearchTerms  []string         `json:"search_terms,omitempty"`
}

// --- DOCUMENT TYPES ---

type CreateDocumentRequest struct {
	Title             string             `json:"title" binding:"required"`
	Topic             string             `json:"topic"`
	Duration          int                `json:"duration"`
	Template          string             `json:"template"`
	Script            string             `json:"script"`
	SourceText        string             `json:"source_text"`
	Language          string             `json:"language"`
	Segments          []Segment          `json:"segments"`
	Entities          []Entity           `json:"entities"`
	StockClips        []StockClip        `json:"stock_clips"`
	StockAssocs       []StockAssoc       `json:"stock_assocs"`
	StockDriveAssocs  []DriveFolderAssoc `json:"stock_drive_assocs"`
	ClipDriveAssocs   []DriveFolderAssoc `json:"clip_drive_assocs"`
	ArtlistClips      []ArtlistClipRef   `json:"artlist_clips"`
	Translations      []Translation      `json:"translations"`
	StockFolder       string             `json:"stock_folder"`
	StockFolderURL    string             `json:"stock_folder_url"`
	FrasiImportanti   []string           `json:"frasi_importanti"`
	NomiSpeciali      []string           `json:"nomi_speciali"`
	ParoleImportanti  []string           `json:"parole_importanti"`
	EntitaConImmagine []EntityImage      `json:"entita_con_immagine"`
	ArtlistAssocs     []ArtlistAssoc     `json:"artlist_assocs"`
	ImageAssociations []ImageAssociation `json:"image_associations,omitempty"`
	MixedSegments     []MixedSegment     `json:"mixed_segments,omitempty"`
	PreviewOnly       bool               `json:"preview_only"`
	SkipEnrichment    bool               `json:"skip_enrichment,omitempty"`
	MinimalDoc        bool               `json:"minimal_doc,omitempty"`
}

type CreateDocumentResponse struct {
	Ok          bool   `json:"ok"`
	DocID       string `json:"doc_id"`
	DocURL      string `json:"doc_url"`
	PreviewPath string `json:"preview_path,omitempty"`
	Mode        string `json:"mode,omitempty"`
}

type GenerateDocResponse struct {
	Ok          bool   `json:"ok"`
	DocID       string `json:"doc_id"`
	DocURL      string `json:"doc_url"`
	Script      string `json:"script"`
	WordCount   int    `json:"word_count"`
	EstDuration int    `json:"est_duration"`
	Model       string `json:"model,omitempty"`
	Language    string `json:"language,omitempty"`
}

type ReviewDraftRequest struct {
	Title      string `json:"title" binding:"required"`
	Topic      string `json:"topic"`
	SourceText string `json:"source_text"`
	Language   string `json:"language"`
	Duration   int    `json:"duration"`
}

type ReviewDraftResponse struct {
	Ok      bool                  `json:"ok"`
	Draft   CreateDocumentRequest `json:"draft"`
	Message string                `json:"message"`
}

// --- FULL PIPELINE TYPES ---

type FullPipelineRequest struct {
	Topic    string `json:"topic" binding:"required"`
	Text     string `json:"text"`
	Duration int    `json:"duration"`
	Title    string `json:"title"`
	Language string `json:"language"`
}

type FullPipelineResponse struct {
	Ok                bool   `json:"ok"`
	DocURL            string `json:"doc_url"`
	SegmentsCount     int    `json:"segments_count"`
	StockClipsFound   int    `json:"stock_clips_found"`
	ArtlistClipsFound int    `json:"artlist_clips_found"`
	EntitiesFound     int    `json:"entities_found"`
}

// --- CHAPTER PLANNING TYPES ---

type ChapterPlanRequest struct {
	Topic          string `json:"topic,omitempty"`
	Text           string `json:"text" binding:"required"`
	SourceLanguage string `json:"source_language,omitempty"`
	TargetLanguage string `json:"target_language,omitempty"`
	Duration       int    `json:"duration,omitempty"`
	MaxChapters    int    `json:"max_chapters,omitempty"`
	Model          string `json:"model,omitempty"`
}

type ChapterPlan struct {
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

type ChapterPlanResponse struct {
	Ok               bool          `json:"ok"`
	Topic            string        `json:"topic,omitempty"`
	SourceLanguage   string        `json:"source_language,omitempty"`
	TargetLanguage   string        `json:"target_language,omitempty"`
	Model            string        `json:"model,omitempty"`
	TotalSentences   int           `json:"total_sentences"`
	Chapters         []ChapterPlan `json:"chapters"`
	TranslatedScript string        `json:"translated_script,omitempty"`
}

// --- DOWNLOAD TYPES ---

type DownloadClipsRequest struct {
	Clips        []StockClip      `json:"clips"`
	ArtlistClips []ArtlistClipRef `json:"artlist_clips"`
	Destination  string           `json:"destination"`
}

type DownloadClipsResponse struct {
	Ok          bool     `json:"ok"`
	Downloaded  []string `json:"downloaded"`
	Failed      []string `json:"failed"`
	DownloadURL string   `json:"download_url,omitempty"`
}
