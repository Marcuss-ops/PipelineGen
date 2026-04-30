package script

// clipDriveIndex represents the structure of the clip drive catalog index
type clipDriveIndex struct {
	Clips []clipDriveRecord `json:"clips"`
}

// clipDriveRecord represents a single clip record in the catalog
type clipDriveRecord struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Filename     string   `json:"filename"`
	FolderID     string   `json:"folder_id"`
	FolderPath   string   `json:"folder_path"`
	Group        string   `json:"group"`
	MediaType    string   `json:"media_type"`
	DriveLink    string   `json:"drive_link"`
	DownloadLink string   `json:"download_link"`
	Tags         []string `json:"tags"`
	Source       string   `json:"source"`
}

// clipDriveCandidate represents a candidate clip for matching
type clipDriveCandidate struct {
	Record   clipDriveRecord
	Score    float64
	Reason   string
	Text     string
	SideText string
}

// clipDriveLLMSelection represents the LLM's selection response
type clipDriveLLMSelection struct {
	ClipID string  `json:"clip_id"`
	Score  float64 `json:"score"`
	Reason string  `json:"reason"`
}

// clipDrivePhraseMatch represents a matched phrase with clip information
type clipDrivePhraseMatch struct {
	Sentence string
	ClipID   string
	Title    string
	Link     string
	Score    float64
	Reason   string
}