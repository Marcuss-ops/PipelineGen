// Package editorialcomposition owns the contract-only description of an
// editorial composition: the typed envelope that lets several overlays behave
// as one timed composition (persistent background, linked phrases, progressive
// summary, chapter opening, attached date, typed chart and map photo callout).
//
// It is deliberately not wired into the runtime. No producer builds a
// composition yet and no renderer admits one; the package exists so the three
// boundaries (PipelineGen, BrainRust, RenderingGen) share one schema, one
// capability catalog and one fail-closed verdict before any of it is adopted.
//
// The envelope is `editorial_composition.v1`. Every member is discriminated by
// `kind` and decoded strictly: an unknown field is an error, never a field the
// next boundary silently drops.
package editorialcomposition

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	// SchemaVersionV1 is the composition envelope version.
	SchemaVersionV1 = "editorial_composition.v1"
	// LayersSchemaVersionV1 is the lowered layer document version.
	LayersSchemaVersionV1 = "editorial_layers.v1"
	// CapabilitiesSchemaVersion is the capability catalog version.
	CapabilitiesSchemaVersion = "motion_capabilities.v1"
)

// Member kinds.
const (
	KindPhrase  = "phrase"
	KindEntity  = "entity"
	KindChart   = "chart"
	KindMap     = "map"
	KindSummary = "summary"
	KindDate    = "date"
)

// Composition is one scene's editorial composition. The window is the
// certified plan extent the members must stay inside.
type Composition struct {
	SchemaVersion string      `json:"schema_version"`
	CompositionID string      `json:"composition_id"`
	SceneID       string      `json:"scene_id"`
	Window        Window      `json:"window"`
	Background    *Background `json:"background,omitempty"`
	Chapter       *Chapter    `json:"chapter,omitempty"`
	Members       []Member    `json:"members"`
}

// Window is a half-open interval on the canonical timeline, in microseconds.
type Window struct {
	StartUS    int64 `json:"start_us"`
	DurationUS int64 `json:"duration_us"`
}

// EndUS returns the exclusive end of the window.
func (w Window) EndUS() int64 { return w.StartUS + w.DurationUS }

// Background modes. Inheriting the previous visual state keeps one persistent
// photograph while new editorial content animates above it; the snapshot mode
// is the content-addressed fallback for independent artifacts.
const (
	BackgroundIndependent = "independent"
	BackgroundInherit     = "inherit_visual_state"
	BackgroundSnapshot    = "previous_snapshot"
)

// Background is the composition's background policy.
type Background struct {
	Mode string `json:"mode"`
	// SourceMemberID is the member whose persistent visual state is inherited.
	// Required by inherit_visual_state, forbidden otherwise.
	SourceMemberID string       `json:"source_member_id,omitempty"`
	Snapshot       *SnapshotRef `json:"snapshot,omitempty"`
	// Persist marks the inherited state as surviving the whole composition.
	Persist bool `json:"persist,omitempty"`
}

// SnapshotRef is a content-addressed last-frame snapshot. The digest is the
// identity: a snapshot without sha256 is not admissible.
type SnapshotRef struct {
	AssetID string `json:"asset_id"`
	SHA256  string `json:"sha256"`
}

// Chapter modes.
const (
	ChapterOff         = "off"
	ChapterAuto        = "auto"
	ChapterExplicit    = "explicit"
	ChapterChapterOnly = "chapter_only"
)

// Chapter grounding sources. An authored title is editorial authorship, not a
// transcript extraction, so it is exempt from verbatim grounding.
const (
	GroundingTranscript = "transcript"
	GroundingAuthored   = "authored"
)

// Chapter style families.
const (
	StyleDocumentary      = "documentary"
	StyleEditorialMinimal = "editorial_minimal"
)

// Chapter is the scene-opening chapter policy.
type Chapter struct {
	Mode        string         `json:"mode"`
	StyleFamily string         `json:"style_family,omitempty"`
	DurationMS  int64          `json:"duration_ms,omitempty"`
	Grounding   string         `json:"grounding,omitempty"`
	Titles      []ChapterTitle `json:"titles,omitempty"`
}

// ChapterTitle is one explicitly authored scene title.
type ChapterTitle struct {
	SceneID       string `json:"scene_id"`
	Title         string `json:"title"`
	ChapterNumber int    `json:"chapter_number"`
}

// Member is one timed element of the composition.
type Member interface {
	// Base returns the shared identity and window of the member.
	Base() *MemberBase
}

