package script

// ScriptSegment is the canonical per-block payload entry
// (PR-CS-1, July 2026). Each script is decomposed into an ordered
// list of segments with optional per-segment source_text. DoD-driven
// contract (godlike/06 SSOT):
//   - Topic is required at runtime (validator enforces non-empty).
//   - source_text present → rewrite / improve that text only.
//   - source_text absent → write the segment from Topic + global source.
//   - target_words > 0 → use it; else fall back to
//     ScriptSpec.SegmentWords, else to ScriptSpec.TargetWords,
//     else default 80.
//
// The runtime mutex with SegmentTopics is enforced at the validator
// layer (DoD #8). ScriptSpec.Segments is the SOLE canonical owner;
// SourceSpec and Item layers consume via generator-normalizer copies.
type ScriptSegment struct {
	ID          string `json:"id,omitempty"`
	Topic       string `json:"topic"`
	SourceText  string `json:"source_text,omitempty"`
	TargetWords int    `json:"target_words,omitempty"`
	// MinWords and MaxWords are optional explicit QA bounds. When omitted,
	// the segment validator derives them from TargetWords and its configured
	// tolerance.
	MinWords int `json:"min_words,omitempty"`
	MaxWords int `json:"max_words,omitempty"`
}

// ScriptSpec controls the generation behaviour: sizing, style, and
// prompt versioning. Identity fields (Language, Tone, Model) live
// on GenerationItemV2; the normalizer merges them into the resolved
// plan.
//
// PR-CS-1 (July 2026): Segments is the per-block payload. When
// present:
//   - it is MUTUALLY EXCLUSIVE with SegmentTopics at runtime
//     (validator surfaces ErrSegmentsAndTopicTopicsBothSet on conflict).
//   - each segment MUST have a non-empty Topic (validator enforces).
//   - the engine prompt renders one block per segment in order.
//
// SegmentTopics remains the legacy alias — used when caller omits
// Segments.
type ScriptSpec struct {
	TargetWords    int    `json:"target_words,omitempty"`
	VoiceoverGroup string `json:"voiceover_group,omitempty"`
	// SingleScene requests one consolidated SpecScene in the generated
	// output. It is useful for short single-segment documents where the
	// narrative must remain one continuous scene.
	SingleScene         bool            `json:"single_scene,omitempty"`
	Duration            int             `json:"duration,omitempty"`
	MinWords            int             `json:"min_words,omitempty"`
	SegmentWords        int             `json:"segment_words,omitempty"`
	SegmentTopics       []string        `json:"segment_topics,omitempty"`
	Segments            []ScriptSegment `json:"segments,omitempty"`
	SentencesPerImage   int             `json:"sentences_per_image,omitempty"`
	ImagesPerScene      int             `json:"images_per_scene,omitempty"`
	Style               string          `json:"style,omitempty"`
	Guidelines          string          `json:"guidelines,omitempty"`
	TranscriptPolicy    string          `json:"transcript_policy,omitempty"`
	OrderingStrategy    string          `json:"ordering_strategy,omitempty"`
	PromptVersion       string          `json:"prompt_version,omitempty"`
	EditorPromptVersion string          `json:"editor_prompt_version,omitempty"`
	QAPromptVersion     string          `json:"qa_prompt_version,omitempty"`
	// PlannerVersion is the scene-planning algorithm version. It is
	// part of the generation fingerprint so changes to the planner
	// invalidate cached results.
	PlannerVersion string `json:"planner_version,omitempty"`
	ForceRefresh   bool   `json:"force_refresh,omitempty"`
	UseMemory      bool   `json:"use_memory,omitempty"`
	// SkipQualityGate keeps the editorial quality metrics but prevents a
	// request-scoped gate failure from blocking persistence. It is
	// intentionally opt-in and never changes the default gate behavior.
	SkipQualityGate bool `json:"skip_quality_gate,omitempty"`
}
