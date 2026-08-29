// Package script — vidrush.go owns the canonical per-segment VidRush
// result shapes used by script generation.
package script

import (
	"errors"
	"strings"
)

// VidRush providers are intentionally a closed set. The application layer
// owns the orchestration contract; infrastructure registers concrete
// implementations at the composition root.
const (
	VidRushProviderArtlist         = "artlist"
	VidRushProviderInternetImages  = "internet_images"
	VidRushProviderImageGeneration = "image_generation"
	VidRushProviderYouTube         = "youtube"
)

// VidRushArtifactStatus is the lifecycle state of a candidate as it moves
// from discovery to a scene binding. Empty is retained for compatibility with
// legacy read-only candidate payloads; new providers must populate the state.
type VidRushArtifactStatus string

const (
	VidRushStatusCandidateFound = "candidate_found"
	VidRushStatusAcquired       = "acquired"
	VidRushStatusVerified       = "verified"
	VidRushStatusPersisted      = "persisted"
	VidRushStatusIndexed        = "indexed"
	VidRushStatusBound          = "bound"
	VidRushStatusFailed         = "failed"
)

// CanonicalSegment is the immutable identity boundary used by VidRush
// extraction, visual planning and asset binding. SourceText preserves the
// narrative/source wording that produced the segment; Text is the text sent
// to the visual/semantic pipeline. Position is zero-based and stable within
// the canonical segment list.
type CanonicalSegment struct {
	ID              string   `json:"segment_id"`
	SceneID         string   `json:"scene_id,omitempty"`
	Position        int      `json:"position"`
	Text            string   `json:"text"`
	SourceText      string   `json:"source_text,omitempty"`
	TextHash        string   `json:"text_hash"`
	SourceTextHash  string   `json:"source_text_hash,omitempty"`
	ArtlistKeywords []string `json:"artlist_keywords,omitempty"`
}

// Validate verifies the stable segment identity and hash contract.
func (s CanonicalSegment) Validate() error {
	if strings.TrimSpace(s.ID) == "" {
		return errors.New("canonical segment: segment_id is required")
	}
	if s.Position < 0 {
		return errors.New("canonical segment: position must not be negative")
	}
	if strings.TrimSpace(s.Text) == "" {
		return errors.New("canonical segment: text is required")
	}
	if strings.TrimSpace(s.TextHash) == "" {
		return errors.New("canonical segment: text_hash is required")
	}
	if source := strings.TrimSpace(s.SourceText); source != "" && strings.TrimSpace(s.SourceTextHash) == "" {
		return errors.New("canonical segment: source_text_hash is required when source_text is present")
	}
	return nil
}

// NormalizeCanonicalSegment trims textual fields and fills the source text
// and hashes deterministically. It does not mutate the receiver.
func NormalizeCanonicalSegment(segment CanonicalSegment) CanonicalSegment {
	segment.ID = strings.TrimSpace(segment.ID)
	segment.SceneID = strings.TrimSpace(segment.SceneID)
	segment.Text = strings.TrimSpace(segment.Text)
	segment.SourceText = strings.TrimSpace(segment.SourceText)
	if segment.SourceText == "" {
		segment.SourceText = segment.Text
	}
	if segment.TextHash == "" {
		segment.TextHash = ComputeCanonicalSegmentTextHash(segment.Text)
	}
	if segment.SourceTextHash == "" {
		segment.SourceTextHash = ComputeCanonicalSegmentTextHash(segment.SourceText)
	}
	segment.ArtlistKeywords = append([]string(nil), segment.ArtlistKeywords...)
	return segment
}

// ComputeCanonicalSegmentTextHash computes the stable hash used by the
// VidRush segment identity boundary. Formatting whitespace is normalized so
// equivalent source text replays the same segment cache key.
func ComputeCanonicalSegmentTextHash(text string) string {
	return ComputeSourceHash(strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(text))), " "))
}

// ExtractedEntity is a typed entity extracted from one segment.
type ExtractedEntity struct {
	Value      string  `json:"value"`
	Type       string  `json:"type"`
	Confidence float64 `json:"confidence"`
}

// EntityMediaLink joins an NLP surface entity to a canonical media identity.
// It is enrichment metadata and never mutates ExtractedEntity.
type EntityMediaLink struct {
	SurfaceValue      string   `json:"surface_value"`
	EntityType        string   `json:"entity_type"`
	CanonicalEntityID string   `json:"canonical_entity_id"`
	AssetIDs          []string `json:"asset_ids,omitempty"`
}

