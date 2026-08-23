package script

// SourceResolutionContext carries item-level traits needed while resolving a
// SourceSpec. Source-side instructions stay on SourceSpec; operator-side traits
// stay here.
type SourceResolutionContext struct {
	ItemID   string `json:"item_id,omitempty"`
	Title    string `json:"title,omitempty"`
	Language string `json:"language,omitempty"`
	Tone     string `json:"tone,omitempty"`
	Model    string `json:"model,omitempty"`
	Style    string `json:"style,omitempty"`

	TargetWords   int             `json:"target_words,omitempty"`
	NumClips      int             `json:"num_clips,omitempty"`
	SegmentWords  int             `json:"segment_words,omitempty"`
	Segments      []ScriptSegment `json:"segments,omitempty"`

	RequireDriveLink bool `json:"-"`
	// RequireLocalMedia is true only for execution paths that will render
	// media bytes. Document-only generation may resolve Drive-backed clips
	// without staging a local copy.
	RequireLocalMedia bool `json:"-"`
}

// ResolvedSource is the source-agnostic result consumed by the generation
// engine.
type ResolvedSource struct {
	Type       SourceType `json:"type"`
	Topic      string     `json:"topic"`
	Title      string     `json:"title"`
	SourceText string     `json:"source_text"`
	// Segments is the resolver-owned canonical segment list.
	Segments         []ScriptSegment       `json:"segments,omitempty"`
	Language         string                `json:"language,omitempty"`
	ClipEvidence     *ClipEvidence         `json:"clip_evidence,omitempty"`
	SearchResults    []SearchResultItem    `json:"search_results,omitempty"`
	GroundingPolicy  string                `json:"grounding_policy,omitempty"`
	Fingerprint      string                `json:"fingerprint,omitempty"`
	ResearchReport   *ResearchReport       `json:"research_report,omitempty"`
	ResearchEvidence *ResearchEvidencePack `json:"research_evidence,omitempty"`
}
