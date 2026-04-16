// Package script fornisce modelli di dati strutturati per script con scene e metadati
package script

import "time"

// StructuredScript rappresenta uno script completo con scene strutturate
type StructuredScript struct {
	ID              string         `json:"id"`
	Title           string         `json:"title"`
	Description     string         `json:"description"`
	Language        string         `json:"language"`
	Tone            string         `json:"tone"`
	TargetDuration  int            `json:"target_duration"` // secondi
	WordCount       int            `json:"word_count"`
	Scenes          []Scene        `json:"scenes"`
	Metadata        ScriptMetadata `json:"metadata"`
	CreatedAt       time.Time      `json:"created_at"`
	Model           string         `json:"model"` // Modello LLM usato
	SourceURL       string         `json:"source_url,omitempty"` // URL YouTube se applicabile
}

// Scene rappresenta una singola scena dello script
type Scene struct {
	SceneNumber int          `json:"scene_number"`
	Type        SceneType    `json:"type"` // intro, content, conclusion, transition
	Title       string       `json:"title"`
	Text        string       `json:"text"` // Testo della narrazione
	Keywords    []string     `json:"keywords"` // Keywords estratte per questa scena
	Entities    []SceneEntity `json:"entities"` // Entità nominate (persone, luoghi, oggetti)
	Emotions    []string     `json:"emotions"` // Emozioni da trasmettere
	VisualCues  []string     `json:"visual_cues"` // Suggerimenti visivi
	Duration    int          `json:"duration"` // Durata stimata in secondi
	WordCount   int          `json:"word_count"`
	ClipMapping ClipMapping  `json:"clip_mapping"` // Mapping con clip associate
	Status      SceneStatus  `json:"status"` // pending, clips_found, clips_approved, ready
}

// EntitiesText restituisce i testi delle entità come array di stringhe
func (s *Scene) EntitiesText() []string {
 texts := make([]string, len(s.Entities))
 for i, e := range s.Entities {
  texts[i] = e.Text
 }
 return texts
}

// SceneType rappresenta il tipo di scena
type SceneType string

const (
	SceneIntro      SceneType = "intro"
	SceneContent    SceneType = "content"
	SceneConclusion SceneType = "conclusion"
	SceneTransition SceneType = "transition"
	SceneHook       SceneType = "hook" // Hook iniziale per catturare attenzione
)

// SceneStatus rappresenta lo stato della scena nel workflow
type SceneStatus string

const (
	ScenePending      SceneStatus = "pending"
	SceneClipsFound   SceneStatus = "clips_found"
	SceneClipsApproved SceneStatus = "clips_approved"
	SceneReady        SceneStatus = "ready"
	SceneNeedsReview  SceneStatus = "needs_review"
)

// SceneEntity rappresenta un'entità estratta da una scena
type SceneEntity struct {
	Text     string   `json:"text"`
	Type     string   `json:"type"` // PERSON, PLACE, ORGANIZATION, PRODUCT, CONCEPT, ACTION
	Relevance float64 `json:"relevance"` // 0-1, quanto è rilevante
	ImageURL string   `json:"image_url,omitempty"`
}

// ClipMapping rappresenta il mapping tra una scena e le clip associate
type ClipMapping struct {
	DriveClips    []ClipAssignment `json:"drive_clips"` // Clip da Google Drive
	ArtlistClips  []ClipAssignment `json:"artlist_clips"` // Clip da Artlist
	YouTubeClips  []ClipAssignment `json:"youtube_clips"` // Clip da YouTube (da scaricare)
	TikTokClips   []ClipAssignment `json:"tiktok_clips"` // Clip da TikTok (da scaricare)
	StockClips    []ClipAssignment `json:"stock_clips"` // Clip stock generiche
	Unmatched     []string         `json:"unmatched"` // Keywords/entità senza clip corrispondenti
}

