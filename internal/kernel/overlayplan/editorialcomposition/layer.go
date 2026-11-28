package editorialcomposition

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Layer roles. They are semantic: RenderingGen owns the geometry behind each
// role, this contract only fixes the role and the timing relationship.
const (
	LayerBackground = "background"
	LayerImage      = "image"
	LayerPhrase     = "phrase"
	LayerChart      = "chart"
	LayerMap        = "map"
	LayerMapCallout = "map_callout"
	LayerSummary    = "summary"
	LayerChapter    = "chapter"
	LayerDate       = "date"
	LayerMetric     = "metric"
	LayerCaption    = "caption"
)

// Layer z-order bands. They are declared so two boundaries cannot disagree on
// what sits above what: background < media < editorial < chapter.
const (
	ZBackground = -100
	ZMedia      = 0
	ZEditorial  = 10
	ZChapter    = 40
)

// Layers is the lowered document a renderer consumes. It is a pure projection:
// the same composition always lowers to the same bytes.
type Layers struct {
	SchemaVersion string      `json:"schema_version"`
	CompositionID string      `json:"composition_id"`
	SceneID       string      `json:"scene_id"`
	Window        Window      `json:"window"`
	Background    *Background `json:"background,omitempty"`
	Layers        []Layer     `json:"layers"`
}

// Layer is one ordered layer intent.
type Layer struct {
	LayerID    string `json:"layer_id"`
	MemberID   string `json:"member_id,omitempty"`
	Role       string `json:"role"`
	StartUS    int64  `json:"start_us"`
	DurationUS int64  `json:"duration_us"`
	Z          int    `json:"z"`
	MotionID   string `json:"motion_id,omitempty"`
	LinkGroup  string `json:"link_group,omitempty"`
	LinkOrder  int    `json:"link_order,omitempty"`
	Hold       string `json:"hold,omitempty"`
	Persistent bool   `json:"persistent,omitempty"`
	// ParentLayerID is set for an attachment: it names the layer the value
	// belongs to, so a date is never a competing sibling.
	ParentLayerID string `json:"parent_layer_id,omitempty"`
	// AnchorPinID is set for a map callout: the leader line ends on this pin.
	AnchorPinID string         `json:"anchor_pin_id,omitempty"`
	Asset       *AssetRef      `json:"asset,omitempty"`
	Text        string         `json:"text,omitempty"`
	Lane        string         `json:"lane,omitempty"`
	Position    string         `json:"position,omitempty"`
	DateValue   string         `json:"date_value,omitempty"`
	Chapter     *Chapter       `json:"chapter,omitempty"`
	Chart       *ChartMember   `json:"chart,omitempty"`
	Map         *MapMember     `json:"map,omitempty"`
	Summary     *SummaryMember `json:"summary,omitempty"`
}

// Lower validates the composition and projects it onto ordered layer intents.
// A composition that does not validate never lowers.
func (c *Composition) Lower(catalog *Catalog) (*Layers, error) {
	if err := c.Validate(catalog); err != nil {
		return nil, err
	}
	document := &Layers{
		SchemaVersion: LayersSchemaVersionV1,
		CompositionID: c.CompositionID,
		SceneID:       c.SceneID,
		Window:        c.Window,
		Background:    c.Background,
		Layers:        make([]Layer, 0, len(c.Members)+2),
	}
	if err := document.appendBackground(c); err != nil {
		return nil, err
	}
	ordered := append([]Member(nil), c.Members...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left, right := ordered[i].Base(), ordered[j].Base()
		if left.StartUS != right.StartUS {
			return left.StartUS < right.StartUS
		}
		if left.Z != right.Z {
			return left.Z < right.Z
		}
		return left.MemberID < right.MemberID
	})
	for _, member := range ordered {
		if err := document.appendMember(member); err != nil {
			return nil, err
		}
	}
	if c.Chapter != nil && c.Chapter.Mode != ChapterOff {
		document.Layers = append(document.Layers, Layer{
			LayerID:    "chapter:" + c.CompositionID,
			Role:       LayerChapter,
			StartUS:    c.Window.StartUS,
			DurationUS: c.Chapter.DurationMS * 1000,
			Z:          ZChapter,
			Chapter:    c.Chapter,
		})
	}
	sort.SliceStable(document.Layers, func(i, j int) bool {
		left, right := document.Layers[i], document.Layers[j]
		if left.StartUS != right.StartUS {
			return left.StartUS < right.StartUS
		}
		if left.Z != right.Z {
			return left.Z < right.Z
		}
		return left.LayerID < right.LayerID
	})
	return document, nil
}

