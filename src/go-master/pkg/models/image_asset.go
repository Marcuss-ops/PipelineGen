package models

import (
	"time"
)

// Subject rappresenta un'entità o un argomento (es: "Mike Tyson")
type Subject struct {
	ID          int64     `json:"id"`
	Slug        string    `json:"slug"`
	DisplayName string    `json:"display_name"`
	WikidataID  string    `json:"wikidata_id,omitempty"`
	Aliases     []string  `json:"aliases"` // Gestito come JSON nel DB
	Category    string    `json:"category"`
	Notes       string    `json:"notes"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ImageAsset rappresenta un'immagine archiviata
type ImageAsset struct {
	ID           int64     `json:"id"`
	Hash         string    `json:"hash"`
	SubjectID    int64     `json:"subject_id"`
	PathRel      string    `json:"path_rel"`
	SourceURL    string    `json:"source_url"`
	License      string    `json:"license"`
	Width        int       `json:"width"`
	Height       int       `json:"height"`
	SizeBytes    int64     `json:"size_bytes"`
	QualityScore int       `json:"quality_score"`
	Description  string    `json:"description"`
	MetadataJSON string    `json:"metadata_json"`
	CreatedAt    time.Time `json:"created_at"`
	Tags         []string  `json:"tags,omitempty"`
}

// ImageUsage traccia l'utilizzo di un'immagine in un video
type ImageUsage struct {
	ID        int64     `json:"id"`
	ImageID   int64     `json:"image_id"`
	VideoID   string    `json:"video_id"`
	UsedAt    time.Time `json:"used_at"`
}

// ImageTag rappresenta un tag associato a un'immagine
type ImageTag struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}
