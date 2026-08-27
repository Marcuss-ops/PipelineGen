package wiring

// chronon_plan_projector.go owns the SINGLE canonical projection of the
// sealed ClipRenderPlanV1 onto the Chronon render-plan v1 wire shape.
// Background, Watermark (text/image), Subtitles, Style and Transition are
// lowered here — never rebuilt as ad-hoc map[string]any layer literals in
// the executor. The typed plan marshals to the exact JSON the Chronon
// render-plan decoder consumes.
//
// The projector is pure: it reads only the sealed plan + the probed
// duration. Asset acquisition (linking), probing and invocation stay in the
// executor; this type owns the layer graph and nothing else.
//
// Byte-stability: struct fields are declared in alphabetical order so the
// output is byte-identical to the legacy map[string]any serialization for
// plans that declare no style/background (locked by a golden test).

import (
	"fmt"
	"path/filepath"
	"strings"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

const (
	chrononSchema     = "chronon.render-plan"
	chrononVersion    = 1
	chrononFont       = "fonts/DejaVuSans.ttf"
	chrononOutputPath = "chronon.mp4"
	// Subtitle safe-area: the burned box spans the canvas minus a 48px side
	// margin, mirroring the ASS lower-safe-area contract.
	chrononSubtitleSideMargin = 48
)

// ── Typed Chronon render-plan v1 wire shape ────────────────────────────────

type chrononRenderPlan struct {
	Canvas  chrononCanvas  `json:"canvas"`
	JobID   string         `json:"job_id"`
	Layers  []chrononLayer `json:"layers"`
	Output  chrononOutput  `json:"output"`
	Schema  string         `json:"schema"`
	Version int            `json:"version"`
}

type chrononCanvas struct {
	DurationFrames int `json:"duration_frames"`
	FPS            int `json:"fps"`
	Height         int `json:"height"`
	Width          int `json:"width"`
}

type chrononOutput struct {
	Codec  string `json:"codec"`
	Format string `json:"format"`
	Path   string `json:"path"`
}

type chrononLayer struct {
	Animation      *chrononAnimation       `json:"animation,omitempty"`
	Background     *chrononLayerBackground `json:"background,omitempty"`
	BoxHeight      float64                 `json:"box_height,omitempty"`
	BoxWidth       float64                 `json:"box_width,omitempty"`
	Color          []float64               `json:"color,omitempty"`
	DurationFrames int                     `json:"duration_frames,omitempty"`
	Fit            string                  `json:"fit,omitempty"`
	Font           string                  `json:"font,omitempty"`
	FontSize       float64                 `json:"font_size,omitempty"`
	Format         string                  `json:"format,omitempty"`
	ID             string                  `json:"id"`
	Opacity        *float64                `json:"opacity,omitempty"`
	Position       []int                   `json:"position,omitempty"`
	Source         string                  `json:"source,omitempty"`
	StartFrame     int                     `json:"start_frame"`
	Style          *chrononLayerStyle      `json:"style,omitempty"`
	Text           string                  `json:"text,omitempty"`
	Type           string                  `json:"type"`
}

// chrononLayerBackground is the optional per-layer image background (asset
// behind the owning layer). Projected ONLY for background mode=asset.
type chrononLayerBackground struct {
	Asset string `json:"asset"`
	Fit   string `json:"fit,omitempty"`
}

// chrononLayerStyle is the LayerStylePlan projection: fill (#RRGGBB),
// font_size and a single shadow. Absent fields stay absent — no fabricated
// defaults (mirrors the omitempty wire contract of script.generate).
type chrononLayerStyle struct {
	Fill     string             `json:"fill,omitempty"`
	FontSize float64            `json:"font_size,omitempty"`
	Shadow   *chrononShadowSpec `json:"shadow,omitempty"`
}

type chrononShadowSpec struct {
	Blur    float64   `json:"blur,omitempty"`
	Color   string    `json:"color,omitempty"`
	Offset  []float64 `json:"offset,omitempty"`
	Opacity float64   `json:"opacity,omitempty"`
}

// chrononAnimation is the transition intent projection: an entry preset with
// an enter duration in frames (duration_ms converted at the canvas fps).
type chrononAnimation struct {
	Preset string        `json:"preset"`
	Enter  *chrononEnter `json:"enter,omitempty"`
}

type chrononEnter struct {
	DurationFrames int `json:"duration_frames"`
}

// ── The canonical projector ────────────────────────────────────────────────

// ChrononPlanProjector is the single canonical projection of a sealed
// ClipRenderPlanV1 onto the Chronon render-plan v1 wire shape. It is pure:
// reads only the sealed plan + the probed duration; never touches files,
// binaries or host state.
type ChrononPlanProjector struct{}

// Project lowers the sealed plan into the typed Chronon render plan.
// fps/frames derive from the output contract + probed duration
// (frames = ceil(durationMS × fps / 1000)) so the canvas every layer is
// timed against is computed in exactly one place.
//
// Fail-closed: a blur_source background cannot be represented by the Chronon
// render plan (no video blur primitive) — silently dropping it would publish
// a video without the declared background. The projector refuses instead.
func (ChrononPlanProjector) Project(plan cliprender.ClipRenderPlanV1, durationMS int64) (chrononRenderPlan, error) {
	fps := plan.Output.FPSNum / plan.Output.FPSDen
	if fps < 1 {
		fps = 1
	}
	frames := int((durationMS*int64(fps) + 999) / 1000)
	if frames < 1 {
		frames = 1
	}

	rp := chrononRenderPlan{
		Schema:  chrononSchema,
		Version: chrononVersion,
		JobID:   plan.RunID,
		Canvas: chrononCanvas{
			Width:          plan.Output.Width,
			Height:         plan.Output.Height,
			FPS:            fps,
			DurationFrames: frames,
		},
		Output: chrononOutput{Path: chrononOutputPath, Format: "mp4", Codec: "h264"},
	}

	if plan.Background != nil && plan.Background.Mode == cliprender.BackgroundModeBlurSource {
		return chrononRenderPlan{}, fmt.Errorf(
			"chronon plan: background mode %q is not representable by the Chronon render plan (no video blur primitive) — route blur_source to a backend that supports it or use background mode=asset",
			plan.Background.Mode)
	}

	video := chrononLayer{
		ID:             "video",
		Type:           "video",
		Source:         "clip.mp4",
		Fit:            "stretch",
		StartFrame:     0,
		DurationFrames: frames,
	}
	if plan.Background != nil && plan.Background.Mode == cliprender.BackgroundModeAsset &&
		strings.TrimSpace(plan.Background.Path) != "" {
		video.Background = &chrononLayerBackground{
			Asset: "background" + filepath.Ext(plan.Background.Path),
			Fit:   "cover",
		}
	}
	rp.Layers = append(rp.Layers, video)

	if plan.Watermark != nil {
		if strings.TrimSpace(plan.Watermark.Text) != "" {
			rp.Layers = append(rp.Layers, projectTextWatermark(
				plan.Watermark, plan.Output.Width, plan.Output.Height, frames, fps))
		} else if strings.TrimSpace(plan.Watermark.Path) != "" {
			rp.Layers = append(rp.Layers, projectImageWatermark(
				plan.Watermark, plan.Output.Width, plan.Output.Height, frames, fps))
		}
	}
	if plan.Subtitles != nil && strings.TrimSpace(plan.Subtitles.Path) != "" {
		rp.Layers = append(rp.Layers, projectSubtitles(
			plan.Subtitles, plan.Output.Width, frames, fps))
	}
	return rp, nil
}

func projectTextWatermark(wm *cliprender.PlanWatermark, canvasW, canvasH, frames, fps int) chrononLayer {
	fontSize := 64.0
	if wm.Style != nil && wm.Style.FontSizePX > 0 {
		fontSize = wm.Style.FontSizePX
	}
	color := []float64{1, 1, 1, wm.Opacity}
	if wm.Style != nil && strings.TrimSpace(wm.Style.Color) != "" {
		// Style fill replaces the white RGB; the watermark opacity stays the
		// alpha (same composition as the unstyled path).
		if rgb, ok := parseHexColor(wm.Style.Color); ok {
			color = []float64{rgb[0], rgb[1], rgb[2], wm.Opacity}
		}
	}
	layer := chrononLayer{
		ID:             "watermark",
		Type:           "text",
		Text:           wm.Text,
		Font:           chrononFont,
		FontSize:       fontSize,
		Color:          color,
		Position:       []int{canvasW / 2, canvasH / 2},
		StartFrame:     0,
		DurationFrames: frames,
	}
	if style := projectLayerStyle(wm.Style); style != nil {
		layer.Style = style
	}
	if anim := projectTransition(wm.Style, fps); anim != nil {
		layer.Animation = anim
	}
	return layer
}

func projectImageWatermark(wm *cliprender.PlanWatermark, canvasW, canvasH, frames, fps int) chrononLayer {
	wmW, wmH := watermarkDimensions(wm.Path)
	if wm.Style != nil {
		if wm.Style.WidthPX > 0 {
			wmW = wm.Style.WidthPX
		}
		if wm.Style.HeightPX > 0 {
			wmH = wm.Style.HeightPX
		}
	}
	layer := chrononLayer{
		ID:             "watermark",
		Type:           "image",
		Source:         "watermark" + filepath.Ext(wm.Path),
		Fit:            "none",
		BoxWidth:       float64(wmW),
		BoxHeight:      float64(wmH),
		Position:       watermarkPositionForSize(wmW, wmH, wm.Position, canvasW, canvasH, wm.MarginPX),
		Opacity:        &wm.Opacity,
		StartFrame:     0,
		DurationFrames: frames,
	}
	if anim := projectTransition(wm.Style, fps); anim != nil {
		layer.Animation = anim
	}
	return layer
}

func projectSubtitles(subs *cliprender.PlanSubtitles, canvasW, frames, fps int) chrononLayer {
	layer := chrononLayer{
		ID:             "subtitles",
		Type:           "subtitle_track",
		Source:         "subtitles.ass",
		Format:         "ass",
		Font:           chrononFont,
		BoxWidth:       float64(canvasW - 2*chrononSubtitleSideMargin),
		BoxHeight:      200,
		StartFrame:     0,
		DurationFrames: frames,
	}
	if style := projectLayerStyle(subs.Style); style != nil {
		layer.Style = style
	}
	if anim := projectTransition(subs.Style, fps); anim != nil {
		layer.Animation = anim
	}
	return layer
}

// ── Style + transition lowering (shared by watermark and subtitles) ───────

// projectLayerStyle lowers the canonical visual style block onto the Chronon
// LayerStylePlan projection. Only fields the request actually declared are
// emitted — no fake defaults.
func projectLayerStyle(style *scriptpkg.VideoVisualStyleSpec) *chrononLayerStyle {
	if style == nil {
		return nil
	}
	out := &chrononLayerStyle{}
	if strings.TrimSpace(style.Color) != "" {
		out.Fill = style.Color
	}
	if style.FontSizePX > 0 {
		out.FontSize = style.FontSizePX
	}
	if style.Shadow != nil {
		shadow := &chrononShadowSpec{}
		if strings.TrimSpace(style.Shadow.Color) != "" {
			shadow.Color = style.Shadow.Color
		}
		if style.Shadow.Opacity > 0 {
			shadow.Opacity = style.Shadow.Opacity
		}
		if style.Shadow.BlurPX > 0 {
			shadow.Blur = style.Shadow.BlurPX
		}
		if style.Shadow.OffsetX != 0 || style.Shadow.OffsetY != 0 {
			shadow.Offset = []float64{style.Shadow.OffsetX, style.Shadow.OffsetY}
		}
		out.Shadow = shadow
	}
	if out.Fill == "" && out.FontSize == 0 && out.Shadow == nil {
		return nil
	}
	return out
}

// projectTransition lowers the transition_in intent onto the Chronon
// animation block: the preset plus the enter duration converted to frames at
// the canvas fps (duration_ms × fps / 1000, rounded up).
func projectTransition(style *scriptpkg.VideoVisualStyleSpec, fps int) *chrononAnimation {
	if style == nil || style.TransitionIn == nil || strings.TrimSpace(style.TransitionIn.Preset) == "" {
		return nil
	}
	anim := &chrononAnimation{Preset: style.TransitionIn.Preset}
	if style.TransitionIn.DurationMS > 0 && fps > 0 {
		enterFrames := int((style.TransitionIn.DurationMS*int64(fps) + 999) / 1000)
		if enterFrames < 1 {
			enterFrames = 1
		}
		anim.Enter = &chrononEnter{DurationFrames: enterFrames}
	}
	return anim
}

// parseHexColor parses "#RRGGBB" into normalized [r, g, b] floats (0..1),
// mirroring the Chronon parse_hex_color contract. A malformed value is
// rejected — the caller keeps its default color.
func parseHexColor(value string) ([3]float64, bool) {
	if len(value) != 7 || value[0] != '#' {
		return [3]float64{}, false
	}
	hex := func(c byte) int {
		switch {
		case c >= '0' && c <= '9':
			return int(c - '0')
		case c >= 'a' && c <= 'f':
			return int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			return int(c-'A') + 10
		}
		return -1
	}
	var rgb [3]float64
	for i := 0; i < 3; i++ {
		hi, lo := hex(value[1+2*i]), hex(value[2+2*i])
		if hi < 0 || lo < 0 {
			return [3]float64{}, false
		}
		rgb[i] = float64(hi*16+lo) / 255.0
	}
	return rgb, true
}
