package models

// MonitoredSource represents a discovered external source (YouTube video, Artlist asset, Drive file, etc.)
type MonitoredSource struct {
	ID             string `json:"id" db:"id"`
	Source         string `json:"source" db:"source"`
	ExternalID     string `json:"external_id" db:"external_id"`
	ExternalURL    string `json:"external_url" db:"external_url"`
	Title          string `json:"title" db:"title"`
	ChannelID      string `json:"channel_id" db:"channel_id"`
	ChannelURL     string `json:"channel_url" db:"channel_url"`
	Keyword        string `json:"keyword" db:"keyword"`
	GroupName      string `json:"group_name" db:"group_name"`
	Category       string `json:"category" db:"category"`
	Status         string `json:"status" db:"status"`
	LastSeenAt     string `json:"last_seen_at" db:"last_seen_at"`
	LastCheckedAt  string `json:"last_checked_at" db:"last_checked_at"`
	ProcessedCount int    `json:"processed_count" db:"processed_count"`
	MetadataJSON   string `json:"metadata_json" db:"metadata_json"`
	CreatedAt      string `json:"created_at" db:"created_at"`
	UpdatedAt      string `json:"updated_at" db:"updated_at"`
}

// TableName returns the database table name for the MonitoredSource model
func (MonitoredSource) TableName() string {
	return "monitored_sources"
}
