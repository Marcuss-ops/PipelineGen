// Package script — output_spec_test.go: regression-guard for OutputSpec.
package script

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestHasAnyPostprocessor_AllFlagsAndTrue verifies the Toggle tri-state
// OR for the active postprocessor flags.
func TestHasAnyPostprocessor_AllFlagsAndTrue(t *testing.T) {
	tests := []struct {
		name string
		spec OutputSpec
		want bool
	}{
		{
			name: "zero_valued_all_default",
			spec: OutputSpec{},
			want: false,
		},
		{
			name: "only_ExtractEntities",
			spec: OutputSpec{ExtractEntities: ToggleEnabled},
			want: true,
		},
		{
			name: "only_GenerateMetadata",
			spec: OutputSpec{GenerateMetadata: ToggleEnabled},
			want: true,
		},
		{
			name: "all_two_active_flags_enabled",
			spec: OutputSpec{
				ExtractEntities:  ToggleEnabled,
				GenerateMetadata: ToggleEnabled,
			},
			want: true,
		},
		{
			name: "caller_explicit_disabled_survives",
			spec: OutputSpec{
				ExtractEntities:  ToggleDisabled,
				GenerateMetadata: ToggleDisabled,
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := tt.spec
			if got := spec.HasAnyPostprocessor(); got != tt.want {
				t.Errorf("OutputSpec%+v.HasAnyPostprocessor() = %v, want %v",
					spec, got, tt.want)
			}
		})
	}
}

// TestOutputSpec_UnmarshalJSON_LegacyBoolPreservesCallerIntent
// regression-guards the legacy bool form mapping to the canonical
// Toggle tri-state (true → ToggleEnabled, false → ToggleDisabled).
func TestOutputSpec_UnmarshalJSON_LegacyBoolPreservesCallerIntent(t *testing.T) {
	tests := []struct {
		name        string
		payload     string
		wantExtract Toggle
		wantMeta    Toggle
	}{
		{
			name:        "legacy_true_maps_to_enabled",
			payload:     `{"extract_entities":true,"generate_metadata":true}`,
			wantExtract: ToggleEnabled,
			wantMeta:    ToggleEnabled,
		},
		{
			name:        "legacy_false_maps_to_disabled",
			payload:     `{"extract_entities":false,"generate_metadata":false}`,
			wantExtract: ToggleDisabled,
			wantMeta:    ToggleDisabled,
		},
		{
			name:        "canonical_string_enabled",
			payload:     `{"extract_entities":"enabled","generate_metadata":"enabled"}`,
			wantExtract: ToggleEnabled,
			wantMeta:    ToggleEnabled,
		},
		{
			name:        "canonical_string_disabled_survives_default_chain",
			payload:     `{"extract_entities":"disabled","generate_metadata":"disabled"}`,
			wantExtract: ToggleDisabled,
			wantMeta:    ToggleDisabled,
		},
		{
			name:        "omitted_field_defaults_to_default",
			payload:     `{}`,
			wantExtract: ToggleDefault,
			wantMeta:    ToggleDefault,
		},
		{
			name:        "invalid_string_returns_error",
			payload:     `{"extract_entities":"UNKNOWN"}`,
			wantExtract: "",
			wantMeta:    "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var spec OutputSpec
			err := json.Unmarshal([]byte(tt.payload), &spec)
			if tt.name == "invalid_string_returns_error" {
				if err == nil {
					t.Fatalf("expected error on invalid Toggle string; got nil (spec=%+v)", spec)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected unmarshal error: %v", err)
			}
			if spec.ExtractEntities != tt.wantExtract {
				t.Errorf("ExtractEntities = %q, want %q (payload=%s)",
					spec.ExtractEntities, tt.wantExtract, tt.payload)
			}
			if spec.GenerateMetadata != tt.wantMeta {
				t.Errorf("GenerateMetadata = %q, want %q (payload=%s)",
					spec.GenerateMetadata, tt.wantMeta, tt.payload)
			}
		})
	}
}

