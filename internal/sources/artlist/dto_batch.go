package artlist

// KeywordBatchRequest represents a keyword-driven batch process
type KeywordBatchRequest struct {
	Term           string `json:"term"`
	Limit          int    `json:"limit"`
	CandidateLimit int    `json:"candidate_limit"`
}

// KeywordBatchItem represents the outcome for one candidate clip
type KeywordBatchItem struct {
	ClipID    string `json:"clip_id"`
	Name      string `json:"name"`
	Source    string `json:"source"`
	DriveLink string `json:"drive_link,omitempty"`
	FileHash  string `json:"file_hash,omitempty"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
}

// KeywordBatchResponse represents a keyword batch execution
type KeywordBatchResponse struct {
	OK               bool               `json:"ok"`
	Term             string             `json:"term"`
	Requested        int                `json:"requested"`
	CandidatesFound  int                `json:"candidates_found"`
	Processed        int                `json:"processed"`
	SkippedOnDrive   int                `json:"skipped_on_drive"`
	SkippedDuplicate int                `json:"skipped_duplicate"`
	FolderID         string             `json:"folder_id"`
	FolderName       string             `json:"folder_name"`
	Results          []KeywordBatchItem `json:"results"`
}
