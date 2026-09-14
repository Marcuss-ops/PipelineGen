// Package renderinggen — clip_plan_mapper.go
//
// MapClipPlanToOverlayPlan converts a fully-validated ClipRenderPlanV1 into
// the renderinggen.overlay-plan.v1 JSON consumed by the RenderingGen worker.
//
// This is the ONLY place where PipelineGen serialises a clip render job for
// the queue. ClipRenderExecutor.Render() must call this function instead of
// json.Marshal(plan); the worker must never receive a raw ClipRenderPlanV1.
//
// Design notes
//   - The mapper is the sole owner of the schema_version field value.
//   - All semantic decisions (scale, background mode, subtitle mode, audio
//     mode) are already final in the ClipRenderPlanV1; the mapper translates
//     them verbatim into the overlay-plan vocabulary.
//   - The typed visual style blocks (subtitles + watermark) travel with the
//     plan VERBATIM. Dropping them here would let the worker fall back to its
//     own defaults and silently diverge from the requested style.
//   - Asset refs use the content-addressable hash as the logical path so
//     RenderingGen workers can materialise them from the object store
//     regardless of the originating machine's filesystem layout.
//   - duration_ms is derived from the source clip duration carried in the
//     plan's Output contract. When the caller has not set it explicitly the
//     mapper emits 0 and lets the compiler derive it from items; for clip
//     render jobs with items:[] the caller MUST set DurationMS.
package renderinggen

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// SemanticSchema is the overlay-plan contract version owned by RenderingGen.
// Keep it in sync with RenderingGen/renderinggen/internal/overlay/compiler.go.
const SemanticSchema = "renderinggen.overlay-plan.v1"

// overlayPlan is the wire type for the renderinggen.overlay-plan.v1 contract.
// It mirrors the semanticPlan struct in RenderingGen's compiler package so
// changes to either side surface as compile/unmarshal errors immediately.
type overlayPlan struct {
	SchemaVersion   string             `json:"schema_version"`
	PlanID          string             `json:"plan_id"`
	VideoID         string             `json:"video_id"`
	Width           int                `json:"width"`
	Height          int                `json:"height"`
	FPSNum          int                `json:"fps_num"`
	FPSDen          int                `json:"fps_den"`
	DurationMS      int64              `json:"duration_ms,omitempty"`
	OutputProfileID string             `json:"output_profile_id,omitempty"`
	Source          *overlaySource     `json:"source,omitempty"`
	ForegroundScale int                `json:"foreground_scale_percent,omitempty"`
	Background      *overlayBackground `json:"background,omitempty"`
	Subtitles       *overlaySubtitles  `json:"subtitles,omitempty"`
	Watermark       *overlayWatermark  `json:"watermark,omitempty"`
	Audio           *overlayAudio      `json:"audio,omitempty"`
	Items           []json.RawMessage  `json:"items"`
}