// MemberBase is the shared part of every member. It is embedded, so the wire
// shape keeps these fields flat next to the kind-specific ones.
type MemberBase struct {
	MemberID   string `json:"member_id"`
	Kind       string `json:"kind"`
	StartUS    int64  `json:"start_us"`
	DurationUS int64  `json:"duration_us"`
	Z          int    `json:"z,omitempty"`
	MotionID   string `json:"motion_id,omitempty"`
	// Link joins the member to a shared-canvas choreography. Presence of the
	// link, not the motion id prefix, is what makes the member a chain member.
	Link *Link `json:"link,omitempty"`
	// Persistent keeps the member's visual state available to a later
	// background inheritance in the same composition.
	Persistent bool `json:"persistent,omitempty"`
}

// Base implements Member.
func (m *MemberBase) Base() *MemberBase { return m }

// Link is the shared-choreography membership of one member.
type Link struct {
	GroupID string `json:"group_id"`
	Order   int    `json:"order"`
	// Hold is "handoff" (the previous member leaves) or "persist" (it stays).
	Hold string `json:"hold"`
}

// Link holds.
const (
	HoldHandoff = "handoff"
	HoldPersist = "persist"
)

// Phrase lanes.
const (
	LaneShort = "short"
	LaneLong  = "long"
)

// PhraseMember is one important phrase. A short and a long phrase can join the
// same link group, which is the linked-animation feature.
type PhraseMember struct {
	MemberBase
	Text string `json:"text"`
	Lane string `json:"lane"`
}

