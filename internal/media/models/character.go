package models

import (
	"encoding/json"
	"time"
)

// Character represents an AI Avatar or Character registry entry
type Character struct {
	ID             string         `json:"id"`               // slug like 'alex'
	Name           string         `json:"name"`             // display name 'Alex'
	ImageDriveID   string         `json:"image_drive_id"`   // Google Drive file ID
	ImageDriveLink string         `json:"image_drive_link"` // Google Drive web link
	VoiceID        string         `json:"voice_id"`         // Optional: preferred voice
	Metadata       map[string]any `json:"metadata"`         // Flexible metadata
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

// MetadataJSON returns the Metadata map serialized as a JSON string
func (c *Character) MetadataJSON() string {
	if c.Metadata == nil {
		return "{}"
	}
	b, err := json.Marshal(c.Metadata)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// SetMetadataJSON parses a JSON string into the Metadata map
func (c *Character) SetMetadataJSON(jsonStr string) {
	if jsonStr == "" || jsonStr == "{}" || jsonStr == "null" {
		c.Metadata = make(map[string]any)
		return
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &meta); err != nil {
		c.Metadata = make(map[string]any)
		return
	}
	c.Metadata = meta
}
