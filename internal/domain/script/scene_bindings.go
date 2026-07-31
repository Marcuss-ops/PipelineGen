package script

// SceneBindings holds the resolved asset references for a scene.
// Each optional binding is omitted when no asset of that type
// is associated with the scene.
type SceneBindings struct {
	// Clip binds this scene to a selected YouTube clip.
	Clip *ClipBinding `json:"clip,omitempty"`

	// Image binds this scene to an AI-generated image.
	Image *ImageBinding `json:"image,omitempty"`

	// Voiceover binds this scene to a generated voiceover audio track.
	Voiceover *VoiceoverBinding `json:"voiceover,omitempty"`

	// Stock binds this scene to a semantically associated stock
	// footage asset, found by vector search. When no stock matches
	// the scene text, falls back to the Clip.DriveLink.
	Stock *StockBinding          `json:"stock,omitempty"`
	Media []ResolvedMediaBinding `json:"media,omitempty"`
}

type ResolvedMediaBinding struct {
	Slot                 string  `json:"slot"`
	AssetID              string  `json:"asset_id,omitempty"`
	BindingID            string  `json:"binding_id,omitempty"`
	Provider             string  `json:"provider,omitempty"`
	MediaType            string  `json:"media_type,omitempty"`
	DriveLink            string  `json:"drive_link,omitempty"`
	Score                float64 `json:"score,omitempty"`
	MaterializationState string  `json:"materialization_state,omitempty"`
	CacheStatus          string  `json:"cache_status,omitempty"`
}