type overlaySource struct {
	AssetID string `json:"asset_id"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
}

type overlayBackground struct {
	Kind      string            `json:"kind"`
	AssetRefs []overlayAssetRef `json:"asset_refs,omitempty"`
	Color     []float64         `json:"color,omitempty"`
	Fit       string            `json:"fit,omitempty"`
	Opacity   *float64          `json:"opacity,omitempty"`
	Loop      bool              `json:"loop,omitempty"`
}

type overlaySubtitles struct {
	AssetRefs []overlayAssetRef `json:"asset_refs,omitempty"`
	StyleID   string            `json:"style_id,omitempty"`
	Mode      string            `json:"mode,omitempty"`
	// Style is the caller's typed visual override (font, size, color, shadow,
	// position, width…). It MUST travel with the plan: dropping it here
	// historically caused the RenderingGen compiler to fall back to its own
	// hardcoded defaults, silently diverging from the requested style.
	Style *styleBlock `json:"style,omitempty"`
}

type overlayWatermark struct {
	Text      string            `json:"text,omitempty"`
	AssetRefs []overlayAssetRef `json:"asset_refs,omitempty"`
	FontRef   *overlayAssetRef  `json:"font_ref,omitempty"`
	Position  string            `json:"position,omitempty"`
	Opacity   *float64          `json:"opacity,omitempty"`
	// MarginPX is the requested distance from the canvas edge. It is a *int so
	// an explicit 0 stays distinguishable from an unset value.
	MarginPX *int        `json:"margin_px,omitempty"`
	Style    *styleBlock `json:"style,omitempty"`
}

// styleBlock is the wire projection of the canonical kernel/script
// VideoVisualStyleSpec. It mirrors that struct field-for-field so the typed
// owner in kernel/script remains the single source of truth; the mapper only
// serialises it.
type styleBlock struct {
	Font         string           `json:"font,omitempty"`
	Position     string           `json:"position,omitempty"`
	Size         float64          `json:"size,omitempty"`
	Color        string           `json:"color,omitempty"`
	FontSizePX   float64          `json:"font_size_px,omitempty"`
	WidthPX      int              `json:"width_px,omitempty"`
	HeightPX     int              `json:"height_px,omitempty"`
	ScalePercent float64          `json:"scale_percent,omitempty"`
	Stroke       *strokeBlock     `json:"stroke,omitempty"`
	Shadow       *shadowBlock     `json:"shadow,omitempty"`
	TransitionIn *transitionBlock `json:"transition_in,omitempty"`
}

type shadowBlock struct {
	Color   string  `json:"color,omitempty"`
	Opacity float64 `json:"opacity,omitempty"`
	BlurPX  float64 `json:"blur_px,omitempty"`
	OffsetX float64 `json:"offset_x,omitempty"`
	OffsetY float64 `json:"offset_y,omitempty"`
}

type strokeBlock struct {
	Color string  `json:"color,omitempty"`
	Width float64 `json:"width,omitempty"`
}

type transitionBlock struct {
	Preset     string `json:"preset,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

// marshalStyle projects the canonical kernel/script visual style into the
// wire block. A nil style stays nil on the wire (no empty object emitted).
// derivedKeylineWidth sizes the black contour the mapper derives when a caller
// declares a shadow but no stroke. It scales with the font (about 4.5% of the
// em) instead of the historical fixed 5 px: at caption line widths of ~40
// runes, a 5 px keyline merges the contours of adjacent glyphs into a solid
// black smear, which is what made burned captions unreadable. Explicit
// strokes declared by the caller are never rescaled.
func derivedKeylineWidth(in *scriptpkg.VideoVisualStyleSpec) float64 {
	size := in.FontSizePX
	if size <= 0 {
		size = in.Size
	}
	if size <= 0 {
		size = 54
	}
	w := math.Round(size * 0.045)
	if w < 1.5 {
		w = 1.5
	}
	if w > 3 {
		w = 3
	}
	return w
}

// subtitleLineHeightFactor converts a font size into the line box used for
// burned captions (Chronon centres the text inside this box).
const subtitleLineHeightFactor = 1.25

// subtitleBoxHeightPX derives the burned-caption box from the resolved font
// size and the short-form line budget. The box is an input to the worker's
// placement math (SubtitleStyleAsset), so a one-line box pushed multi-line
// captions out of their safe area and made their keyline look inflated.
func subtitleBoxHeightPX(fontSizePX float64) int {
	if fontSizePX <= 0 {
		fontSizePX = 54
	}
	lines := texttracks.DefaultShortFormPolicy().MaxLines
	return int(math.Ceil(fontSizePX*subtitleLineHeightFactor)) * lines
}

func marshalStyle(in *scriptpkg.VideoVisualStyleSpec) *styleBlock {
	if in == nil {
		return nil
	}
	out := &styleBlock{
		Font:         in.Font,
		Position:     in.Position,
		Size:         in.Size,
		Color:        in.Color,
		FontSizePX:   in.FontSizePX,
		WidthPX:      in.WidthPX,
		HeightPX:     in.HeightPX,
		ScalePercent: in.ScalePercent,
	}
	// An explicit caller stroke wins verbatim (width in render pixels).  The
	// public clip style historically exposed a drop-shadow only; when no
	// stroke is declared, derive one from the shadow color so subtitle text
	// always carries a readable black contour across the semantic-plan
	// worker boundary.
	if in.Stroke != nil && strings.TrimSpace(in.Stroke.Color) != "" {
		out.Stroke = &strokeBlock{Color: in.Stroke.Color, Width: in.Stroke.Width}
	} else if in.Shadow != nil && strings.TrimSpace(in.Shadow.Color) != "" {
		out.Stroke = &strokeBlock{Color: in.Shadow.Color, Width: derivedKeylineWidth(in)}
	}
	if in.Shadow != nil {
		out.Shadow = &shadowBlock{
			Color:   in.Shadow.Color,
			Opacity: in.Shadow.Opacity,
			BlurPX:  in.Shadow.BlurPX,
			OffsetX: in.Shadow.OffsetX,
			OffsetY: in.Shadow.OffsetY,
		}
	}
	if in.TransitionIn != nil {
		out.TransitionIn = &transitionBlock{
			Preset:     in.TransitionIn.Preset,
			DurationMS: in.TransitionIn.DurationMS,
		}
	}
	return out
}

type overlayAudio struct {
	Mode       string `json:"mode,omitempty"`
	Codec      string `json:"codec,omitempty"`
	SampleRate int    `json:"sample_rate,omitempty"`
	Channels   int    `json:"channels,omitempty"`
}

// Semantic contract slots for a rendered video overlay item. They mirror the
// RenderingGen compiler's video-overlay kind/template (registry.go) — the
// single spelling both sides share.
const (
	// SemanticKindVideoOverlay is the semantic kind of a pre-rendered overlay
	// segment composited inside the same Chronon render pass.
	SemanticKindVideoOverlay = "video_overlay"
	// SemanticTemplateVideoOverlay is the registry template_id backing it.
	SemanticTemplateVideoOverlay = "VIDEO_OVERLAY"
)

// overlayItem is the wire projection of one RenderingGen semantic item. The
// field set mirrors semanticItem in RenderingGen's compiler, whose decoder
// rejects unknown fields and requires the non-omitempty keys to be present —
// so every slot is emitted explicitly, even when empty.
type overlayItem struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	TemplateID string `json:"template_id"`
	// PresetID/MotionID are ABSENT when empty: the published overlay-plan.v1
	// contract declares them with minLength 1, so emitting "" would fail
	// schema validation. A preset-less item (the rendered video overlay
	// carries its own pixels) legitimately omits both.
	PresetID     string            `json:"preset_id,omitempty"`
	MotionID     string            `json:"motion_id,omitempty"`
	MotionParams map[string]any    `json:"motion_params"`
	Text         string            `json:"text"`
	StartMS      int64             `json:"start_ms"`
	EndMS        int64             `json:"end_ms"`
	Params       map[string]any    `json:"params"`
	Assets       []overlayAssetRef `json:"asset_refs"`
}

