// Package bindings provides BindingExtractor implementations for job payload types.
// Each extractor knows how to pull asset references from a job payload
// and rewrite them with canonical velox-asset:// URIs.
package bindings

import (
	"github.com/Marcuss-ops/PipelineGen/internal/assetregistry"
)

// ── Voiceover Binding Extractor ────────────────────────────────────────

// VoiceoverBindings extracts voiceover audio assets from a job payload.
type VoiceoverBindings struct{}

// NewVoiceoverBindings creates a voiceover binding extractor.
func NewVoiceoverBindings() *VoiceoverBindings {
	return &VoiceoverBindings{}
}

// JobType returns the job type this extractor handles.
func (e *VoiceoverBindings) JobType() string { return "voiceover_batch" }

// Extract finds voiceover references in the payload.
// Looks for: voiceover_file, audio_path, and assets[].voiceover fields.
func (e *VoiceoverBindings) Extract(payload map[string]any) ([]assetregistry.Binding, error) {
	var bindings []assetregistry.Binding
	ordinal := 0

	// voiceover_file: single voiceover reference
	if ref, ok := getString(payload, "voiceover_file"); ok && ref != "" {
		bindings = append(bindings, assetregistry.Binding{
			Role:    assetregistry.RoleVoiceover,
			Ordinal: ordinal,
			Required: true,
			Source: assetregistry.Reference{
				Scheme: detectScheme(ref),
				Raw:    ref,
			},
		})
		ordinal++
	}

	// audio_path: alternative field name
	if ref, ok := getString(payload, "audio_path"); ok && ref != "" {
		bindings = append(bindings, assetregistry.Binding{
			Role:    assetregistry.RoleVoiceover,
			Ordinal: ordinal,
			Required: true,
			Source: assetregistry.Reference{
				Scheme: detectScheme(ref),
				Raw:    ref,
			},
		})
		ordinal++
	}

	return bindings, nil
}

// Rewrite replaces references in the payload with canonical velox-asset:// URIs.
func (e *VoiceoverBindings) Rewrite(payload map[string]any, resolved []assetregistry.ResolvedBinding) error {
	for _, rb := range resolved {
		uri := "velox-asset://" + rb.AssetID
		switch rb.Binding.Role {
		case assetregistry.RoleVoiceover:
			if _, ok := payload["voiceover_file"]; ok {
				payload["voiceover_file"] = uri
			}
			if _, ok := payload["audio_path"]; ok {
				payload["audio_path"] = uri
			}
		}
	}
	return nil
}

// ── Scene Image Binding Extractor ──────────────────────────────────────

// SceneImageBindings extracts scene image assets from a job payload.
type SceneImageBindings struct{}

// NewSceneImageBindings creates a scene image binding extractor.
func NewSceneImageBindings() *SceneImageBindings {
	return &SceneImageBindings{}
}

// JobType returns the job type this extractor handles.
func (e *SceneImageBindings) JobType() string { return "script.generate_from_clips" }

// Extract finds scene image references in the payload.
// Looks for: scenes[].image_path, scene_images[], and image_paths[].
func (e *SceneImageBindings) Extract(payload map[string]any) ([]assetregistry.Binding, error) {
	var bindings []assetregistry.Binding

	// scenes[].image_path
	if scenes, ok := getArray(payload, "scenes"); ok {
		for i, scene := range scenes {
			if sceneMap, ok := scene.(map[string]any); ok {
				if ref, ok := getString(sceneMap, "image_path"); ok && ref != "" {
					bindings = append(bindings, assetregistry.Binding{
						Role:     assetregistry.RoleSceneImage,
						Ordinal:  i,
						Required: false, // images are optional enhancers
						Source: assetregistry.Reference{
							Scheme: detectScheme(ref),
							Raw:    ref,
						},
					})
				}
			}
		}
	}

	// scene_images[]: flat array
	if sceneImages, ok := getStringArray(payload, "scene_images"); ok {
		for i, ref := range sceneImages {
			bindings = append(bindings, assetregistry.Binding{
				Role:     assetregistry.RoleSceneImage,
				Ordinal:  i,
				Required: false,
				Source: assetregistry.Reference{
					Scheme: detectScheme(ref),
					Raw:    ref,
				},
			})
		}
	}

	return bindings, nil
}

