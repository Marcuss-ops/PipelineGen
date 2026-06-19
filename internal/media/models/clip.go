package models

import (
	"time"
)

// IndexingCheckpoint represents a checkpoint for the indexing process
type IndexingCheckpoint struct {
	ID            string    `json:"id"`
	Path          string    `json:"path"`
	LastIndexedAt time.Time `json:"last_indexed_at"`
	Metadata      string    `json:"metadata"`
}
