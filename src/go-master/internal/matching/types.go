package matching

// MatchResult represents a generic match with score and metadata
type MatchResult struct {
	ID      string
	Title   string
	Path    string
	Link    string
	Score   float64
	Source  string
	Details string
	Tags    []string
}

// ScoringConfig holds boost values for different match types
type ScoringConfig struct {
	NameMatchBoost       float64
	FilenameMatchBoost   float64
	FolderMatchBoost     float64
	TopicMatchBoost      float64
	SideTextBoost        float64
}

// DefaultScoringConfig returns the default scoring configuration
func DefaultScoringConfig() ScoringConfig {
	return ScoringConfig{
		NameMatchBoost:     20.0,
		FilenameMatchBoost: 18.0,
		FolderMatchBoost:   10.0,
		TopicMatchBoost:    5.0,
		SideTextBoost:      5.0,
	}
}