// TestHasAnyPostprocessor_DisabledSurvivesSafetyDefault locks the
// invariant that caller-explicit ToggleDisabled is preserved through
// applySafetyDefaults without silent override.
func TestHasAnyPostprocessor_DisabledSurvivesSafetyDefault(t *testing.T) {
	spec := OutputSpec{
		ExtractEntities:  ToggleDisabled,
		GenerateMetadata: ToggleDisabled,
	}
	if spec.HasAnyPostprocessor() {
		t.Errorf("HasAnyPostprocessor() = true after all-Disabled; " +
			"applySafetyDefaults must NOT override caller-explicit " +
			"ToggleDisabled (godlike/07 NO-FAKE-AVAILABILITY)")
	}
}

// TestVideoRenderSpec_UnmarshalBackgroundStyleTransition guards the full
// new render-block wire shape: background, shared visual style, shadow and
// transition blocks decode losslessly from a single script.generate payload.
func TestVideoRenderSpec_UnmarshalBackgroundStyleTransition(t *testing.T) {
	payload := `{
		"enabled": true,
		"background": {"mode": "blur_source"},
		"watermark": {
			"enabled": true,
			"asset_id": "logo_pulse",
			"position": "top_right",
			"opacity": 0.9,
			"margin_px": 24,
			"style": {
				"width_px": 180,
				"scale_percent": 100,
				"shadow": {"color": "#000000", "opacity": 0.55, "blur_px": 14, "offset_x": 0, "offset_y": 8},
				"transition_in": {"preset": "fade_in", "duration_ms": 250}
			}
		},
		"subtitles": {
			"enabled": true,
			"mode": "burn",
			"style_id": "shorts-v1",
			"style": {
				"color": "#FFFFFF",
				"font_size_px": 54,
				"shadow": {"color": "#000000", "opacity": 0.7, "blur_px": 10, "offset_x": 0, "offset_y": 5},
				"transition_in": {"preset": "fade_in", "duration_ms": 120}
			}
		}
	}`
	var spec VideoRenderSpec
	if err := json.Unmarshal([]byte(payload), &spec); err != nil {
		t.Fatalf("unmarshal render block: %v", err)
	}
	if !spec.Enabled {
		t.Fatal("enabled must decode as true")
	}
	if spec.Background == nil || spec.Background.Mode != "blur_source" {
		t.Fatalf("background not decoded: %+v", spec.Background)
	}
	if spec.Watermark == nil || !spec.Watermark.Enabled || spec.Watermark.AssetID != "logo_pulse" ||
		spec.Watermark.Position != "top_right" || spec.Watermark.Opacity != 0.9 || spec.Watermark.MarginPX != 24 {
		t.Fatalf("watermark base fields not decoded: %+v", spec.Watermark)
	}
	wmStyle := spec.Watermark.Style
	if wmStyle == nil || wmStyle.WidthPX != 180 || wmStyle.ScalePercent != 100 {
		t.Fatalf("watermark style not decoded: %+v", wmStyle)
	}
	if wmStyle.Shadow == nil || wmStyle.Shadow.Color != "#000000" || wmStyle.Shadow.Opacity != 0.55 ||
		wmStyle.Shadow.BlurPX != 14 || wmStyle.Shadow.OffsetX != 0 || wmStyle.Shadow.OffsetY != 8 {
		t.Fatalf("watermark shadow not decoded: %+v", wmStyle.Shadow)
	}
	if wmStyle.TransitionIn == nil || wmStyle.TransitionIn.Preset != "fade_in" || wmStyle.TransitionIn.DurationMS != 250 {
		t.Fatalf("watermark transition not decoded: %+v", wmStyle.TransitionIn)
	}
	if spec.Subtitles == nil || !spec.Subtitles.Enabled || spec.Subtitles.Mode != "burn" || spec.Subtitles.StyleID != "shorts-v1" {
		t.Fatalf("subtitles base fields not decoded: %+v", spec.Subtitles)
	}
	subStyle := spec.Subtitles.Style
	if subStyle == nil || subStyle.Color != "#FFFFFF" || subStyle.FontSizePX != 54 {
		t.Fatalf("subtitles style not decoded: %+v", subStyle)
	}
	if subStyle.Shadow == nil || subStyle.Shadow.Opacity != 0.7 || subStyle.Shadow.BlurPX != 10 || subStyle.Shadow.OffsetY != 5 {
		t.Fatalf("subtitles shadow not decoded: %+v", subStyle.Shadow)
	}
	if subStyle.TransitionIn == nil || subStyle.TransitionIn.Preset != "fade_in" || subStyle.TransitionIn.DurationMS != 120 {
		t.Fatalf("subtitles transition not decoded: %+v", subStyle.TransitionIn)
	}
}

