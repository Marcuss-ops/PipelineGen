// request_specs.go — clip.render payload specs, split out of request.go (600-line file gate).
package cliprender

import (
	"encoding/json"
	"fmt"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// blur_source: those modes carry no asset, so a declared kind is a
// contradiction.
type BackgroundSpec struct {
	Mode    string `json:"mode,omitempty"`
	AssetID string `json:"asset_id,omitempty"` // required when mode == "asset"
	// Kind is one of image|video (BackgroundKindImage / BackgroundKindVideo);
	// empty means "derive from the resolved asset media type".
	Kind string `json:"kind,omitempty"`
}

// WatermarkSpec overlays a canonical watermark asset. When disabled the
// block may be omitted entirely.
//
// Opacity and MarginPX deliberately carry NO `omitempty`: both have a non-zero
// contract default, so a requested explicit 0 (invisible watermark / flush to
// the edge) must survive the JSON round trip into the persisted job payload.
// With omitempty an explicit 0 was dropped on marshal and re-defaulted by the
// next Normalize(), which silently changed what the caller asked for.
type WatermarkSpec struct {
	Enabled  bool    `json:"enabled,omitempty"`
	Text     string  `json:"text,omitempty"`
	AssetID  string  `json:"asset_id,omitempty"` // required when enabled
	Position string  `json:"position,omitempty"` // default top_right
	Opacity  float64 `json:"opacity"`            // 0.0–1.0, default 1.0 when absent
	MarginPX int     `json:"margin_px"`          // >= 0, default 100 when absent
	// Style is the canonical visual override block (size, color, shadow,
	// transition). It is the kernel/script SSOT definition — this boundary
	// projects it verbatim, never re-defines it.
	Style *scriptpkg.VideoVisualStyleSpec `json:"style,omitempty"`

	// opacitySet and marginSet record whether the decoded JSON document
	// carried the key at all. "Absent" and "explicit 0" are different
	// contracts and Normalize() must be able to tell them apart.
	// Unexported so the wire shape stays unchanged.
	opacitySet bool
	marginSet  bool
}

// UnmarshalJSON records the presence of the two optional fields whose default
// is non-zero. encoding/json alone cannot distinguish an omitted key from an
// explicit 0, which is exactly the ambiguity that made a requested opacity 0
// render as 1.0. The alias type avoids recursing into this method.
func (w *WatermarkSpec) UnmarshalJSON(data []byte) error {
	type watermarkAlias WatermarkSpec
	var present map[string]json.RawMessage
	if err := json.Unmarshal(data, &present); err != nil {
		return err
	}
	var decoded watermarkAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*w = WatermarkSpec(decoded)
	_, w.opacitySet = present["opacity"]
	_, w.marginSet = present["margin_px"]
	return nil
}

// TranscriptSpec controls canonical transcript resolution. clip.render is a
// RENDER step, so the default is `reuse`: the canonical READY text track must
// already exist and a miss FAILS CLOSED. `generate` remains only as an
// explicit manual repair that runs ASR and persists the transcript once into
// the DB (never a temp WAV).
//
// Persist defaults to TRUE (see Normalize). Without persistence an explicit
// `generate` repair has nothing to reuse: EVERY clip of a batch over the same
// source re-runs speech recognition, which is the single largest per-clip cost
// that is not the render itself. An EXPLICIT `"persist": false` still opts
// out, which is why the presence of the key is recorded separately
// (persistSet).
type TranscriptSpec struct {
	Mode     string `json:"mode,omitempty"` // default reuse
	Language string `json:"language,omitempty"`
	// Persist deliberately has NO omitempty: an explicit false must survive the
	// persisted job payload, otherwise the worker's re-decode would see an
	// absent key and re-apply the true default (the same trap the watermark
	// opacity/margin_px contract documents).
	Persist bool `json:"persist"`

	// persistSet records whether the decoded JSON document carried the persist
	// key at all. "Absent" (apply the true default) and "explicit false" (opt
	// out) are different contracts and Normalize must tell them apart.
	// Unexported so the wire shape stays unchanged.
	persistSet bool
}

// UnmarshalJSON records the presence of persist AND preserves the endpoint's
// strict-decoding contract: encoding/json hands a type with a custom
// UnmarshalJSON its WHOLE subtree, so DisallowUnknownFields on the outer
// decoder no longer reaches inside it. The explicit key check below
// re-establishes that gate.
func (t *TranscriptSpec) UnmarshalJSON(data []byte) error {
	var present map[string]json.RawMessage
	if err := json.Unmarshal(data, &present); err != nil {
		return err
	}
	for key := range present {
		switch key {
		case "mode", "language", "persist":
		default:
			return fmt.Errorf("json: unknown field %q", key)
		}
	}
	type transcriptAlias TranscriptSpec
	var decoded transcriptAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*t = TranscriptSpec(decoded)
	_, t.persistSet = present["persist"]
	return nil
}

// SubtitlesSpec compiles a deterministic .ass from the canonical
// transcript timing — never re-runs speech recognition for subtitles.
type SubtitlesSpec struct {
	Enabled bool   `json:"enabled,omitempty"`
	Mode    string `json:"mode,omitempty"` // default burn
	StyleID string `json:"style_id,omitempty"`
	// Style is the canonical visual override block (color, size, shadow,
	// transition). It is the kernel/script SSOT definition — this boundary
	// projects it verbatim, never re-defines it.
	Style *scriptpkg.VideoVisualStyleSpec `json:"style,omitempty"`
}

// ClipEffectSpec selects one native Chronon effect on the complete clip composite.
// The allowlist and parameter bounds are intentionally closed at the HTTP edge.
type ClipEffectSpec struct {
	Type       string  `json:"type"`
	Amount     float64 `json:"amount,omitempty"`
	Radius     float64 `json:"radius,omitempty"`
	Softness   float64 `json:"softness,omitempty"`
	BlockSize  int     `json:"block_size,omitempty"`
	Size       int     `json:"size,omitempty"`
	Angle      float64 `json:"angle,omitempty"`
	Intensity  float64 `json:"intensity,omitempty"`
	Amplitude  float64 `json:"amplitude,omitempty"`
	Wavelength float64 `json:"wavelength,omitempty"`
	Seed       uint32  `json:"seed,omitempty"`
	Animated   bool    `json:"animated,omitempty"`
	Curvature  float64 `json:"curvature,omitempty"`
	Scale      float64 `json:"scale,omitempty"`
	Threshold  float64 `json:"threshold,omitempty"`
	Speed      float64 `json:"speed,omitempty"`
}

// ClipGridSpec draws a full-canvas technical grid over the final composite.
type ClipGridSpec struct {
	Style       string  `json:"style,omitempty"`
	SpacingPX   float64 `json:"spacing_px"`
	LineWidthPX float64 `json:"line_width_px,omitempty"`
	DotRadiusPX float64 `json:"dot_radius_px,omitempty"`
	Color       string  `json:"color"`
	Opacity     float64 `json:"opacity"`
}

// OutputSpec pins the VeloxEditing-compatible output contract. The
// precise codec/pixel/timebase values are owned by the contract
// (follow-up step); the request only selects it + resolution/fps.
type OutputSpec struct {
	Contract string `json:"contract,omitempty"` // default VELOX_ASSEMBLY_READY_V1
	Width    int    `json:"width,omitempty"`    // default 1920 (YouTube horizontal)
	Height   int    `json:"height,omitempty"`   // default 1080 (YouTube horizontal)
	FPSNum   int    `json:"fps_num,omitempty"`  // default 24
	FPSDen   int    `json:"fps_den,omitempty"`  // default 1
	// ForegroundScalePercent scales the source over the canvas while keeping
	// the resolved background visible. Zero means the contract default (100).
	ForegroundScalePercent int `json:"foreground_scale_percent,omitempty"`
	// SourceFrame is the optional card treatment of the rendered clip (border
	// frame behind the clip, drop shadow, clip radius, and/or perimeter stroke).
	// It is transported verbatim into the overlay contract; the renderer owns
	// the geometry.
	SourceFrame *PlanSourceFrame `json:"source_frame,omitempty"`
	// Effects applies native adjustment-layer filters to the complete composite.
	Effects []ClipEffectSpec `json:"effects,omitempty"`
	// Grid is a procedural full-canvas overlay, distinct from image filters.
	Grid *ClipGridSpec `json:"grid,omitempty"`
}

// AudioSpec selects the audio copy policy. copy_if_compatible never
// re-encodes audio that already satisfies the target contract.
type AudioSpec struct {
	Mode string `json:"mode,omitempty"` // default copy_if_compatible
}

// DestinationSpec routes the validated artifacts on Drive.
type DestinationSpec struct {
	DriveFolderID string `json:"drive_folder_id,omitempty"` // default DefaultDriveRootFolderID
	// SubfolderName is the optional human-readable leaf folder the clip is
	// published into, created/reused under DriveFolderID by the worker via
	// the canonical DestinationFolderResolver (never by the publisher). All
	// clips of one script/batch carry the SAME subfolder_name (e.g. the
	// script title), so each job resolves create-or-reuse and they converge
	// on a single shared Drive folder: ROOT/<SafeName(SubfolderName)>/
	// <clip>.mp4. Empty = publish directly into DriveFolderID (legacy
	// pre-resolved leaf behaviour).
	SubfolderName string `json:"subfolder_name,omitempty"`
}

// ExecutionSpec selects the render execution policy. It never names a
// backend: PipelineGen describes WHAT to render and RenderingGen decides HOW
// (asset resolution, semantic plan lowering, backend selection, Chronon
// execution).
type ExecutionSpec struct {
	// RequireGPU fails the render unless the artifact reports a GPU backend.
	// The worker enforces it via RenderBackend.IsGPUBackend (Chronon is the
	// only render backend and is GPU-native), so a request demanding GPU is
	// never satisfied by a non-GPU artifact. Default false skips the extra
	// assertion without weakening the unconditional Chronon-only gate.
	RequireGPU bool `json:"require_gpu,omitempty"`
	// RequireZeroCopy demands a device-local video path. It is UNSATISFIABLE
	// by construction: after the PATH B CUDA hybrid removal no backend
	// certifies video_zero_copy. RenderRequest.Validate therefore rejects a
	// request that sets it at the EDGE — 400 INVALID_PAYLOAD (typed
	// ErrInvalidRequest) before any enqueue, and typed ErrInvalidJobPayload on
	// a payload replay — never a silent downgrade and never a GPU render that
	// can only fail.
	RequireZeroCopy bool `json:"require_zero_copy,omitempty"`
}

// OverlayRefSpec carries the artifact lineage of the overlay the final video
// composites: the overlay.render queue job id, the frozen OverlayPlan
// fingerprint, the item render key, the source video asset the overlay
// was rendered over, and the declared compositing window (start_us/end_us)
// on the final timeline. It is the join that proves "this final video
// contains THAT overlay" — the final video asset record carries the same
// identity the OverlayPlan / EditingOverlaySpan declared, never a re-derived
// reference.
//
// Omitted (nil) means the final video carries no entity overlay (a plain
// subtitles/watermark clip). When present, every field is required: a
// partial lineage is fail-closed, never a half-proven composition.
type OverlayRefSpec struct {
	// RenderJobID is the overlay.render queue job id that produced the
	// composited overlay artifact.
	RenderJobID string `json:"render_job_id"`
	// PlanFingerprint is the frozen OverlayPlan fingerprint the render was
	// validated against (plan fingerprint == result fingerprint).
	PlanFingerprint string `json:"plan_fingerprint"`
	// RenderKey is the content-addressed render key of the plan item.
	RenderKey string `json:"render_key"`
	// SourceVideoAssetID is the video asset the overlay is composited over
	// (the OverlayPlan's VideoID).
	SourceVideoAssetID string `json:"source_video_asset_id"`
	// StartUS is the declared compositing window start on the final video
	// timeline (integer microseconds, the EditingOverlaySpan.StartUS the
	// request was built from).
	StartUS int64 `json:"start_us"`
	// EndUS is the declared compositing window end on the final video
	// timeline (integer microseconds, the EditingOverlaySpan.EndUS).
	EndUS int64 `json:"end_us"`
}
