package youtube

import (
	"context"
)

// classifyCategory classifies the video title using the wired CategoryClassifierPort.
// Returns "general" when no classifier is wired.
func (s *Service) classifyCategory(ctx context.Context, title string) string {
	if s.classifier == nil {
		return "general"
	}
	return s.classifier.Classify(ctx, title)
}
