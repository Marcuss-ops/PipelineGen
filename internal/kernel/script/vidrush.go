// Package script — vidrush.go owns the canonical per-segment VidRush
// result shapes used by script generation.
package script

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

// CanonicalSegment is the stable segment representation used for
// VidRush extraction and asset binding.
type CanonicalSegment struct {
	ID              string   `json:"segment_id"`
	SceneID         string   `json:"scene_id,omitempty"`
	Position        int      `json:"position"`
	Text            string   `json:"text"`
	TextHash        string   `json:"text_hash"`
	ArtlistKeywords []string `json:"artlist_keywords,omitempty"`
}

// ExtractedEntity is a typed entity extracted from one segment.
type ExtractedEntity struct {
	Value      string  `json:"value"`
	Type       string  `json:"type"`
	Confidence float64 `json:"confidence"`
}

// SegmentInsights collects the per-segment semantic extractions and
// generated queries used by VidRush.
type SegmentInsights struct {
	SegmentID         string            `json:"segment_id"`
	TextHash          string            `json:"text_hash"`
	Entities          []ExtractedEntity `json:"entities,omitempty"`
	ImportantPhrases  []string          `json:"important_phrases,omitempty"`
	ImportantWords    []string          `json:"important_words,omitempty"`
	ArtlistQueries    []string          `json:"artlist_queries,omitempty"`
	YouTubeQueries    []string          `json:"youtube_queries,omitempty"`
	ArtlistIntentHash string            `json:"artlist_intent_hash,omitempty"`
	ImageQueries      []string          `json:"image_queries,omitempty"`
	// ImageSearchRequired is the deterministic image search decision of the
	// Image Search Intent resolver (capabilities/imagesearch): false when the
	// scene carries no visual entity (abstract/editorial text). When false,
	// ImageQueries holds only the compact B-roll fallback; the flag itself is
	// the decision the visual scheduler consumes. Omitted (false) when the
	// resolver is not wired.
	ImageSearchRequired bool `json:"image_search_required,omitempty"`
	// ImageSearchNoImageReason explains a Required=false decision
	// (e.g. "no_visual_entity", "pronoun_without_antecedent").
	ImageSearchNoImageReason string `json:"image_search_no_image_reason,omitempty"`
	// ImagePrimaryCanonicalID is the canonical_entity_id (entities-package
	// spelling, e.g. "person:floyd-mayweather") of the resolver's chosen
	// PRIMARY entity — the entity that must drive the primary image and the
	// entity card. Empty when the resolver is not wired or the decision is
	// no-image.
	ImagePrimaryCanonicalID string `json:"image_primary_canonical_id,omitempty"`
	// ImageEntityCanonicalIDs maps each imageable entity's lowercased
	// surface/canonical text to the canonical_entity_id the resolver chose
	// for it. The scene-annotation projection consumes it to stamp the SAME
	// identity onto the annotated entity (join key of the media index).
	ImageEntityCanonicalIDs map[string]string `json:"image_entity_canonical_ids,omitempty"`
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

// SegmentCacheState stores the per-segment cache status across the
// VidRush steps.
type SegmentCacheState struct {
	Extraction      string `json:"extraction,omitempty"`
	Artlist         string `json:"artlist,omitempty"`
	InternetImages  string `json:"internet_images,omitempty"`
	ImageGeneration string `json:"image_generation,omitempty"`
	YouTube         string `json:"youtube,omitempty"`
	Binding         string `json:"binding,omitempty"`

	// InternetImagesProviderSearches is the numeric count of real
	// internet_images provider invocations for this segment. It is 0 when
	// every image query was satisfied by the durable catalog or the segment
	// cache (a warm replay) — the numeric proof behind the
	// "provider search = 0" certification gate.
	InternetImagesProviderSearches int `json:"internet_images_provider_searches,omitempty"`

	// InternetImagesNewUploads is the numeric count of new Drive uploads for
	// internet_images candidates in this segment. It is 0 when the catalog's
	// Drive materialization is reused (no re-download, no re-upload) — the
	// numeric proof behind the "new upload = 0" certification gate.
	InternetImagesNewUploads int `json:"internet_images_new_uploads,omitempty"`
}

// VidRushSegmentResult is the full per-segment output surfaced by
// script generation.
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
