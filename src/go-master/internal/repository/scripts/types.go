package scripts

import (
	"time"
)

// ScriptRecord represents a script record in the database
type ScriptRecord struct {
	ID              int64
	Topic           string
	Duration        int
	Language        string
	Template        string
	Mode            string
	NarrativeText   string
	TimelineJSON    string
	EntitiesJSON    string
	MetadataJSON    string
	FullDocument    string
	ModelUsed       string
	OllamaBaseURL   string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	Version         int
	ParentScriptID  *int64
	IsDeleted       bool
}

// ScriptSectionRecord represents a section of a script
type ScriptSectionRecord struct {
	ID         int64
	ScriptID   int64
	SectionType string
	SectionTitle string
	Content    string
	SortOrder  int
}

// ScriptStockMatchRecord represents a stock match for a script segment
type ScriptStockMatchRecord struct {
	ID           int64
	ScriptID     int64
	SegmentIndex int
	StockPath    string
	StockSource  string
	Score        float64
	MatchedTerms string
}