// overlayAssetRef references a content-addressed asset. The LogicalPath is
// the hash-addressed key used by the RenderingGen object store materialiser
// (format: "sha256/<hex>/<filename>"), NOT a local VPS path.
type overlayAssetRef struct {
	AssetID   string `json:"asset_id"`
	SHA256    string `json:"sha256"`
	URL       string `json:"url,omitempty"`
	MediaType string `json:"media_type,omitempty"`
}

// hashAddressedPath returns the logical path used both by the concrete plan
// and by workspace materialization. The bytes remain addressed by SHA-256 in
// the queue/object store; this path is only the Chronon-mounted filename.
func hashAddressedPath(assetID, filename string) string {
	id := strings.TrimSpace(assetID)
	if id == "" {
		id = "asset"
	}
	return "assets/semantic/" + id + "/" + filename
}

// MapClipPlanToOverlayPlan converts a sealed ClipRenderPlanV1 into the
// renderinggen.overlay-plan.v1 JSON wire representation.
//
// The caller must have called plan.Validate() before invoking this function;
// the mapper itself performs a lightweight re-check and returns an error if
// the plan is structurally invalid.
func MapClipPlanToOverlayPlan(plan cliprender.ClipRenderPlanV1) ([]byte, error) {
	if plan.Version != cliprender.PlanVersion {
		return nil, fmt.Errorf("clip plan mapper: unsupported plan version %q", plan.Version)
	}
	if plan.RunID == "" {
		return nil, fmt.Errorf("clip plan mapper: plan has no run_id")
	}
	if plan.Source.AssetID == "" || plan.Source.SHA256 == "" {
		return nil, fmt.Errorf("clip plan mapper: source asset_id and sha256 are required")
	}
	if plan.Output.Width <= 0 || plan.Output.Height <= 0 || plan.Output.FPSNum <= 0 || plan.Output.FPSDen <= 0 {
		return nil, fmt.Errorf("clip plan mapper: output canvas dimensions and fps are required")
	}

	op := overlayPlan{
		SchemaVersion:   SemanticSchema,
		PlanID:          plan.RunID,
		VideoID:         plan.Source.AssetID,
		Width:           plan.Output.Width,
		Height:          plan.Output.Height,
		FPSNum:          plan.Output.FPSNum,
		FPSDen:          plan.Output.FPSDen,
		OutputProfileID: plan.Output.ContractID,
		Source: &overlaySource{
			AssetID: plan.Source.AssetID,
			Path:    hashAddressedPath(plan.Source.AssetID, "source.mp4"),
			SHA256:  plan.Source.SHA256,
		},
		// items is always emitted as an explicit empty array, never null,
		// so the RenderingGen schema validator sees a valid JSON array.
		Items: []json.RawMessage{},
	}

	// Foreground scale: 0 and 100 both mean "full canvas" in ClipRenderPlanV1;
	// only emit the field when it is a real scale-down.
	if plan.Output.ForegroundScalePercent > 0 && plan.Output.ForegroundScalePercent < 100 {
		op.ForegroundScale = plan.Output.ForegroundScalePercent
	}

	if plan.DurationMS <= 0 {
		return nil, fmt.Errorf("clip plan mapper: duration_ms must be positive")
	}
	op.DurationMS = plan.DurationMS

	// Background (its own file: the block IS a renderer-shape decision).
	background, err := mapBackground(plan)
	if err != nil {
		return nil, err
	}
	op.Background = background

	// Subtitles
	if plan.Subtitles != nil {
		subStyle := marshalStyle(plan.Subtitles.Style)
		if subStyle == nil {
			subStyle = &styleBlock{}
		}
		if subStyle.Color == "" {
			subStyle.Color = "#FFFFFF"
		}
		if subStyle.FontSizePX <= 0 {
			if subStyle.Size > 0 {
				subStyle.FontSizePX = subStyle.Size
			} else {
				subStyle.FontSizePX = 54
			}
		}
		if subStyle.Position == "" {
			subStyle.Position = "bottom_center"
		}
		if subStyle.HeightPX <= 0 {
			subStyle.HeightPX = subtitleBoxHeightPX(subStyle.FontSizePX)
		}
		op.Subtitles = &overlaySubtitles{
			Mode:    plan.Subtitles.Mode,
			StyleID: plan.Subtitles.StyleID,
			// The typed style block MUST be carried verbatim: the worker-side
			// compiler has no other owner for subtitle geometry/color/shadow.
			Style: subStyle,
			AssetRefs: []overlayAssetRef{{
				AssetID:   plan.Subtitles.SHA256, // use hash as stable ID
				SHA256:    plan.Subtitles.SHA256,
				URL:       hashAddressedPath(plan.Subtitles.SHA256, "subtitles.ass"),
				MediaType: "text/x-ass",
			}},
		}
	}

	// Watermark
	if plan.Watermark != nil {
		wmStyle := marshalStyle(plan.Watermark.Style)
		if wmStyle == nil {
			wmStyle = &styleBlock{}
		}
		if wmStyle.Color == "" {
			wmStyle.Color = "#FFFFFF"
		}
		if wmStyle.FontSizePX <= 0 {
			if wmStyle.Size > 0 {
				wmStyle.FontSizePX = wmStyle.Size
			} else {
				wmStyle.FontSizePX = 42
			}
		}
		wm := &overlayWatermark{
			Text:     plan.Watermark.Text,
			Position: plan.Watermark.Position,
			Style:    wmStyle,
		}
		// Single owner: Normalize owns watermark defaults (40/100 margin,
		// 1.0 opacity). Mapper emits verbatim without re-defaulting so
		// 0 remains representable and drift is impossible.
		margin := plan.Watermark.MarginPX
		wm.MarginPX = &margin
		opacity := plan.Watermark.Opacity
		wm.Opacity = &opacity
		if plan.Watermark.SHA256 != "" {
			wm.AssetRefs = []overlayAssetRef{{
				AssetID:   plan.Watermark.AssetID,
				SHA256:    plan.Watermark.SHA256,
				URL:       hashAddressedPath(plan.Watermark.SHA256, "watermark.png"),
				MediaType: "image/png",
			}}
		}
		if wm.Text != "" && len(wm.AssetRefs) == 0 {
			font, err := watermarkFontAssetForStyle(plan.Watermark.Style)
			if err != nil {
				return nil, fmt.Errorf("clip plan mapper: watermark font: %w", err)
			}
			wm.FontRef = &overlayAssetRef{AssetID: fontAssetID(plan.Watermark.Style), SHA256: font.Hash, URL: font.LogicalPath, MediaType: "font/ttf"}
		}
		op.Watermark = wm
	}

	// Audio
	op.Audio = &overlayAudio{
		Mode:       plan.Audio.Mode,
		Codec:      plan.Audio.Codec,
		SampleRate: plan.Audio.SampleRate,
		Channels:   plan.Audio.Channels,
	}

	// Entity overlays — one pre-rendered segment PER certified overlay.render
	// artifact, each composited INSIDE the same Chronon render pass. Emitting
	// every segment as its own semantic item is what removes the second full
	// transcode: the clip is encoded once, with each overlay timed on the
	// timeline (Chronon samples a segment at frame - layer_start). Dropping all
	// but one segment here is what used to silently lose a scene's second and
	// third overlay.
	if plan.Overlay != nil {
		for index, segment := range plan.Overlay.Segments {
			assetID := overlaySegmentAssetID(&segment)
			item := overlayItem{
				ID:           overlaySegmentItemID(assetID, index),
				Kind:         SemanticKindVideoOverlay,
				TemplateID:   SemanticTemplateVideoOverlay,
				MotionParams: map[string]any{},
				Params:       map[string]any{"fit": "cover"},
				StartMS:      segment.StartMS,
				EndMS:        segment.EndMS,
				Assets: []overlayAssetRef{{
					AssetID:   assetID,
					SHA256:    segment.SHA256,
					URL:       hashAddressedPath(assetID, "overlay.mp4"),
					MediaType: "video/mp4",
				}},
			}
			rawItem, err := json.Marshal(item)
			if err != nil {
				return nil, fmt.Errorf("clip plan mapper: marshal overlay item: %w", err)
			}
			op.Items = append(op.Items, json.RawMessage(rawItem))
		}
	}

	raw, err := json.Marshal(op)
	if err != nil {
		return nil, fmt.Errorf("clip plan mapper: marshal overlay plan: %w", err)
	}
	return raw, nil
}

func watermarkFontAsset() (assetRef, error) {
	return ResolveFontAsset(FontMontserratBold)
}

func fontAssetID(style *scriptpkg.VideoVisualStyleSpec) string {
	if style != nil && strings.Contains(strings.ToLower(strings.TrimSpace(style.Font)), "poppins") {
		return FontPoppinsBold
	}
	return FontMontserratBold
}

func watermarkFontAssetForStyle(style *scriptpkg.VideoVisualStyleSpec) (assetRef, error) {
	return ResolveFontAsset(fontAssetID(style))
}

func poppinsFontAsset() (assetRef, error) {
	return ResolveFontAsset(FontPoppinsBold)
}
