package semantic

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

// PeakOptions bounds temporal peak selection. MinDistanceUS applies greedy
// non-maximum suppression in descending score order; equal scores prefer the
// earlier timestamp. ThresholdPercentile is in [0, 100] and uses nearest-rank
// quantiles. A zero percentile disables thresholding. MaxPeaks <= 0 is
// unlimited.
type PeakOptions struct {
	MinDistanceUS       int64   `json:"min_distance_us"`
	ThresholdPercentile float64 `json:"threshold_percentile"`
	MaxPeaks            int     `json:"max_peaks"`
}

// SelectPeaks returns temporally separated local maxima from already-scored
// sentences. It never changes timestamps or scores. Output is in chronological
// order, independent of input-score ordering.
func SelectPeaks(scores []Impact, options PeakOptions) ([]Impact, error) {
	if options.MinDistanceUS < 0 {
		return nil, ErrInvalidPeakOptions
	}
	if math.IsNaN(options.ThresholdPercentile) || math.IsInf(options.ThresholdPercentile, 0) ||
		options.ThresholdPercentile < 0 || options.ThresholdPercentile > 100 {
		return nil, ErrInvalidPeakOptions
	}
	if len(scores) == 0 {
		return nil, nil
	}
	ordered := append([]Impact(nil), scores...)
	for i, score := range ordered {
		if score.StartUS < 0 || score.EndUS <= score.StartUS ||
			math.IsNaN(score.Score) || math.IsInf(score.Score, 0) || score.Score < 0 || score.Score > 1 {
			return nil, invalidPeak(i)
		}
		if i > 0 && score.StartUS < ordered[i-1].StartUS {
			return nil, invalidPeak(i)
		}
	}
	if len(ordered) < 3 {
		return nil, nil
	}
	minScore, maxScore := ordered[0].Score, ordered[0].Score
	for _, score := range ordered[1:] {
		if score.Score < minScore {
			minScore = score.Score
		}
		if score.Score > maxScore {
			maxScore = score.Score
		}
	}
	if maxScore == minScore {
		return nil, nil
	}

	threshold := percentileThreshold(ordered, options.ThresholdPercentile)
	candidates := make([]Impact, 0, len(ordered)/3)
	for i := 0; i < len(ordered); {
		end := i + 1
		for end < len(ordered) && ordered[end].Score == ordered[i].Score {
			end++
		}
		current := ordered[i]
		leftHigher := i > 0 && ordered[i-1].Score > current.Score
		rightHigher := end < len(ordered) && ordered[end].Score > current.Score
		// A flat plateau resolves to its first item only when the whole run
		// is a local maximum; a later, higher neighbor disqualifies it.
		if current.Score >= threshold && !leftHigher && !rightHigher {
			candidates = append(candidates, current)
		}
		i = end
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	byScore := append([]Impact(nil), candidates...)
	sort.SliceStable(byScore, func(i, j int) bool {
		if byScore[i].Score != byScore[j].Score {
			return byScore[i].Score > byScore[j].Score
		}
		return byScore[i].StartUS < byScore[j].StartUS
	})
	selected := make([]Impact, 0, len(byScore))
	for _, candidate := range byScore {
		tooClose := false
		for _, prior := range selected {
			if absoluteDifference(candidate.StartUS, prior.StartUS) < uint64(options.MinDistanceUS) {
				tooClose = true
				break
			}
		}
		if tooClose {
			continue
		}
		selected = append(selected, candidate)
		if options.MaxPeaks > 0 && len(selected) == options.MaxPeaks {
			break
		}
	}
	sort.SliceStable(selected, func(i, j int) bool {
		if selected[i].StartUS != selected[j].StartUS {
			return selected[i].StartUS < selected[j].StartUS
		}
		return selected[i].SentenceIndex < selected[j].SentenceIndex
	})
	return selected, nil
}

var (
	ErrInvalidPeakOptions = errors.New("semantic peak selection: invalid options")
	ErrInvalidPeakInput   = errors.New("semantic peak selection: invalid score or timestamp")
)

func invalidPeak(index int) error {
	return fmt.Errorf("%w at index %d", ErrInvalidPeakInput, index)
}

func percentileThreshold(scores []Impact, percentile float64) float64 {
	if percentile == 0 {
		return 0
	}
	values := make([]float64, len(scores))
	for i, score := range scores {
		values[i] = score.Score
	}
	sort.Float64s(values)
	index := int(math.Ceil(percentile/100*float64(len(values)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(values) {
		index = len(values) - 1
	}
	return values[index]
}

func absoluteDifference(a, b int64) uint64 {
	if a >= b {
		return uint64(a) - uint64(b)
	}
	return uint64(b) - uint64(a)
}
