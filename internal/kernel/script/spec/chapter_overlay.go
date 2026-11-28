package spec

// Chapter overlay payload directive (`chapter_overlay`).
//
// It is the producer side of the `editorial_composition.v1` chapter contract:
// the same modes, grounds, style families and authored titles, carried on the
// generation envelope so a run can open its scenes with an explicit chapter or
// keep the automatic one. The contract rules are enforced by
// `editorialcomposition.ValidateChapterPolicy` at ingress, so the field has no
// second validator here.
//
// A title with `grounding=authored` is editorial authorship, not a transcript
// quote: it is exempt from verbatim grounding, and the worker stamps the
// manifest entry with `title_source=authored` so no consumer mistakes it for an
// extraction.
type ChapterOverlaySpec struct {
	// Mode is off, auto, explicit or chapter_only.
	Mode string `json:"mode"`
	// StyleFamily is documentary or editorial_minimal; empty keeps the
	// documentary default.
	StyleFamily string `json:"style_family,omitempty"`
	// DurationMS is the fixed readable beat of the chapter card.
	DurationMS int64 `json:"duration_ms,omitempty"`
	// Grounding is transcript (auto) or authored (explicit/chapter_only).
	Grounding string `json:"grounding,omitempty"`
	// Titles are the authored chapter titles, one per opening scene.
	Titles []ChapterOverlayTitle `json:"titles,omitempty"`
}

// ChapterOverlayTitle anchors one authored title to the scene that opens the
// chapter. ChapterNumber is the editorial order and must be unique.
type ChapterOverlayTitle struct {
	SceneID       string `json:"scene_id"`
	Title         string `json:"title"`
	ChapterNumber int    `json:"chapter_number"`
}

// Chapter overlay modes and grounds, mirroring `editorial_composition.v1`.
const (
	ChapterOverlayOff         = "off"
	ChapterOverlayAuto        = "auto"
	ChapterOverlayExplicit    = "explicit"
	ChapterOverlayChapterOnly = "chapter_only"

	ChapterGroundingTranscript = "transcript"
	ChapterGroundingAuthored   = "authored"

	ChapterStyleDocumentary      = "documentary"
	ChapterStyleEditorialMinimal = "editorial_minimal"
)

// ChaptersEnabled reports whether the overlay asks for a chapter layer at all.
// Nil means the caller declared no chapter policy, which keeps the historical
// automatic segmentation.
func (c *ChapterOverlaySpec) ChaptersEnabled() bool {
	return c != nil && c.Mode != ChapterOverlayOff
}

// AutoChaptersEnabled reports whether the automatic (transcript-grounded)
// chapter extraction runs for this request.
func (c *ChapterOverlaySpec) AutoChaptersEnabled() bool {
	return c == nil || c.Mode == ChapterOverlayAuto
}
