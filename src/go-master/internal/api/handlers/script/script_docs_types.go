package script

import "strings"

// ScriptDocsRequest is the input for modular script-doc generation.
type ScriptDocsRequest struct {
	Topic       string `json:"topic" binding:"required"`
	Duration    int    `json:"duration"`
	Language    string `json:"language"`
	Template    string `json:"template"`
	PreviewOnly bool   `json:"preview_only"`
	SourceText  string `json:"source_text"`
	Voiceover   bool   `json:"voiceover"`
}

func (r *ScriptDocsRequest) Normalize() {
	if r.Duration <= 0 {
		r.Duration = 60
	}
	if r.Language == "" {
		r.Language = "it"
	}
	if r.Template == "" {
		r.Template = "documentary"
	}
}

// ScriptSection is a named section in the generated document.
type ScriptSection struct {
	Title string
	Body  string
}

// ScriptDocument is the final assembled output before upload/preview.
type ScriptDocument struct {
	Title    string
	Content  string
	Sections []ScriptSection
	Timeline *TimelinePlan
}

type artlistIndex struct {
	FolderID string            `json:"folder_id"`
	Clips    []artlistClipItem `json:"clips"`
}

type artlistClipItem struct {
	ClipID     string   `json:"clip_id"`
	FolderID   string   `json:"folder_id"`
	Filename   string   `json:"filename"`
	Title      string   `json:"title"`
	Name       string   `json:"name"`
	URL        string   `json:"url"`
	DriveURL   string   `json:"drive_url"`
	Folder     string   `json:"folder"`
	Category   string   `json:"category"`
	Source     string   `json:"source"`
	Tags       []string `json:"tags"`
	Duration   int      `json:"duration"`
	Downloaded bool     `json:"downloaded"`
}

// DisplayName returns a human readable name for Artlist entries.
func (a artlistClipItem) DisplayName() string {
	if a.Title != "" {
		return a.Title
	}
	if a.Filename != "" {
		return a.Filename
	}
	if a.Name != "" {
		return a.Name
	}
	return a.ClipID
}

// PickLink returns the best available link for Artlist entries.
func (a artlistClipItem) PickLink() string {
	if strings.TrimSpace(a.URL) != "" {
		return a.URL
	}
	if strings.TrimSpace(a.DriveURL) != "" {
		return a.DriveURL
	}
	if strings.TrimSpace(a.FolderID) != "" {
		return "https://drive.google.com/drive/folders/" + a.FolderID
	}
	return ""
}