func (d *Layers) appendBackground(c *Composition) error {
	if c.Background != nil && c.Background.Mode == BackgroundSnapshot {
		asset := &AssetRef{AssetID: c.Background.Snapshot.AssetID, SHA256: c.Background.Snapshot.SHA256}
		d.Layers = append(d.Layers, Layer{
			LayerID:    "background:" + c.CompositionID,
			Role:       LayerBackground,
			StartUS:    c.Window.StartUS,
			DurationUS: c.Window.DurationUS,
			Z:          ZBackground,
			Persistent: true,
			Asset:      asset,
		})
		return nil
	}
	if c.Background != nil && c.Background.Mode == BackgroundInherit {
		d.Layers = append(d.Layers, Layer{
			LayerID:       "background:" + c.CompositionID,
			Role:          LayerBackground,
			StartUS:       c.Window.StartUS,
			DurationUS:    c.Window.DurationUS,
			Z:             ZBackground,
			Persistent:    true,
			ParentLayerID: c.Background.SourceMemberID,
		})
	}
	return nil
}

func (d *Layers) appendMember(member Member) error {
	base := member.Base()
	switch typed := member.(type) {
	case *PhraseMember:
		d.Layers = append(d.Layers, Layer{
			LayerID:    typed.MemberID,
			MemberID:   typed.MemberID,
			Role:       LayerPhrase,
			StartUS:    typed.StartUS,
			DurationUS: typed.DurationUS,
			Z:          ZEditorial + typed.Z,
			MotionID:   typed.MotionID,
			LinkGroup:  linkGroup(typed.Link),
			LinkOrder:  linkOrder(typed.Link),
			Hold:       linkHold(typed.Link),
			Persistent: typed.Persistent,
			Text:       typed.Text,
			Lane:       typed.Lane,
		})
	case *EntityMember:
		layer := Layer{
			LayerID:    typed.MemberID,
			MemberID:   typed.MemberID,
			Role:       LayerImage,
			StartUS:    typed.StartUS,
			DurationUS: typed.DurationUS,
			Z:          ZMedia + typed.Z,
			MotionID:   typed.MotionID,
			LinkGroup:  linkGroup(typed.Link),
			LinkOrder:  linkOrder(typed.Link),
			Hold:       linkHold(typed.Link),
			Persistent: typed.Persistent,
			Text:       typed.Label,
			Asset:      typed.Asset,
		}
		d.Layers = append(d.Layers, layer)
		for index, attachment := range typed.Attachments {
			layerID := fmt.Sprintf("%s#%s", typed.MemberID, attachmentKey(attachment, index))
			d.Layers = append(d.Layers, Layer{
				LayerID:       layerID,
				MemberID:      typed.MemberID,
				Role:          attachment.Role,
				StartUS:       typed.StartUS,
				DurationUS:    typed.DurationUS,
				Z:             ZEditorial + typed.Z,
				MotionID:      attachment.MotionID,
				ParentLayerID: typed.MemberID,
				Position:      attachment.Position,
				Text:          attachment.Value,
				DateValue:     dateValue(attachment),
			})
		}
	case *ChartMember:
		d.Layers = append(d.Layers, Layer{
			LayerID:    typed.MemberID,
			MemberID:   typed.MemberID,
			Role:       LayerChart,
			StartUS:    typed.StartUS,
			DurationUS: typed.DurationUS,
			Z:          ZEditorial + typed.Z,
			MotionID:   typed.MotionID,
			LinkGroup:  linkGroup(typed.Link),
			LinkOrder:  linkOrder(typed.Link),
			Hold:       linkHold(typed.Link),
			Text:       typed.Subject,
			Chart:      typed,
		})
	case *MapMember:
		d.Layers = append(d.Layers, Layer{
			LayerID:    typed.MemberID,
			MemberID:   typed.MemberID,
			Role:       LayerMap,
			StartUS:    typed.StartUS,
			DurationUS: typed.DurationUS,
			Z:          ZMedia + typed.Z,
			MotionID:   typed.MotionID,
			Text:       typed.MapType,
			Map:        typed,
		})
		for _, pin := range typed.Pins {
			if pin.Callout == nil {
				continue
			}
			d.Layers = append(d.Layers, Layer{
				LayerID:     typed.MemberID + "#pin:" + pin.PinID,
				MemberID:    typed.MemberID,
				Role:        LayerMapCallout,
				StartUS:     typed.StartUS,
				DurationUS:  typed.DurationUS,
				Z:           ZEditorial + typed.Z,
				MotionID:    pin.Callout.Choreography,
				AnchorPinID: pin.PinID,
				Asset:       pin.Callout.Asset,
				Position:    pin.Callout.Placement,
			})
		}
	case *SummaryMember:
		d.Layers = append(d.Layers, Layer{
			LayerID:    typed.MemberID,
			MemberID:   typed.MemberID,
			Role:       LayerSummary,
			StartUS:    typed.StartUS,
			DurationUS: typed.DurationUS,
			Z:          ZEditorial + typed.Z,
			MotionID:   typed.MotionID,
			Summary:    typed,
		})
	case *DateMember:
		d.Layers = append(d.Layers, Layer{
			LayerID:    typed.MemberID,
			MemberID:   typed.MemberID,
			Role:       LayerDate,
			StartUS:    typed.StartUS,
			DurationUS: typed.DurationUS,
			Z:          ZEditorial + typed.Z,
			MotionID:   typed.MotionID,
			Position:   typed.Position,
			DateValue:  typed.Value,
		})
	default:
		return &Error{Code: CodeUnknownMemberKind, Path: "members", Detail: base.Kind}
	}
	return nil
}