// SegmentInsights collects the per-segment semantic extractions and
// generated queries used by VidRush.
type SegmentInsights struct {
	SegmentID                string              `json:"segment_id"`
	TextHash                 string              `json:"text_hash"`
	Entities                 []ExtractedEntity   `json:"entities,omitempty"`
	ImportantPhrases         []string            `json:"important_phrases,omitempty"`
	ImportantWords           []string            `json:"important_words,omitempty"`
	ArtlistQueries           []string            `json:"artlist_queries,omitempty"`
	YouTubeQueries           []string            `json:"youtube_queries,omitempty"`
	ArtlistIntentHash        string              `json:"artlist_intent_hash,omitempty"`
	ImageQueries             []string            `json:"image_queries,omitempty"`
	ImageSearchRequired      bool                `json:"image_search_required,omitempty"`
	ImageSearchNoImageReason string              `json:"image_search_no_image_reason,omitempty"`
	ImagePrimaryCanonicalID  string              `json:"image_primary_canonical_id,omitempty"`
	ImageEntityCanonicalIDs  map[string]string   `json:"image_entity_canonical_ids,omitempty"`
	ResearchSources          []ResearchWebSource `json:"research_sources,omitempty"`
	EntityMediaLinks         []EntityMediaLink   `json:"entity_media_links,omitempty"`
}

// SegmentAssetCandidate is a single candidate found for a segment.
type SegmentAssetCandidate struct {
	AssetID               string  `json:"asset_id"`
	Provider              string  `json:"provider"`
	Query                 string  `json:"query,omitempty"`
	Entity                string  `json:"entity,omitempty"`
	Score                 float64 `json:"score"`
	RelevanceScore        float64 `json:"relevance_score,omitempty"`
	TechnicalQualityScore float64 `json:"technical_quality_score,omitempty"`
	RightsScore           float64 `json:"rights_score,omitempty"`
	DiversityScore        float64 `json:"diversity_score,omitempty"`
	ProviderReliability   float64 `json:"provider_reliability,omitempty"`
	SemanticStatus        string  `json:"semantic_status,omitempty"`
	SemanticScore         float64 `json:"semantic_score,omitempty"`
	QualityReason         string  `json:"quality_reason,omitempty"`
	SourceURL             string  `json:"source_url,omitempty"`
	SourcePageURL         string  `json:"source_page_url,omitempty"`
	SourceStartMs         int64   `json:"source_start_ms,omitempty"`
	SourceEndMs           int64   `json:"source_end_ms,omitempty"`
	PreviewURL            string  `json:"preview_url,omitempty"`
	DriveLink             string  `json:"drive_link,omitempty"`
	DurationMs            int64   `json:"duration_ms,omitempty"`
	Width                 int     `json:"width,omitempty"`
	Height                int     `json:"height,omitempty"`
	RightsStatus          string  `json:"rights_status,omitempty"`
	SelectionReason       string  `json:"selection_reason,omitempty"`
	CandidateSetHash      string  `json:"candidate_set_hash,omitempty"`
	LegacyFileMD5         string  `json:"legacy_file_md5,omitempty"`
	MIMEType              string  `json:"mime_type,omitempty"`
	LocalPath             string  `json:"local_path,omitempty"`
	AcquisitionStatus     string  `json:"acquisition_status,omitempty"`
	VerificationStatus    string  `json:"verification_status,omitempty"`
	PersistenceStatus     string  `json:"persistence_status,omitempty"`
	IndexStatus           string  `json:"index_status,omitempty"`
	RightsBasis           string  `json:"rights_basis,omitempty"`
}

// SegmentAssetSelection is the winning asset bundle for a segment.
type SegmentAssetSelection struct {
	PrimaryVideo     *SegmentAssetCandidate  `json:"primary_video,omitempty"`
	SecondaryImages  []SegmentAssetCandidate `json:"secondary_images,omitempty"`
	GeneratedImages  []SegmentAssetCandidate `json:"generated_images,omitempty"`
	Candidates       []SegmentAssetCandidate `json:"candidates,omitempty"`
	CandidateSetHash string                  `json:"candidate_set_hash,omitempty"`
	SelectionReason  string                  `json:"selection_reason,omitempty"`
}

// SegmentCacheState stores the per-segment cache status across the VidRush steps.
type SegmentCacheState struct {
	Extraction                     string `json:"extraction,omitempty"`
	Artlist                        string `json:"artlist,omitempty"`
	InternetImages                 string `json:"internet_images,omitempty"`
	ImageGeneration                string `json:"image_generation,omitempty"`
	YouTube                        string `json:"youtube,omitempty"`
	Binding                        string `json:"binding,omitempty"`
	InternetImagesProviderSearches int    `json:"internet_images_provider_searches,omitempty"`
	InternetImagesNewUploads       int    `json:"internet_images_new_uploads,omitempty"`
}

// VidRushSegmentResult is the full per-segment output surfaced by script generation.
type VidRushSegmentResult struct {
	SegmentID string                `json:"segment_id"`
	SceneID   string                `json:"scene_id,omitempty"`
	Position  int                   `json:"position"`
	Text      string                `json:"text"`
	TextHash  string                `json:"text_hash"`
	Insights  SegmentInsights       `json:"insights"`
	Assets    SegmentAssetSelection `json:"assets"`
	Cache     SegmentCacheState     `json:"cache"`
}
