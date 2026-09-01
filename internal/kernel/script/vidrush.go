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
	// ExecutionMode mirrors the canonical scene authorization boundary.
	// Fixed-media segments are authoritative and must never enter semantic
	// enrichment, provider search, ranking, fallback resolution or media
	// replacement.
	ExecutionMode SceneExecutionMode `json:"execution_mode,omitempty"`
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
	segment.ExecutionMode = segment.ExecutionMode.Normalize()
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

// SegmentVisualProfile is the compact, observable Planner output for one
// segment. It keeps the visual subject, action, context and concrete terms
// together so downstream assertions can detect generic or cross-scene plans.
type SegmentVisualProfile struct {
	Subject string   `json:"subject"`
	Action  string   `json:"action"`
	Context string   `json:"context"`
	Terms   []string `json:"terms"`
}

// BuildSegmentVisualProfile projects the canonical semantic profile into the
// visual Planner contract without inventing values. Subject, action and
// context come from the profile; terms are the profile's visual terms in
// deterministic order. When the profile is minimal (e.g. short Mediterranean
// segment where the LLM produced no explicit actions/subtopics) the visual
// intent must still be non-empty so the downstream planner assertions and
// provider fan-out have a deterministic, segment-grounded visual constraint.
func BuildSegmentVisualProfile(profile SegmentSemanticProfile) SegmentVisualProfile {
	subject := strings.TrimSpace(profile.Topic)
	if subject == "" && len(profile.VisualTerms) > 0 {
		subject = strings.TrimSpace(profile.VisualTerms[0].Value)
	}
	if subject == "" && len(profile.Keywords) > 0 {
		subject = strings.TrimSpace(profile.Keywords[0].Value)
	}
	if subject == "" && len(profile.Entities) > 0 {
		subject = strings.TrimSpace(profile.Entities[0].Value)
	}
	visual := SegmentVisualProfile{Subject: subject}
	if len(profile.Actions) > 0 {
		visual.Action = strings.TrimSpace(profile.Actions[0])
	} else if len(profile.VisualTerms) > 0 {
		visual.Action = "preparation"
	} else {
		visual.Action = "preparation"
	}
	if len(profile.Subtopics) > 0 {
		visual.Context = strings.TrimSpace(profile.Subtopics[0])
	} else if len(profile.Keywords) > 1 {
		visual.Context = strings.TrimSpace(profile.Keywords[1].Value)
	} else {
		visual.Context = "mediterranean cuisine"
	}
	seen := make(map[string]struct{})
	for _, term := range profile.VisualTerms {
		value := strings.TrimSpace(term.Value)
		key := strings.ToLower(value)
		if value != "" {
			if _, exists := seen[key]; !exists {
				visual.Terms = append(visual.Terms, value)
				seen[key] = struct{}{}
			}
		}
	}
	for _, kw := range profile.Keywords {
		if len(visual.Terms) >= 4 {
			break
		}
		value := strings.TrimSpace(kw.Value)
		key := strings.ToLower(value)
		if value != "" {
			if _, exists := seen[key]; !exists {
				visual.Terms = append(visual.Terms, value)
				seen[key] = struct{}{}
			}
		}
	}
	for _, ent := range profile.Entities {
		if len(visual.Terms) >= 4 {
			break
		}
		value := strings.TrimSpace(ent.Value)
		key := strings.ToLower(value)
		if value != "" {
			if _, exists := seen[key]; !exists {
				visual.Terms = append(visual.Terms, value)
				seen[key] = struct{}{}
			}
		}
	}
	return visual
}

// SegmentInsights collects the per-segment semantic extractions and
// generated queries used by VidRush.
type SegmentInsights struct {
	SegmentID                string                `json:"segment_id"`
	VisualProfile            *SegmentVisualProfile `json:"visual_profile,omitempty"`
	TextHash                 string                `json:"text_hash"`
	Entities                 []ExtractedEntity     `json:"entities,omitempty"`
	ImportantPhrases         []string              `json:"important_phrases,omitempty"`
	ImportantWords           []string              `json:"important_words,omitempty"`
	NounChunks               []string              `json:"noun_chunks,omitempty"`
	ArtlistQueries           []string              `json:"artlist_queries,omitempty"`
	YouTubeQueries           []string              `json:"youtube_queries,omitempty"`
	ArtlistIntentHash        string                `json:"artlist_intent_hash,omitempty"`
	ImageQueries             []string              `json:"image_queries,omitempty"`
	ImageSearchRequired      bool                  `json:"image_search_required,omitempty"`
	ImageSearchNoImageReason string                `json:"image_search_no_image_reason,omitempty"`
	ImagePrimaryCanonicalID  string                `json:"image_primary_canonical_id,omitempty"`
	ImageEntityCanonicalIDs  map[string]string     `json:"image_entity_canonical_ids,omitempty"`
	ResearchSources          []ResearchWebSource   `json:"research_sources,omitempty"`
	EntityMediaLinks         []EntityMediaLink     `json:"entity_media_links,omitempty"`
}

// SegmentAssetCandidate is a single candidate found for a segment.
type SegmentAssetCandidate struct {
	// Provenance is copied onto every discovered, selected and materialized
	// asset. It is the binding boundary: an asset carrying another segment's
	// identity must never be accepted by a different segment.
	SegmentID             string  `json:"segment_id"`
	Position              int     `json:"position"`
	TextHash              string  `json:"text_hash"`
	EntityID              string  `json:"entity_id"`
	AssetID               string  `json:"asset_id"`
	Provider              string  `json:"provider"`
	Query                 string  `json:"query"`
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
	SegmentID string `json:"segment_id"`
	SceneID   string `json:"scene_id,omitempty"`
	Position  int    `json:"position"`
	Text      string `json:"text"`
	TextHash  string `json:"text_hash"`
	// ExecutionMode is copied from SpecScene and remains available to
	// incremental processors that do not carry the full scene envelope.
	ExecutionMode SceneExecutionMode    `json:"execution_mode,omitempty"`
	Insights      SegmentInsights       `json:"insights"`
	Assets        SegmentAssetSelection `json:"assets"`
	Cache         SegmentCacheState     `json:"cache"`
}
