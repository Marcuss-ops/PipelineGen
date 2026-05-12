package visualplanner

import (
	"strings"

	"velox/go-master/internal/ml/ollama/types"
)

// SubjectKind defines the category of a visual subject
type SubjectKind string

const (
	KindPerson   SubjectKind = "person"
	KindOrg      SubjectKind = "org"
	KindPlace    SubjectKind = "place"
	KindConcept  SubjectKind = "concept"
	KindObject   SubjectKind = "object"
	KindFallback SubjectKind = "fallback"
)

// TimelineSegment holds the subset of timeline data needed for visual planning
type TimelineSegment struct {
	Index             int
	VisualSubject     string
	VisualCaption     string
	SearchSuggestions []string
}

// VisualSubject represents a candidate for an image or stock clip
type VisualSubject struct {
	Text     string
	Kind     SubjectKind
	Source   string
	Priority int
	Query    string
}

// SegmentVisualPlan holds visual strategy for a specific timeline segment
type SegmentVisualPlan struct {
	SegmentIndex  int
	VisualSubject string
	VisualCaption string
	SearchQueries []string
	ImageSubjects []VisualSubject
}

// VisualPlan is the unified source of truth for script visuals
type VisualPlan struct {
	Topic          string
	GlobalSubjects []VisualSubject
	SegmentPlans   []SegmentVisualPlan
}

// Build creates a visual plan from analysis and timeline data
func Build(topic, narrative string, analysis *types.FullEntityAnalysis, segments []TimelineSegment) *VisualPlan {
	plan := &VisualPlan{
		Topic: topic,
	}

	// 1. Process Global Subjects from analysis
	plan.GlobalSubjects = extractGlobalSubjects(topic, analysis, segments)

	// 2. Process Segment-level plans
	for _, seg := range segments {
		segPlan := SegmentVisualPlan{
			SegmentIndex:  seg.Index,
			VisualSubject: seg.VisualSubject,
			VisualCaption: seg.VisualCaption,
			SearchQueries: seg.SearchSuggestions,
		}
		
		// Maybe add segment-specific image subjects here if needed
		plan.SegmentPlans = append(plan.SegmentPlans, segPlan)
	}

	return plan
}

func extractGlobalSubjects(topic string, analysis *types.FullEntityAnalysis, segments []TimelineSegment) []VisualSubject {
	seen := make(map[string]struct{})
	var result []VisualSubject

	add := func(text string, kind SubjectKind, source string, priority int) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		
		lower := strings.ToLower(text)
		if _, ok := seen[lower]; ok {
			return
		}

		// Basic quality filters moved from pickImageSubjects
		if len([]rune(text)) < 3 || strings.Count(text, " ") > 5 {
			return
		}

		seen[lower] = struct{}{}
		result = append(result, VisualSubject{
			Text:     text,
			Kind:     kind,
			Source:   source,
			Priority: priority,
			Query:    text,
		})
	}

	// Priority 1: Special Names from Analysis
	if analysis != nil {
		for _, segEntities := range analysis.SegmentEntities {
			for _, name := range segEntities.NomiSpeciali {
				prio := 100
				if !strings.Contains(name, " ") {
					prio = 80 // Single word names get lower priority than full names
				}
				add(name, KindPerson, "analysis_special_names", prio)
			}
		}
	}

	// Priority 2: Visual Subjects from Timeline Segments
	for _, seg := range segments {
		if seg.VisualSubject != "" {
			add(seg.VisualSubject, KindConcept, "timeline_visual_subject", 90)
		}
	}

	// Priority 3: Important words (multi-word only for quality)
	if analysis != nil {
		for _, segEntities := range analysis.SegmentEntities {
			for _, word := range segEntities.ParoleImportanti {
				if strings.Contains(word, " ") {
					add(word, KindConcept, "analysis_important_words", 60)
				}
			}
		}
	}

	// Fallback: Topic
	add(topic, KindFallback, "topic", 50)

	return result
}

// GlobalImageSubjects returns up to max top priority subjects for general image planning
func (p *VisualPlan) GlobalImageSubjects(max int) []string {
	var subjects []string
	for i, s := range p.GlobalSubjects {
		if i >= max {
			break
		}
		subjects = append(subjects, s.Text)
	}
	return subjects
}