// MarshalLayers encodes a lowered document exactly as the corpus stores it.
func MarshalLayers(document *Layers) ([]byte, error) {
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

// DecodeLayers strictly decodes a lowered document. It exists so a boundary
// test can prove the golden bytes round-trip without a renderer.
func DecodeLayers(raw []byte) (*Layers, error) {
	var header struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return nil, fmt.Errorf("decode layers header: %w", err)
	}
	if header.SchemaVersion != LayersSchemaVersionV1 {
		return nil, &Error{Code: CodeUnsupportedSchema, Path: "schema_version", Detail: header.SchemaVersion}
	}
	var document Layers
	if err := strictUnmarshal(raw, &document); err != nil {
		return nil, &Error{Code: CodeUnknownField, Path: "layers", Detail: err.Error()}
	}
	return &document, nil
}

func strictUnmarshal(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func attachmentKey(attachment Attachment, index int) string {
	if id := strings.TrimSpace(attachment.AttachmentID); id != "" {
		return id
	}
	return fmt.Sprintf("%s-%d", attachment.Role, index)
}

func dateValue(attachment Attachment) string {
	if attachment.Role == RoleDate {
		return attachment.Value
	}
	return ""
}

func linkGroup(link *Link) string {
	if link == nil {
		return ""
	}
	return link.GroupID
}

func linkOrder(link *Link) int {
	if link == nil {
		return 0
	}
	return link.Order
}

func linkHold(link *Link) string {
	if link == nil {
		return ""
	}
	return link.Hold
}