// Rewrite replaces image references with velox-asset:// URIs.
func (e *SceneImageBindings) Rewrite(payload map[string]any, resolved []assetregistry.ResolvedBinding) error {
	resolvedByOrdinal := make(map[int]string)
	for _, rb := range resolved {
		if rb.Binding.Role == assetregistry.RoleSceneImage {
			resolvedByOrdinal[rb.Binding.Ordinal] = "velox-asset://" + rb.AssetID
		}
	}

	// Rewrite scenes[].image_path
	if scenes, ok := getArray(payload, "scenes"); ok {
		for i, scene := range scenes {
			if sceneMap, ok := scene.(map[string]any); ok {
				if uri, ok := resolvedByOrdinal[i]; ok {
					sceneMap["image_path"] = uri
				}
			}
		}
	}

	// Rewrite scene_images[] flat array
	if sceneImages, ok := getStringArray(payload, "scene_images"); ok {
		for i := range sceneImages {
			if uri, ok := resolvedByOrdinal[i]; ok {
				sceneImages[i] = uri
			}
		}
		payload["scene_images"] = sceneImages
	}

	return nil
}

// ── Stock Clip Binding Extractor ───────────────────────────────────────

// StockClipBindings extracts stock clip/footage references.
type StockClipBindings struct{}

// NewStockClipBindings creates a stock clip binding extractor.
func NewStockClipBindings() *StockClipBindings {
	return &StockClipBindings{}
}

// JobType returns the job type this extractor handles.
func (e *StockClipBindings) JobType() string { return "script.generate_from_clips" }

// Extract finds stock clip references in the payload.
func (e *StockClipBindings) Extract(payload map[string]any) ([]assetregistry.Binding, error) {
	var bindings []assetregistry.Binding

	// stock_clips[]: array of clip references
	if clips, ok := getStringArray(payload, "stock_clips"); ok {
		for i, ref := range clips {
			bindings = append(bindings, assetregistry.Binding{
				Role:     assetregistry.RoleStockClip,
				Ordinal:  i,
				Required: false,
				Source: assetregistry.Reference{
					Scheme: detectScheme(ref),
					Raw:    ref,
				},
			})
		}
	}

	// clips[]: generic array
	if clips, ok := getStringArray(payload, "clips"); ok {
		startIdx := len(bindings)
		for i, ref := range clips {
			bindings = append(bindings, assetregistry.Binding{
				Role:     assetregistry.RoleStockClip,
				Ordinal:  startIdx + i,
				Required: false,
				Source: assetregistry.Reference{
					Scheme: detectScheme(ref),
					Raw:    ref,
				},
			})
		}
	}

	return bindings, nil
}

// Rewrite replaces stock clip references with velox-asset:// URIs.
func (e *StockClipBindings) Rewrite(payload map[string]any, resolved []assetregistry.ResolvedBinding) error {
	resolvedByOrdinal := make(map[int]string)
	for _, rb := range resolved {
		if rb.Binding.Role == assetregistry.RoleStockClip {
			resolvedByOrdinal[rb.Binding.Ordinal] = "velox-asset://" + rb.AssetID
		}
	}

	// Rewrite stock_clips[]
	if clips, ok := getStringArray(payload, "stock_clips"); ok {
		for i := range clips {
			if uri, ok := resolvedByOrdinal[i]; ok {
				clips[i] = uri
			}
		}
		payload["stock_clips"] = clips
	}

	// Rewrite clips[] (offset-aware)
	if clips, ok := getStringArray(payload, "clips"); ok {
		stockOffset := len(getStringArrayOrEmpty(payload, "stock_clips"))
		for i := range clips {
			if uri, ok := resolvedByOrdinal[stockOffset+i]; ok {
				clips[i] = uri
			}
		}
		payload["clips"] = clips
	}

	return nil
}

// ── Music Binding Extractor ────────────────────────────────────────────

// MusicBindings extracts background music references.
type MusicBindings struct{}

// NewMusicBindings creates a music binding extractor.
func NewMusicBindings() *MusicBindings { return &MusicBindings{} }

// JobType returns the job type this extractor handles.
func (e *MusicBindings) JobType() string { return "script.generate_from_clips" }

// Extract finds music references in the payload.
func (e *MusicBindings) Extract(payload map[string]any) ([]assetregistry.Binding, error) {
	var bindings []assetregistry.Binding

	// music_track: single music reference
	if ref, ok := getString(payload, "music_track"); ok && ref != "" {
		bindings = append(bindings, assetregistry.Binding{
			Role:     assetregistry.RoleMusic,
			Ordinal:  0,
			Required: false,
			Source: assetregistry.Reference{
				Scheme: detectScheme(ref),
				Raw:    ref,
			},
		})
	}

	// background_music: alternative field
	if ref, ok := getString(payload, "background_music"); ok && ref != "" {
		bindings = append(bindings, assetregistry.Binding{
			Role:     assetregistry.RoleMusic,
			Ordinal:  1,
			Required: false,
			Source: assetregistry.Reference{
				Scheme: detectScheme(ref),
				Raw:    ref,
			},
		})
	}

	return bindings, nil
}