// ClipAssignment rappresenta un'assegnazione di clip a una scena
type ClipAssignment struct {
	ClipID        string  `json:"clip_id"`
	Source        string  `json:"source"` // drive, artlist, youtube, tiktok, stock
	RelevanceScore float64 `json:"relevance_score"` // 0-100
	Status        string  `json:"status"` // pending, approved, rejected, downloading, downloaded
	URL           string  `json:"url,omitempty"` // URL per clip esterne (YouTube, TikTok)
	FilePath      string  `json:"file_path,omitempty"` // Path locale dopo download
	DriveFileID   string  `json:"drive_file_id,omitempty"` // ID file Drive
	DriveFolder   string  `json:"drive_folder,omitempty"` // Cartella Drive di destinazione
	ThumbnailURL  string  `json:"thumbnail_url,omitempty"`
	Duration      int     `json:"duration"` // Durata clip in secondi
	MatchReason   string  `json:"match_reason"` // Perché è stata selezionata
	ApprovedBy    string  `json:"approved_by,omitempty"` // Chi ha approvato (user o "auto")
	ApprovedAt    string  `json:"approved_at,omitempty"` // Timestamp approvazione
}

// ScriptMetadata contiene metadati aggiuntivi
type ScriptMetadata struct {
	Tags          []string `json:"tags"` // Tags generali dello script
	Category      string   `json:"category"` // Categoria (tech, business, interview, etc.)
	Difficulty    string   `json:"difficulty,omitempty"` // Livello difficoltà
	TargetAudience string  `json:"target_audience,omitempty"` // Pubblico target
	KeyMessages   []string `json:"key_messages"` // Messaggi chiave
	SEOKeywords   []string `json:"seo_keywords"` // Keywords per SEO
	RequiresApproval bool  `json:"requires_approval"` // Se richiede approvazione manuale
	TotalClipsNeeded int   `json:"total_clips_needed"` // Numero totale di clip necessarie
	ClipsFound    int     `json:"clips_found"` // Clip trovate
	ClipsApproved int     `json:"clips_approved"` // Clip approvate
	ClipsDownload int     `json:"clips_downloaded"` // Clip scaricate
}

// ClipSearchQuery rappresenta una query di ricerca per trovare clip
type ClipSearchQuery struct {
	ID          string   `json:"id"`
	SceneNumber int      `json:"scene_number"`
	Keywords    []string `json:"keywords"`
	Entities    []string `json:"entities"`
	Emotions    []string `json:"emotions"`
	VisualStyle string   `json:"visual_style"` // cinematic, documentary, casual, etc.
	MediaType   string   `json:"media_type"` // clip, stock, broll
	MaxDuration int      `json:"max_duration"` // Durata massima clip
	MinDuration int      `json:"min_duration"` // Durata minima clip
	Priority    int      `json:"priority"` // Priorità della ricerca (1-10)
}

// ClipApprovalRequest rappresenta una richiesta di approvazione per clip
type ClipApprovalRequest struct {
	SceneNumber int              `json:"scene_number"`
	SceneText   string           `json:"scene_text"`
	Clips       []ClipCandidate  `json:"clips"`
	NeedsReview bool             `json:"needs_review"` // Se richiede revisione manuale
	AutoApproved []string        `json:"auto_approved"` // Clip approvate automaticamente (score alto)
}

// ClipCandidate rappresenta una clip candidata per approvazione
type ClipCandidate struct {
	ClipID        string  `json:"clip_id"`
	Source        string  `json:"source"`
	Title         string  `json:"title"`
	ThumbnailURL  string  `json:"thumbnail_url"`
	RelevanceScore float64 `json:"relevance_score"`
	MatchReason   string  `json:"match_reason"`
	URL           string  `json:"url,omitempty"` // Per YouTube/TikTok
	Duration      int     `json:"duration"`
	Recommendation string `json:"recommendation"` // approve, review, reject
}

// ScriptProcessingStatus rappresenta lo stato di processamento di uno script
type ScriptProcessingStatus struct {
	ScriptID        string             `json:"script_id"`
	Status          string             `json:"status"` // analyzing, searching, awaiting_approval, downloading, ready
	CurrentScene    int                `json:"current_scene"`
	TotalScenes     int                `json:"total_scenes"`
	Progress        float64            `json:"progress"` // 0-100
	Errors          []string           `json:"errors,omitempty"`
	Warnings        []string           `json:"warnings,omitempty"`
	StartedAt       time.Time          `json:"started_at"`
	CompletedAt     *time.Time         `json:"completed_at,omitempty"`
}
