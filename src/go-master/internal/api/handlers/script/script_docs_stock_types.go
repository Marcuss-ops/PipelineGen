package script

import "sync"

type stockIndex struct {
	Version    string         `json:"version"`
	LastSync   string         `json:"last_sync"`
	RootFolder string         `json:"root_folder_id"`
	Clips      []stockClipRef `json:"clips"`
}

type stockClipRef struct {
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

type stockFolderMatchRecord struct {
	Folder  stockClipRef
	NormKey string
	Tokens  []string
	Counts  map[string]int
	Length  int
}

type stockFolderMatchIndex struct {
	Records []stockFolderMatchRecord
	DF      map[string]int
	AvgLen  float64
}

var stockFolderIndexCache = struct {
	mu   sync.Mutex
	data map[string]*stockFolderMatchIndex
}{
	data: make(map[string]*stockFolderMatchIndex),
}
