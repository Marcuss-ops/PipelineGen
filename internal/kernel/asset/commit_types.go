// Package asset — commit_types.go defines the canonical typed metadata
// and location commit shapes used by the MediaCommitter contract.
// These are pure data types; no SQL, transport, or DB dependencies.
package asset

// TypedMetadata is the canonical structured metadata written to
// media_assets.metadata_json during an atomic asset commit.
type TypedMetadata struct {
	Title           string
	Origin          string
	Description     string
	SourceVersion   string
	PublishAction   string
	SizeBytes       int64
	Round           int
	Event           string
	Subject         string
	Tags            []string
	Category        string
	SourceProvider  string
	SourceVideoID   string
	SourceTitle     string
	SourceChannel   string
	DrivePath       string
	IndexingStatus  string
	StartSec        float64
	EndSec          float64
	Slug            string
	Summary         string
	Topics          []string
	Speakers        []string
	MentionedPeople []string
	Hook            string
	QualityScore    float64
	SponsorSegment  bool
	Extra           map[string]any
}

// ToMap serialises the typed metadata into the canonical map shape
// written to media_assets.metadata_json.
func (m TypedMetadata) ToMap() map[string]any {
	out := make(map[string]any)
	setIfNotEmpty := func(k, v string) {
		if v != "" {
			out[k] = v
		}
	}
	setIfNotEmpty("description", m.Description)
	setIfNotEmpty("publish_action", m.PublishAction)
	setIfNotEmpty("event", m.Event)
	setIfNotEmpty("subject", m.Subject)
	setIfNotEmpty("source_title", m.SourceTitle)
	setIfNotEmpty("source_channel", m.SourceChannel)
	setIfNotEmpty("drive_path", m.DrivePath)
	setIfNotEmpty("indexing_status", m.IndexingStatus)
	setIfNotEmpty("slug", m.Slug)
	setIfNotEmpty("origin", m.Origin)
	setIfNotEmpty("summary", m.Summary)
	setIfNotEmpty("hook", m.Hook)
	if len(m.Topics) > 0 {
		out["topics"] = m.Topics
	}
	if len(m.Speakers) > 0 {
		out["speakers"] = m.Speakers
	}
	if len(m.MentionedPeople) > 0 {
		out["mentioned_people"] = m.MentionedPeople
	}
	if m.QualityScore != 0 {
		out["quality_score"] = m.QualityScore
	}
	if m.SponsorSegment {
		out["sponsor_segment"] = true
	}
	if m.SizeBytes != 0 {
		out["size_bytes"] = m.SizeBytes
	}
	if m.Round != 0 {
		out["round"] = m.Round
	}
	if m.StartSec != 0 {
		out["start_sec"] = m.StartSec
	}
	if m.EndSec != 0 {
		out["end_sec"] = m.EndSec
	}
	if len(m.Tags) > 0 {
		out["tags"] = append([]string(nil), m.Tags...)
	}
	for k, v := range m.Extra {
		if _, exists := out[k]; !exists && v != nil {
			out[k] = v
		}
	}
	return out
}

// LocationCommit describes one row to write into asset_locations.
type LocationCommit struct {
	Kind          string
	Provider      string
	ExternalID    string
	URI           string
	WebViewLink   string
	DownloadURL   string
	MimeType      string
	FileSizeBytes int64
	LegacyFileMD5 string
	IsPrimary     bool
}
