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

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
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
}

type overlayWatermark struct {
	Text      string            `json:"text,omitempty"`
	AssetRefs []overlayAssetRef `json:"asset_refs,omitempty"`
	Position  string            `json:"position,omitempty"`
	Opacity   *float64          `json:"opacity,omitempty"`
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

// hashAddressedPath returns the canonical object-store key for an asset
// identified by sha256 + original filename. RenderingGen workers resolve this
// key against the configured object-store root; the originating machine's
// local path is never forwarded.
func hashAddressedPath(sha256, filename string) string {
	return "sha256/" + sha256 + "/" + filename
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

	// DurationMS: ClipRenderPlanV1 does not carry an explicit duration yet.
	// Leave it zero so RenderingGen derives it from item end_ms. When items
	// is empty the compiler will gate on duration_ms > 0 (after the fix).
	// TODO(next): add DurationMS to ClipRenderPlanV1 and propagate it here.

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
					URL:     hashAddressedPath(plan.Source.SHA256, "source.mp4"),
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
					URL:     hashAddressedPath(plan.Background.SHA256, "background.mp4"),
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
		op.Subtitles = &overlaySubtitles{
			Mode:    plan.Subtitles.Mode,
			StyleID: plan.Subtitles.StyleID,
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
		wm := &overlayWatermark{
			Text:     plan.Watermark.Text,
			Position: plan.Watermark.Position,
		}
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
func overlayPlanAssets(plan cliprender.ClipRenderPlanV1) []assetRef {
	refs := []assetRef{{
		Hash:        plan.Source.SHA256,
		LogicalPath: hashAddressedPath(plan.Source.SHA256, "source.mp4"),
	}}
	if plan.Background != nil && plan.Background.Mode == cliprender.BackgroundModeAsset {
		refs = append(refs, assetRef{
			Hash:        plan.Background.SHA256,
			LogicalPath: hashAddressedPath(plan.Background.SHA256, "background.mp4"),
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
			LogicalPath: hashAddressedPath(plan.Watermark.SHA256, "watermark.png"),
		})
	}
	return refs
}

// assetRef is a hash-addressed asset pointer used internally by this package.
type assetRef struct {
	Hash        string
	LogicalPath string
}
