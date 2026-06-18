// Package indexing provides the domain types for the indexing subsystem.
package indexing

import "time"

// Checkpoint represents a checkpoint for the indexing process.
type Checkpoint struct {
	ID            string    `json:"id"`
	Path          string    `json:"path"`
	LastIndexedAt time.Time `json:"last_indexed_at"`
	Metadata      string    `json:"metadata"`
}
