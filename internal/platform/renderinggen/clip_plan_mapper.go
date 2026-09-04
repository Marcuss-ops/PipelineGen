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
	"os"
	"strings"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
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
	MarginPX *int       `json:"margin_px,omitempty"`
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

type transitionBlock struct {
	Preset     string `json:"preset,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

// marshalStyle projects the canonical kernel/script visual style into the
// wire block. A nil style stays nil on the wire (no empty object emitted).
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

	// Background
	if plan.Background != nil {
		switch plan.Background.Mode {
		case cliprender.BackgroundModeNone:
			// no background layer emitted
		case cliprender.BackgroundModeBlurSource:
			// blur_source is not a first-class overlay-plan primitive; it is
			// expressed as a "video" background referencing the source asset
			// with a blur fit hint so RenderingGen can apply the effect.
			op.Background = &overlayBackground{
				Kind: "video",
				AssetRefs: []overlayAssetRef{{
					AssetID: plan.Source.AssetID,
					SHA256:  plan.Source.SHA256,
					URL:     hashAddressedPath(plan.Source.AssetID, "source.mp4"),
				}},
				Fit:  "blur_cover",
				Loop: true,
			}
		case cliprender.BackgroundModeAsset:
			op.Background = &overlayBackground{
				Kind: "video",
				AssetRefs: []overlayAssetRef{{
					AssetID: plan.Background.AssetID,
					SHA256:  plan.Background.SHA256,
					URL:     hashAddressedPath(plan.Background.AssetID, "background.mp4"),
				}},
				Fit:  "cover",
				Loop: true,
			}
		default:
			return nil, fmt.Errorf("clip plan mapper: unsupported background mode %q", plan.Background.Mode)
		}
	}

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
		margin := plan.Watermark.MarginPX
		if margin <= 0 {
			margin = 40
		}
		wm.MarginPX = &margin
		if plan.Watermark.Opacity > 0 {
			op := plan.Watermark.Opacity
			wm.Opacity = &op
		}
		if plan.Watermark.SHA256 != "" {
			wm.AssetRefs = []overlayAssetRef{{
				AssetID:   plan.Watermark.AssetID,
				SHA256:    plan.Watermark.SHA256,
				URL:       hashAddressedPath(plan.Watermark.SHA256, "watermark.png"),
				MediaType: "image/png",
			}}
		}
		if wm.Text != "" && len(wm.AssetRefs) == 0 {
			font, err := watermarkFontAsset()
			if err != nil {
				return nil, fmt.Errorf("clip plan mapper: watermark font: %w", err)
			}
			wm.FontRef = &overlayAssetRef{AssetID: "font-montserrat-bold", SHA256: font.Hash, URL: font.LogicalPath, MediaType: "font/ttf"}
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

	raw, err := json.Marshal(op)
	if err != nil {
		return nil, fmt.Errorf("clip plan mapper: marshal overlay plan: %w", err)
	}
	return raw, nil
}

// overlayPlanAssets returns the content-addressed AssetRef list corresponding
// to a ClipRenderPlanV1. These refs use the same hash-addressed logical paths
// as the serialised overlay plan so the worker can materialise each asset from
// the object store.
func overlayPlanAssets(plan cliprender.ClipRenderPlanV1) ([]assetRef, error) {
	refs := []assetRef{{
		Hash:        plan.Source.SHA256,
		LogicalPath: hashAddressedPath(plan.Source.AssetID, "source.mp4"),
	}}
	if plan.Background != nil && plan.Background.Mode == cliprender.BackgroundModeAsset {
		refs = append(refs, assetRef{
			Hash:        plan.Background.SHA256,
			LogicalPath: hashAddressedPath(plan.Background.AssetID, "background.mp4"),
		})
	}
	if plan.Background != nil && plan.Background.Mode == cliprender.BackgroundModeBlurSource {
		// blur_source reuses the source asset — already registered above.
	}
	if plan.Subtitles != nil {
		refs = append(refs, assetRef{
			Hash:        plan.Subtitles.SHA256,
			LogicalPath: hashAddressedPath(plan.Subtitles.SHA256, "subtitles.ass"),
		})
	}
	if plan.Watermark != nil && plan.Watermark.SHA256 != "" {
		refs = append(refs, assetRef{
			Hash:        plan.Watermark.SHA256,
			LogicalPath: hashAddressedPath(plan.Watermark.AssetID, "watermark.png"),
		})
	}
	if plan.Watermark != nil && plan.Watermark.Text != "" && plan.Watermark.SHA256 == "" {
		font, err := watermarkFontAsset()
		if err != nil {
			return nil, fmt.Errorf("watermark font: %w", err)
		}
		refs = append(refs, font)
	}
	return refs, nil
}

// assetRef is a hash-addressed asset pointer used internally by this package.
type assetRef struct {
	Hash        string
	LogicalPath string
	LocalPath   string
}

func watermarkFontAsset() (assetRef, error) {
	const path = "assets/fonts/Montserrat-Bold.ttf"
	b, err := os.ReadFile(path)
	if err != nil {
		return assetRef{}, fmt.Errorf("read %s: %w", path, err)
	}
	return assetRef{Hash: digest.SHA256Bytes(b), LogicalPath: hashAddressedPath("font-montserrat-bold", "Montserrat-Bold.ttf"), LocalPath: path}, nil
}
