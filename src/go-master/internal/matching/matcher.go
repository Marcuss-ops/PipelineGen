package matching

// Matcher provides methods for scoring and ranking assets
type Matcher struct {
	config ScoringConfig
}

// NewMatcher creates a new matcher with default configuration
func NewMatcher() *Matcher {
	return &Matcher{config: DefaultScoringConfig()}
}

// NewMatcherWithConfig creates a new matcher with custom configuration
func NewMatcherWithConfig(cfg ScoringConfig) *Matcher {
	return &Matcher{config: cfg}
}

// ScoreAsset calculates a match score between a phrase and an asset
func (m *Matcher) ScoreAsset(phrase string, name, filename, folder, tags string) (float64, string) {
	return ScoreAsset(phrase, name, filename, folder, tags, m.config)
}
