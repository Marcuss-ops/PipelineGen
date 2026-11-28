package finaljob

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// RuntimeAssetFromOverlayAsset and RuntimeAssetFromOverlayRow adapt the
// versioned overlay DTOs into the runtime asset envelope without rebuilding a
// transport map. Overlay-specific metadata remains forward-compatible JSON in
// Additional, while the stable asset identity stays typed.
func RuntimeAssetFromOverlayAsset(value OverlayAssetV1, role string) (RuntimeAssetV1, error) {
	asset := RuntimeAssetV1{
		AssetID: value.AssetID, SHA256: value.SHA256, SizeBytes: value.SizeBytes,
		Role: role, URL: value.URL, SourceURI: value.SourceURI, DriveFileID: value.DriveFileID,
		Additional: make(map[string]json.RawMessage),
	}
	for _, field := range []struct {
		key   string
		value any
	}{
		{"duration_ms", value.DurationMS}, {"frame_count", value.FrameCount},
		{"mode", value.Mode}, {"audio_mode", value.AudioMode},
		{"codec", value.Codec}, {"container", value.Container}, {"pixel_format", value.PixelFormat},
		{"audio_streams", value.AudioStreams}, {"width", value.Width}, {"height", value.Height},
		{"fps_num", value.FPSNum}, {"fps_den", value.FPSDen},
	} {
		if err := addRuntimeAssetAdditional(&asset, field.key, field.value); err != nil {
			return RuntimeAssetV1{}, err
		}
	}
	for _, field := range []struct {
		key   string
		value any
		omit  bool
	}{
		{"duration_us", value.DurationUS, value.DurationUS == 0},
		{"profile_id", value.ProfileID, value.ProfileID == ""},
		{"copy_eligible", value.CopyEligible, !value.CopyEligible},
		{"first_frame_keyframe", value.FirstFrameKeyframe, !value.FirstFrameKeyframe},
		{"closed_gop", value.ClosedGOP, !value.ClosedGOP},
		{"stream", value.Stream, value.Stream == nil},
	} {
		if !field.omit {
			if err := addRuntimeAssetAdditional(&asset, field.key, field.value); err != nil {
				return RuntimeAssetV1{}, err
			}
		}
	}
	return validateOverlayRuntimeAsset(asset)
}

func RuntimeAssetFromOverlayRow(value OverlayRowV1, role string) (RuntimeAssetV1, error) {
	asset := RuntimeAssetV1{
		AssetID: value.AssetID, SHA256: value.SHA256, SizeBytes: value.SizeBytes,
		Role: role, URL: value.URL, SourceURI: value.SourceURI, DriveFileID: value.DriveFileID,
		Additional: make(map[string]json.RawMessage),
	}
	for _, field := range []struct {
		key   string
		value any
	}{
		{"id", value.ID}, {"start_frame", value.StartFrame}, {"end_frame", value.EndFrame},
		{"frame_count", value.FrameCount}, {"mode", value.Mode}, {"z_index", value.ZIndex},
		{"audio_mode", value.AudioMode},
	} {
		if err := addRuntimeAssetAdditional(&asset, field.key, field.value); err != nil {
			return RuntimeAssetV1{}, err
		}
	}
	for _, field := range []struct {
		key   string
		value any
		omit  bool
	}{
		{"duration_us", value.DurationUS, value.DurationUS == 0},
		{"duration_ms", value.DurationMS, value.DurationMS == 0},
		{"width", value.Width, value.Width == 0}, {"height", value.Height, value.Height == 0},
		{"fps_num", value.FPSNum, value.FPSNum == 0}, {"fps_den", value.FPSDen, value.FPSDen == 0},
		{"profile_id", value.ProfileID, value.ProfileID == ""},
		{"copy_eligible", value.CopyEligible, !value.CopyEligible},
		{"first_frame_keyframe", value.FirstFrameKeyframe, !value.FirstFrameKeyframe},
		{"closed_gop", value.ClosedGOP, !value.ClosedGOP},
		{"stream", value.Stream, value.Stream == nil},
	} {
		if !field.omit {
			if err := addRuntimeAssetAdditional(&asset, field.key, field.value); err != nil {
				return RuntimeAssetV1{}, err
			}
		}
	}
	return validateOverlayRuntimeAsset(asset)
}

func addRuntimeAssetAdditional(asset *RuntimeAssetV1, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode final_job overlay runtime asset field %q: %w", key, err)
	}
	asset.Additional[key] = raw
	return nil
}

func validateOverlayRuntimeAsset(asset RuntimeAssetV1) (RuntimeAssetV1, error) {
	if asset.AssetID == "" || len(asset.SHA256) != 64 || asset.SizeBytes <= 0 || asset.Role == "" {
		return RuntimeAssetV1{}, fmt.Errorf("final_job overlay runtime asset requires id, SHA-256, positive size, and role")
	}
	return asset, nil
}

// RuntimeAssetFromMap is the single ingress from the legacy map transport into
// the typed asset contract. No caller should carry the map past this boundary.
func RuntimeAssetFromMap(values map[string]any) (RuntimeAssetV1, error) {
	if values == nil {
		return RuntimeAssetV1{}, fmt.Errorf("final_job runtime asset is nil")
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return RuntimeAssetV1{}, fmt.Errorf("encode final_job runtime asset: %w", err)
	}
	var asset RuntimeAssetV1
	if err := json.Unmarshal(raw, &asset); err != nil {
		return RuntimeAssetV1{}, fmt.Errorf("decode final_job runtime asset: %w", err)
	}
	return asset, nil
}

// Map is the single egress from the typed asset back to the deployed Master
// map. It is called only by the wire functions, never by the preparation core.
func (a RuntimeAssetV1) Map() (map[string]any, error) {
	raw, err := json.Marshal(a)
	if err != nil {
		return nil, fmt.Errorf("encode final_job typed runtime asset: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var values map[string]any
	if err := decoder.Decode(&values); err != nil {
		return nil, fmt.Errorf("decode final_job typed runtime asset: %w", err)
	}
	normalizeRuntimeAssetNumbers(values)
	return values, nil
}

// normalizeRuntimeAssetNumbers restores the integer representation the legacy
// payload builder already produced for the Master. A json.Number that is not
// an integer remains a float so no precision is invented.
func normalizeRuntimeAssetNumbers(values map[string]any) {
	for key, value := range values {
		switch typed := value.(type) {
		case json.Number:
			if i, err := typed.Int64(); err == nil {
				values[key] = i
				continue
			}
			if f, err := typed.Float64(); err == nil {
				values[key] = f
			}
		case map[string]any:
			normalizeRuntimeAssetNumbers(typed)
		}
	}
}
