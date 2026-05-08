package script

import (
	"context"
	"strings"

	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/service/association"
	"velox/go-master/pkg/termutil"
)

func subjectMatchesTopic(subject string, topicTokens []string) bool {
	return termutil.SubjectMatchesTopic(subject, topicTokens)
}

func deriveFallbackSubject(seg *timelineLLMSegment, topic string, topicTokens []string) string {
	if entitySubject := preferredEntitySubject(seg, topicTokens); entitySubject != "" {
		return entitySubject
	}
	// conciseSubject disabled: produces bad subjects from first tokens
	// TODO: Implement VisualSubjectGenerator for proper subject extraction
	return topic
}

func preferredEntitySubject(seg *timelineLLMSegment, topicTokens []string) string {
	if seg == nil {
		return ""
	}
	return termutil.PreferredEntitySubject(seg.Entities, seg.Subject, topicTokens)
}

func looksLikePersonName(text string) bool {
	return termutil.LooksLikePersonName(text)
}

func resolveTimelineSegmentSubject(ctx context.Context, req ScriptDocsRequest, seg TimelineSegment, dataDir string, stockRepo *clips.Repository, assocService *association.Service) string {
	topic := strings.TrimSpace(req.Topic)
	rawSubject := strings.TrimSpace(seg.Subject)

	if assocService != nil {
		if direct, ok, err := assocService.FindDirectStockFolderCandidate(ctx, topic, rawSubject); err == nil && ok && direct != nil {
			if topic != "" && looksLikePersonName(topic) {
				return topic
			}
			if name := strings.TrimSpace(direct.Name); name != "" {
				return name
			}
		}
	}

	if entitySubject := preferredEntitySubject(&timelineLLMSegment{
		Subject:  rawSubject,
		Entities: seg.Entities,
	}, topicTokens(topic)); entitySubject != "" {
		return entitySubject
	}

	if subjectMatchesTopic(rawSubject, topicTokens(topic)) {
		return rawSubject
	}
	// conciseSubject disabled: produces bad subjects from first tokens
	if topic != "" {
		return topic
	}
	return rawSubject
}