// TestVideoRenderSpec_NormalizeBackground locks the background defaults and
// the enable rule: a real background layer (blur_source/asset) enables the
// video path exactly like an enabled overlay; none/omitted does not.
func TestVideoRenderSpec_NormalizeBackground(t *testing.T) {
	tests := []struct {
		name        string
		spec        VideoRenderSpec
		wantEnabled bool
		wantMode    string
	}{
		{
			name: "background_omitted",
			spec: VideoRenderSpec{},
		},
		{
			name:        "background_empty_mode_defaults_to_none",
			spec:        VideoRenderSpec{Background: &VideoBackgroundSpec{}},
			wantEnabled: false,
			wantMode:    "none",
		},
		{
			name:        "background_blur_source_enables_render",
			spec:        VideoRenderSpec{Background: &VideoBackgroundSpec{Mode: "blur_source"}},
			wantEnabled: true,
			wantMode:    "blur_source",
		},
		{
			name:        "background_asset_enables_render",
			spec:        VideoRenderSpec{Background: &VideoBackgroundSpec{Mode: "asset", AssetID: "bg-asset"}},
			wantEnabled: true,
			wantMode:    "asset",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.spec.Normalize()
			if tt.spec.Enabled != tt.wantEnabled {
				t.Errorf("Enabled = %v, want %v", tt.spec.Enabled, tt.wantEnabled)
			}
			if tt.wantMode == "" {
				if tt.spec.Background != nil {
					t.Errorf("Background = %+v, want nil", tt.spec.Background)
				}
				return
			}
			if tt.spec.Background == nil || tt.spec.Background.Mode != tt.wantMode {
				t.Errorf("Background = %+v, want mode %q", tt.spec.Background, tt.wantMode)
			}
		})
	}
}

// TestVideoRenderSpec_MarshalStyleOmitempty guards the wire-shape
// contract: an empty style block must stay omitted so legacy payloads and
// zero-value specs serialize identically to before.
func TestVideoRenderSpec_MarshalStyleOmitempty(t *testing.T) {
	spec := VideoRenderSpec{
		Enabled: true,
		Watermark: &VideoWatermarkSpec{
			Enabled:  true,
			AssetID:  "wm",
			Position: "top_right",
			Opacity:  1,
		},
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	wire := string(raw)
	if strings.Contains(wire, `"style"`) || strings.Contains(wire, `"background"`) || strings.Contains(wire, `"shadow"`) {
		t.Fatalf("empty style/background/shadow must be omitted from the wire: %s", wire)
	}
	var round VideoRenderSpec
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatalf("round-trip unmarshal: %v", err)
	}
}

// TestVideoRenderSpec_UnmarshalRoundTrip ensures the full extended block
// survives marshal → unmarshal without losing a field.
func TestVideoRenderSpec_UnmarshalRoundTrip(t *testing.T) {
	original := VideoRenderSpec{
		Enabled: true,
		Background: &VideoBackgroundSpec{
			Mode:    "asset",
			AssetID: "bg-asset",
		},
		Watermark: &VideoWatermarkSpec{
			Enabled:  true,
			AssetID:  "logo",
			Position: "center",
			Opacity:  0.8,
			MarginPX: 12,
			Style: &VideoVisualStyleSpec{
				Color:        "#FF0000",
				FontSizePX:   58,
				WidthPX:      180,
				HeightPX:     60,
				ScalePercent: 100,
				Shadow: &VideoShadowSpec{
					Color:   "#000000",
					Opacity: 0.6,
					BlurPX:  14,
					OffsetX: 0,
					OffsetY: 8,
				},
				TransitionIn: &VideoTransitionSpec{
					Preset:     "fade_in",
					DurationMS: 250,
				},
			},
		},
		Subtitles: &VideoSubtitlesSpec{
			Enabled: true,
			Mode:    "burn",
			StyleID: "shorts-v1",
			Style: &VideoVisualStyleSpec{
				FontSizePX: 54,
			},
		},
	}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var round VideoRenderSpec
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatalf("unmarshal: %v\njson=%s", err, raw)
	}
	if !reflect.DeepEqual(original, round) {
		t.Fatalf("round-trip mismatch:\noriginal=%#v\nround=%#v\njson=%s", original, round, raw)
	}
}
