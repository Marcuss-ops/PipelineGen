package channelmonitor

import (
	"strings"
	"time"
)

type FilterCriteria struct {
	Keywords        []string
	ExcludeKeywords []string
	MinViews       int64
	MinDuration   int
	MaxDuration  int
	Timeframe    string
	IsShorts     bool
}

type FilterResult struct {
	Matched      bool
	Reason      string
	Score       float64
}

type VideoInfo struct {
	ID          string
	Title       string
	Channel     string
	URL         string
	Views       int64
	Duration    int
	UploadDate  string
	Thumbnail  string
}

type FilterEngine struct{}

func NewFilterEngine() *FilterEngine {
	return &FilterEngine{}
}

func (f *FilterEngine) Match(video VideoInfo, criteria FilterCriteria) FilterResult {
	result := FilterResult{Matched: true}

	if criteria.IsShorts && f.isShorts(video.Title) {
		return FilterResult{Matched: false, Reason: "is_shorts"}
	}

	if criteria.MinViews > 0 && video.Views < criteria.MinViews {
		return FilterResult{Matched: false, Reason: "views_below_min"}
	}

	if criteria.MinDuration > 0 && video.Duration < criteria.MinDuration {
		return FilterResult{Matched: false, Reason: "duration_below_min"}
	}

	if criteria.MaxDuration > 0 && video.Duration > criteria.MaxDuration {
		return FilterResult{Matched: false, Reason: "duration_above_max"}
	}

	if len(criteria.ExcludeKeywords) > 0 {
		lower := strings.ToLower(video.Title)
		for _, kw := range criteria.ExcludeKeywords {
			if strings.Contains(lower, strings.ToLower(kw)) {
				return FilterResult{Matched: false, Reason: "excluded_keyword"}
			}
		}
	}

	if len(criteria.Keywords) > 0 {
		lower := strings.ToLower(video.Title)
		matched := false
		score := 0.0
		for _, kw := range criteria.Keywords {
			if strings.Contains(lower, strings.ToLower(kw)) {
				matched = true
				score++
			}
		}
		if !matched {
			return FilterResult{Matched: false, Reason: "no_keyword_match"}
		}
		result.Score = score / float64(len(criteria.Keywords))
	}

	if criteria.Timeframe != "" {
		if !f.isWithinTimeframeFromCriteria(video, criteria.Timeframe) {
			return FilterResult{Matched: false, Reason: "outside_timeframe"}
		}
	}

	return result
}

func (f *FilterEngine) isShorts(title string) bool {
	lower := strings.ToLower(title)
	return strings.Contains(lower, "#shorts") || strings.Contains(lower, "| short")
}

func (f *FilterEngine) isWithinTimeframeFromCriteria(video VideoInfo, tf string) bool {
	windowStart := f.timeframeStart(tf)
	return f.isWithinTimeframe(video, windowStart)
}

func (f *FilterEngine) timeframeStart(tf string) time.Time {
	now := time.Now().UTC()
	switch tf {
	case "24h":
		return now.Add(-24 * time.Hour)
	case "week", "7d":
		return now.Add(-7 * 24 * time.Hour)
	case "month", "30d":
		return now.Add(-30 * 24 * time.Hour)
	default:
		return now.Add(-30 * 24 * time.Hour)
	}
}

func (f *FilterEngine) isWithinTimeframe(video VideoInfo, windowStart time.Time) bool {
	if video.UploadDate == "" || video.UploadDate == "NA" {
		return true
	}

	uploadDate, err := parseUploadDate(video.UploadDate)
	if err != nil {
		return true
	}
	return uploadDate.After(windowStart) || uploadDate.Equal(windowStart)
}

func (f *FilterEngine) CriteriaFromChannel(ch ChannelConfig) FilterCriteria {
	return FilterCriteria{
		Keywords:       ch.Keywords,
		MinViews:       ch.MinViews,
		MinDuration:   0,
		MaxDuration:   0,
		Timeframe:    "",
		IsShorts:     true,
	}
}

func (f *FilterEngine) MatchVideo(video VideoInfo, ch ChannelConfig) FilterResult {
	return f.Match(video, f.CriteriaFromChannel(ch))
}