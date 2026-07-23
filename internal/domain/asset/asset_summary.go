package asset

import "time"

// Summary is a lightweight projection of an asset for list views.
type Summary struct {
	ID             string         `json:"id"`
	Source         Source         `json:"source"`
	Name           string         `json:"name"`
	Filename       string         `json:"filename"`
	MediaType      MediaType      `json:"media_type"`
	Category       string         `json:"category"`
	LifecycleState LifecycleState `json:"lifecycle_state"`
	PrimaryURI     string         `json:"primary_uri,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}
