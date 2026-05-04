package catalog

// CatalogRecord represents a generic record from any of the media databases.
type CatalogRecord struct {
	ID        string   `json:"id"`
	Source    string   `json:"source"`
	Name      string   `json:"name"`
	Path      string   `json:"path"`
	Link      string   `json:"link"`
	DriveID   string   `json:"drive_id,omitempty"`
	TopicSlug string   `json:"topic_slug,omitempty"`
	MediaType string   `json:"media_type,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Duration  int      `json:"duration,omitempty"`
	Group     string   `json:"group,omitempty"`
}

// StockClipRef represents a reference to a stock clip or folder for matching.
type StockClipRef struct {
	ClipID     string   `json:"id"`
	Name       string   `json:"name"`
	Filename   string   `json:"filename"`
	FolderID   string   `json:"folder_id"`
	FolderPath string   `json:"folder_path"`
	FullPath   string   `json:"full_path"`
	TopicSlug  string   `json:"topic_slug"`
	Group      string   `json:"group"`
	MediaType  string   `json:"media_type"`
	DriveLink  string   `json:"drive_link"`
	Tags       []string `json:"tags"`
	Duration   int      `json:"duration"`
}

func (c StockClipRef) DisplayName() string {
	if c.Name != "" {
		return c.Name
	}
	return c.Filename
}

func (c StockClipRef) StockPath() string {
	if c.FullPath != "" {
		return c.FullPath
	}
	return c.FolderPath
}

func (c StockClipRef) PickLink() string {
	// Simple normalize for link and folderID
	link := c.DriveLink
	folderID := c.FolderID
	if link == "" && folderID != "" {
		return "https://drive.google.com/drive/folders/" + folderID
	}
	return link
}
