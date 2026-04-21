// Package timestamp gestisce il collegamento tra timestamp del testo e clip esistenti
package timestamp

import (
	"context"
	"time"
)

// TextSegment rappresenta un segmento di testo con timestamp
type TextSegment struct {
	ID        string    `json:"id"`
	Index     int       `json:"index"`       // Posizione nel testo
	StartTime float64   `json:"start_time"`  // Start time in secondi
	EndTime   float64   `json:"end_time"`    // End time in secondi
	Text      string    `json:"text"`        // Testo del segmento
	Keywords  []string  `json:"keywords"`    // Keywords estratte
	Entities  []string  `json:"entities"`    // Entità nominate
	Emotions  []string  `json:"emotions"`    // Emozioni del segmento
}

// ClipAssignment rappresenta una clip assegnata a un segmento
type ClipAssignment struct {
	ClipID         string  `json:"clip_id"`
	Source         string  `json:"source"`         // "drive" o "artlist"
	Name           string  `json:"name"`
	FolderPath     string  `json:"folder_path"`
	RelevanceScore float64 `json:"relevance_score"` // 0-100
	Duration       float64 `json:"duration"`
	DriveLink      string  `json:"drive_link"`
	MatchReason    string  `json:"match_reason"`
}

// TimestampMapping rappresenta il mapping completo per uno script
type TimestampMapping struct {
	ScriptID      string             `json:"script_id"`
	TotalDuration float64            `json:"total_duration"`
	Segments      []SegmentWithClips `json:"segments"`
	AverageScore  float64            `json:"average_score"`
	CreatedAt     time.Time          `json:"created_at"`
}

// SegmentWithClips combina un segmento di testo con le clip assegnate
type SegmentWithClips struct {
	Segment       TextSegment      `json:"segment"`
	AssignedClips []ClipAssignment `json:"assigned_clips"`
	BestScore     float64          `json:"best_score"`
	ClipCount     int              `json:"clip_count"`
}

// MappingRequest rappresenta una richiesta di mapping timestamp-clip
type MappingRequest struct {
	ScriptID       string  `json:"script_id"`
	Segments       []TextSegment `json:"segments"`
	MediaType      string  `json:"media_type"`      // "clip" o "stock"
	MaxClipsPerSegment int   `json:"max_clips_per_segment"`
	MinScore       float64 `json:"min_score"`
	IncludeDrive   bool    `json:"include_drive"`
	IncludeArtlist bool    `json:"include_artlist"`
}

// MappingResponse rappresenta la risposta del mapping
type MappingResponse struct {
	Success        bool             `json:"success"`
	Mapping        *TimestampMapping `json:"mapping,omitempty"`
	TotalSegments  int              `json:"total_segments"`
	TotalClips     int              `json:"total_clips"`
	AverageScore   float64          `json:"average_score"`
	Errors         []string         `json:"errors,omitempty"`
}

// Mapper interface per il mapping timestamp-clip
type Mapper interface {
	// MapSegmentsToClips mappa segmenti di testo a clip esistenti
	MapSegmentsToClips(ctx context.Context, req *MappingRequest) (*TimestampMapping, error)
}

// TotalClips returns the total number of clips across all segments
func (m *TimestampMapping) TotalClips() int {
	total := 0
	for _, seg := range m.Segments {
		total += seg.ClipCount
	}
	return total
}
