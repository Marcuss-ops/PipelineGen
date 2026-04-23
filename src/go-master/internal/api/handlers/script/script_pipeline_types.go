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

type AssociateStockRequest struct {
	Segments []Segment `json:"segments"`
	Entities []string  `json:"entities"`
	Topic    string    `json:"topic"`
}

type AssociateStockResponse struct {
	Ok             bool               `json:"ok"`
	SegmentData    []SegmentStock     `json:"segment_data"`
	AllClips       []StockClip        `json:"all_clips"`
	DriveAssocs    []DriveFolderAssoc `json:"drive_assocs,omitempty"`
	StockFolder    string             `json:"stock_folder,omitempty"`
	StockFolderURL string             `json:"stock_folder_url,omitempty"`
}

// --- DRIVE TYPES ---

type DriveFolderAssoc struct {
	Phrase     string `json:"phrase"`
	FolderName string `json:"folder_name"`
	FolderURL  string `json:"folder_url"`
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

type AssociateArtlistRequest struct {
	Segments []Segment `json:"segments"`
	Entities []string  `json:"entities"`
	Topic    string    `json:"topic"`
}

type AssociateArtlistResponseRef struct {
	Ok          bool                `json:"ok"`
	SegmentData []SegmentArtlistRef `json:"segment_data"`
	AllClips    []ArtlistClipRef    `json:"all_clips"`
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
	DriveAssocs       []DriveFolderAssoc `json:"drive_assocs"`
	ArtlistClips      []ArtlistClipRef   `json:"artlist_clips"`
	Translations      []Translation      `json:"translations"`
	StockFolder       string             `json:"stock_folder"`
	StockFolderURL    string             `json:"stock_folder_url"`
	FrasiImportanti   []string           `json:"frasi_importanti"`
	NomiSpeciali      []string           `json:"nomi_speciali"`
	ParoleImportanti  []string           `json:"parole_importanti"`
	EntitaConImmagine []EntityImage      `json:"entita_con_immagine"`
	ArtlistAssocs     []ArtlistAssoc     `json:"artlist_assocs"`
	PreviewOnly       bool               `json:"preview_only"`
}

type CreateDocumentResponse struct {
	Ok          bool   `json:"ok"`
	DocID       string `json:"doc_id"`
	DocURL      string `json:"doc_url"`
	PreviewPath string `json:"preview_path,omitempty"`
	Mode        string `json:"mode,omitempty"`
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