// EntityMember is one entity card or image, optionally carrying attachments.
type EntityMember struct {
	MemberBase
	EntityID    string       `json:"entity_id"`
	Label       string       `json:"label,omitempty"`
	Asset       *AssetRef    `json:"asset,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

// AssetRef is a verified media identity.
type AssetRef struct {
	AssetID   string `json:"asset_id"`
	SHA256    string `json:"sha256,omitempty"`
	MediaType string `json:"media_type,omitempty"`
}

// Attachment roles.
const (
	RoleDate    = "date"
	RoleMetric  = "metric"
	RoleCaption = "caption"
)

// Attachment positions. "auto" lets the renderer pick the allowed slot for the
// parent layout; every other value is a request that must be admissible.
const (
	PositionAuto  = "auto"
	PositionBelow = "below"
	PositionAbove = "above"
	PositionBadge = "badge"
	PositionSide  = "side"
)

// Attachment is a typed child of an entity member: a date, a metric or a
// caption that belongs to this parent instead of competing for its own window.
type Attachment struct {
	AttachmentID string `json:"attachment_id,omitempty"`
	Role         string `json:"role"`
	Value        string `json:"value"`
	Unit         string `json:"unit,omitempty"`
	Position     string `json:"position,omitempty"`
	MotionID     string `json:"motion_id,omitempty"`
	// Anchor names the sub-element the attachment hangs from inside the parent
	// (for example "caption.line2").
	Anchor   string    `json:"anchor,omitempty"`
	Evidence *Evidence `json:"evidence,omitempty"`
}

// Evidence is the grounded source span of an attached value.
type Evidence struct {
	Text       string `json:"text"`
	StartUS    int64  `json:"start_us,omitempty"`
	DurationUS int64  `json:"duration_us,omitempty"`
}

// Chart layouts certified by the contract.
var certifiedChartLayouts = map[string]struct{}{
	"animated_bar_comparison": {},
	"bar_comparison":          {},
	"animated_line":           {},
	"timeline_growth":         {},
	"qualified_growth":        {},
	"ranking_bar":             {},
	"before_after_metric":     {},
	"segmented_total_bar":     {},
	"radial_percentage":       {},
}

// ChartMember is a typed chart: one subject, one metric, one unit and points
// that keep the value/period/unit binding instead of a flat list of strings.
type ChartMember struct {
	MemberBase
	Subject string        `json:"subject"`
	Metric  string        `json:"metric"`
	Unit    string        `json:"unit"`
	Layout  string        `json:"layout"`
	Points  []ChartPoint  `json:"points"`
	Series  []ChartSeries `json:"series,omitempty"`
}

// ChartPoint is one typed data point. Value is a pointer so a missing value is
// distinguishable from a legitimate zero.
type ChartPoint struct {
	Period string `json:"period"`
	Value  *int64 `json:"value"`
	// Unit overrides the chart unit for this point; when set it must agree
	// with the chart unit.
	Unit     string    `json:"unit,omitempty"`
	SceneID  string    `json:"scene_id,omitempty"`
	Evidence *Evidence `json:"evidence,omitempty"`
}

// ChartSeries is one named series of a multi-series chart.
type ChartSeries struct {
	SeriesID string       `json:"series_id"`
	Label    string       `json:"label,omitempty"`
	Points   []ChartPoint `json:"points"`
}

// MapMember is a map composition. It owns the full georeferenced canvas, so it
// never joins a chain.
type MapMember struct {
	MemberBase
	MapType string   `json:"map_type,omitempty"`
	Pins    []MapPin `json:"pins"`
}

// MapPin is one geographic anchor, optionally carrying a photo callout.
type MapPin struct {
	PinID     string      `json:"pin_id"`
	Label     string      `json:"label,omitempty"`
	Latitude  float64     `json:"latitude"`
	Longitude float64     `json:"longitude"`
	LOD       string      `json:"lod,omitempty"`
	Callout   *MapCallout `json:"callout,omitempty"`
}

// Map callout kinds and connectors.
const (
	CalloutPhoto    = "photo"
	ConnectorLeader = "leader_line"
	ConnectorNone   = "none"
)

// MapCallout is a photo card anchored to its pin.
type MapCallout struct {
	Kind         string    `json:"kind"`
	Asset        *AssetRef `json:"asset,omitempty"`
	Connector    string    `json:"connector,omitempty"`
	Placement    string    `json:"placement,omitempty"`
	Choreography string    `json:"choreography,omitempty"`
	Evidence     *Evidence `json:"evidence,omitempty"`
}

// Summary reveals.
const (
	RevealProgressive = "progressive"
	RevealReplace     = "replace"
)

// SummaryMember is a local micro summary of consecutive, semantically related
// phrases. Progressive reveal keeps every point visible once it entered.
type SummaryMember struct {
	MemberBase
	Reveal string         `json:"reveal"`
	Points []SummaryPoint `json:"points"`
}

// SummaryPoint is one line of the micro summary.
type SummaryPoint struct {
	Text       string `json:"text"`
	StartUS    int64  `json:"start_us"`
	DurationUS int64  `json:"duration_us"`
	MotionID   string `json:"motion_id,omitempty"`
}

// DateMember is a standalone date card. It is admitted only when the
// composition has no date attachment on an entity, so a date is never shown
// twice.
type DateMember struct {
	MemberBase
	Value    string `json:"value"`
	Position string `json:"position,omitempty"`
}

// Decode strictly decodes one composition document: unknown fields and unknown
// member kinds are errors, and the schema version is checked before any
// semantic validation runs.
func Decode(raw []byte) (*Composition, error) {
	var header struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return nil, fmt.Errorf("decode composition header: %w", err)
	}
	if header.SchemaVersion != SchemaVersionV1 {
		return nil, &Error{Code: CodeUnsupportedSchema, Path: "schema_version", Detail: header.SchemaVersion}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var wire struct {
		SchemaVersion string            `json:"schema_version"`
		CompositionID string            `json:"composition_id"`
		SceneID       string            `json:"scene_id"`
		Window        Window            `json:"window"`
		Background    *Background       `json:"background,omitempty"`
		Chapter       *Chapter          `json:"chapter,omitempty"`
		Members       []json.RawMessage `json:"members"`
	}
	if err := decoder.Decode(&wire); err != nil {
		return nil, &Error{Code: CodeUnknownField, Path: "composition", Detail: err.Error()}
	}
	composition := &Composition{
		SchemaVersion: wire.SchemaVersion,
		CompositionID: wire.CompositionID,
		SceneID:       wire.SceneID,
		Window:        wire.Window,
		Background:    wire.Background,
		Chapter:       wire.Chapter,
		Members:       make([]Member, 0, len(wire.Members)),
	}
	for index, memberRaw := range wire.Members {
		member, err := decodeMember(memberRaw)
		if err != nil {
			return nil, fmt.Errorf("members[%d]: %w", index, err)
		}
		composition.Members = append(composition.Members, member)
	}
	return composition, nil
}

func decodeMember(raw json.RawMessage) (Member, error) {
	var header struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return nil, &Error{Code: CodeUnknownMemberKind, Path: "kind", Detail: err.Error()}
	}
	var target Member
	switch strings.TrimSpace(header.Kind) {
	case KindPhrase:
		target = &PhraseMember{}
	case KindEntity:
		target = &EntityMember{}
	case KindChart:
		target = &ChartMember{}
	case KindMap:
		target = &MapMember{}
	case KindSummary:
		target = &SummaryMember{}
	case KindDate:
		target = &DateMember{}
	default:
		return nil, &Error{Code: CodeUnknownMemberKind, Path: "kind", Detail: header.Kind}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return nil, &Error{Code: CodeUnknownField, Path: "member", Detail: err.Error()}
	}
	return target, nil
}

// MemberByID returns the member with the given id.
func (c *Composition) MemberByID(id string) (Member, bool) {
	for _, member := range c.Members {
		if member.Base().MemberID == id {
			return member, true
		}
	}
	return nil, false
}

// MembersOfKind returns every member of the given kind in wire order.
func (c *Composition) MembersOfKind(kind string) []Member {
	out := make([]Member, 0, len(c.Members))
	for _, member := range c.Members {
		if member.Base().Kind == kind {
			out = append(out, member)
		}
	}
	return out
}
