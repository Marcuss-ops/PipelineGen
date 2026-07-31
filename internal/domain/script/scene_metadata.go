package script

// SceneMetadata carries technical scene data that should not be
// read as narration. It is separate from Text by contract.
type SceneMetadata struct {
	SourceURL string   `json:"source_url,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Keywords  []string `json:"keywords,omitempty"`
	Raw       string   `json:"raw,omitempty"`
}
