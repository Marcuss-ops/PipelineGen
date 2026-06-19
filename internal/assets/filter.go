package assets

// Filter defines query parameters for listing assets.
type Filter struct {
	Source       string   `json:"source,omitempty"`
	MediaType    string   `json:"media_type,omitempty"`
	States       []string `json:"states,omitempty"`
	IDs          []string `json:"ids,omitempty"`
	ExcludeIDs   []string `json:"exclude_ids,omitempty"`
	HasEmbedding *bool    `json:"has_embedding,omitempty"`
	IsFolder     *bool    `json:"is_folder,omitempty"`
	Category     string   `json:"category,omitempty"`
	Group        string   `json:"group_name,omitempty"`
	Limit        int      `json:"limit,omitempty"`
	Offset       int      `json:"offset,omitempty"`
}