// Rewrite replaces music references with velox-asset:// URIs.
func (e *MusicBindings) Rewrite(payload map[string]any, resolved []assetregistry.ResolvedBinding) error {
	for _, rb := range resolved {
		if rb.Binding.Role != assetregistry.RoleMusic {
			continue
		}
		uri := "velox-asset://" + rb.AssetID
		switch rb.Binding.Ordinal {
		case 0:
			payload["music_track"] = uri
		case 1:
			payload["background_music"] = uri
		}
	}
	return nil
}

// ── Thumbnail Binding Extractor ────────────────────────────────────────

// ThumbnailBindings extracts thumbnail image references.
type ThumbnailBindings struct{}

// NewThumbnailBindings creates a thumbnail binding extractor.
func NewThumbnailBindings() *ThumbnailBindings { return &ThumbnailBindings{} }

// JobType returns the job type this extractor handles.
func (e *ThumbnailBindings) JobType() string { return "script.generate_from_clips" }

// Extract finds thumbnail references in the payload.
func (e *ThumbnailBindings) Extract(payload map[string]any) ([]assetregistry.Binding, error) {
	// thumbnail_path: single thumbnail reference
	if ref, ok := getString(payload, "thumbnail_path"); ok && ref != "" {
		return []assetregistry.Binding{{
			Role:     assetregistry.RoleThumbnail,
			Ordinal:  0,
			Required: false,
			Source: assetregistry.Reference{
				Scheme: detectScheme(ref),
				Raw:    ref,
			},
		}}, nil
	}
	return nil, nil
}

// Rewrite replaces the thumbnail reference with a velox-asset:// URI.
func (e *ThumbnailBindings) Rewrite(payload map[string]any, resolved []assetregistry.ResolvedBinding) error {
	for _, rb := range resolved {
		if rb.Binding.Role == assetregistry.RoleThumbnail {
			payload["thumbnail_path"] = "velox-asset://" + rb.AssetID
			return nil
		}
	}
	return nil
}

// ── Helpers ────────────────────────────────────────────────────────────

// detectScheme infers the URI scheme from a reference string.
func detectScheme(ref string) string {
	if len(ref) > 7 && ref[:7] == "file://" {
		return "file"
	}
	if len(ref) > 8 && ref[:8] == "https://" {
		return "https"
	}
	if len(ref) > 7 && ref[:7] == "http://" {
		return "https" // treat http as https for SSRF purposes
	}
	if len(ref) > 6 && ref[:6] == "drive:" {
		return "drive"
	}
	if len(ref) > 12 && ref[:12] == "velox-asset:" {
		return "velox-asset"
	}
	// Default: treat as local file path
	return "file"
}

// getString safely extracts a string value from a map.
func getString(m map[string]any, key string) (string, bool) {
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// getStringArray safely extracts a string array from a map.
func getStringArray(m map[string]any, key string) ([]string, bool) {
	v, ok := m[key]
	if !ok {
		return nil, false
	}
	arr, ok := v.([]interface{})
	if !ok {
		return nil, false
	}
	result := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result, true
}

// getStringArrayOrEmpty returns an empty array if the key is missing.
func getStringArrayOrEmpty(m map[string]any, key string) []string {
	arr, _ := getStringArray(m, key)
	if arr == nil {
		return []string{}
	}
	return arr
}

// getArray safely extracts a generic array from a map.
func getArray(m map[string]any, key string) ([]interface{}, bool) {
	v, ok := m[key]
	if !ok {
		return nil, false
	}
	arr, ok := v.([]interface{})
	return arr, ok
}

// Compile-time checks
var (
	_ assetregistry.BindingExtractor = (*VoiceoverBindings)(nil)
	_ assetregistry.BindingExtractor = (*SceneImageBindings)(nil)
	_ assetregistry.BindingExtractor = (*StockClipBindings)(nil)
	_ assetregistry.BindingExtractor = (*MusicBindings)(nil)
	_ assetregistry.BindingExtractor = (*ThumbnailBindings)(nil)
)
