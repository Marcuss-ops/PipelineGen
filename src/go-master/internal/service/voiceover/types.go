package voiceover

import "time"

type BatchRequest struct {
	Text             string              `json:"text"`
	Languages        []string            `json:"languages"`
	FilenameTemplate string              `json:"filename_template"`
	RemoveSilence    *bool               `json:"remove_silence,omitempty"`
	UploadDrive      *bool               `json:"upload_drive,omitempty"`
	SaveDB           *bool               `json:"save_db,omitempty"`
	Strategy         string              `json:"strategy"`
	Destination      *DestinationRequest `json:"destination,omitempty"`
	Metadata         map[string]any      `json:"metadata,omitempty"`
}

type DestinationRequest struct {
	Group           string `json:"group,omitempty"`
	FolderID        string `json:"folder_id,omitempty"`
	FolderPath      string `json:"folder_path,omitempty"`
	SubfolderName   string `json:"subfolder_name,omitempty"`
	CreateSubfolder bool   `json:"create_subfolder,omitempty"`
}

type BatchResponse struct {
	OK        bool        `json:"ok"`
	RequestID string      `json:"request_id"`
	Items     []BatchItem `json:"items"`
	Error     string      `json:"error,omitempty"`
}

type BatchItem struct {
	ID           string `json:"id"`
	Language     string `json:"language"`
	Voice        string `json:"voice,omitempty"`
	Filename     string `json:"filename"`
	LocalPath    string `json:"local_path,omitempty"`
	CleanedPath  string `json:"cleaned_path,omitempty"`
	DriveLink    string `json:"drive_link,omitempty"`
	DownloadLink string `json:"download_link,omitempty"`
	FileHash     string `json:"file_hash,omitempty"`
	Status       string `json:"status"`
	Error        string `json:"error,omitempty"`
}

type VoiceoverResult struct {
	OK    bool   `json:"ok"`
	Voice string `json:"voice,omitempty"`
	Path  string `json:"path,omitempty"`
	Error string `json:"error,omitempty"`
}

type ResolvedDestination struct {
	Group      string
	FolderID   string
	FolderPath string
	DriveLink  string
}

type BatchItemError struct {
	Item  BatchItem
	Error error
}

func (i *BatchItem) fail(status string, err error) BatchItem {
	i.Status = status
	i.Error = err.Error()
	return *i
}

func normalizeBatchRequest(req *BatchRequest) *BatchRequest {
	if req.FilenameTemplate == "" {
		req.FilenameTemplate = "{slug}_{lang}.mp3"
	}
	if req.Strategy == "" {
		req.Strategy = "verify"
	}
	if len(req.Languages) == 0 {
		req.Languages = []string{"en"}
	}
	return req
}

func buildRequestID() string {
	return "vo_" + time.Now().Format("20060102_150405") + "_" + randomSuffix(6)
}

func randomSuffix(n int) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = chars[time.Now().UnixNano()%int64(len(chars))]
		time.Sleep(1)
	}
	return string(b)
}
