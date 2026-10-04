package semantic

import (
	"errors"
	"reflect"
	"testing"
)

func TestSelectPeaksUsesLocalMaximaAndTemporalSuppression(t *testing.T) {
	scores := []Impact{
		{SentenceIndex: 0, StartUS: 0, EndUS: 1, Score: 0.20},
		{SentenceIndex: 1, StartUS: 2_000_000, EndUS: 3_000_000, Score: 0.95},
		{SentenceIndex: 2, StartUS: 4_000_000, EndUS: 5_000_000, Score: 0.30},
		{SentenceIndex: 3, StartUS: 6_000_000, EndUS: 7_000_000, Score: 0.90},
		{SentenceIndex: 4, StartUS: 10_000_000, EndUS: 11_000_000, Score: 0.25},
		{SentenceIndex: 5, StartUS: 12_000_000, EndUS: 13_000_000, Score: 0.88},
		{SentenceIndex: 6, StartUS: 14_000_000, EndUS: 15_000_000, Score: 0.20},
	}

	got, err := SelectPeaks(scores, PeakOptions{MinDistanceUS: 8_000_000, ThresholdPercentile: 70, MaxPeaks: 3})
	if err != nil {
		t.Fatalf("SelectPeaks() error = %v", err)
	}
	want := []Impact{scores[1], scores[5]}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SelectPeaks() = %+v, want sentence indexes [1, 5]", got)
	}
}

func TestSelectPeaksKeepsFirstPointOfAPlateau(t *testing.T) {
	scores := []Impact{
		{SentenceIndex: 0, StartUS: 0, EndUS: 1, Score: 0.1},
		{SentenceIndex: 1, StartUS: 1, EndUS: 2, Score: 0.9},
		{SentenceIndex: 2, StartUS: 2, EndUS: 3, Score: 0.9},
		{SentenceIndex: 3, StartUS: 3, EndUS: 4, Score: 0.2},
	}
	got, err := SelectPeaks(scores, PeakOptions{})
	if err != nil {
		t.Fatalf("SelectPeaks() error = %v", err)
	}
	if len(got) != 1 || got[0].SentenceIndex != 1 {
		t.Fatalf("SelectPeaks() = %+v, want only the plateau's first sentence", got)
	}
}

func TestSelectPeaksThresholdPercentileUsesNearestRank(t *testing.T) {
	scores := []Impact{
		{SentenceIndex: 0, StartUS: 0, EndUS: 1, Score: .1},
		{SentenceIndex: 1, StartUS: 2, EndUS: 3, Score: .2},
		{SentenceIndex: 2, StartUS: 4, EndUS: 5, Score: .9},
		{SentenceIndex: 3, StartUS: 6, EndUS: 7, Score: .1},
	}
	got, err := SelectPeaks(scores, PeakOptions{ThresholdPercentile: 75})
	if err != nil {
		t.Fatalf("SelectPeaks() error = %v", err)
	}
	if len(got) != 1 || got[0].SentenceIndex != 2 {
		t.Fatalf("SelectPeaks() = %+v, want only the peak meeting p75", got)
	}
}

func TestSelectPeaksHandlesBoundaryAndInvalidInputs(t *testing.T) {
	if got, err := SelectPeaks(nil, PeakOptions{}); err != nil || got != nil {
		t.Fatalf("SelectPeaks(nil) = %v, %v; want nil, nil", got, err)
	}
	short := []Impact{{StartUS: 0, EndUS: 1, Score: 1}, {StartUS: 2, EndUS: 3, Score: 0}}
	if got, err := SelectPeaks(short, PeakOptions{}); err != nil || len(got) != 0 {
		t.Fatalf("SelectPeaks(short) = %v, %v; want no peaks", got, err)
	}
	flat := []Impact{{StartUS: 0, EndUS: 1, Score: .4}, {StartUS: 2, EndUS: 3, Score: .4}, {StartUS: 4, EndUS: 5, Score: .4}}
	if got, err := SelectPeaks(flat, PeakOptions{}); err != nil || len(got) != 0 {
		t.Fatalf("SelectPeaks(flat) = %v, %v; want no fabricated peak on constant signal", got, err)
	}
	validScores := []Impact{
		{StartUS: 0, EndUS: 1, Score: 0.1},
		{StartUS: 2, EndUS: 3, Score: 0.9},
		{StartUS: 4, EndUS: 5, Score: 0.1},
	}
	if _, err := SelectPeaks(validScores, PeakOptions{ThresholdPercentile: 101}); !errors.Is(err, ErrInvalidPeakOptions) {
		t.Fatalf("invalid percentile error = %v, want ErrInvalidPeakOptions", err)
	}
	if _, err := SelectPeaks(validScores, PeakOptions{MinDistanceUS: -1}); !errors.Is(err, ErrInvalidPeakOptions) {
		t.Fatalf("negative gap error = %v, want ErrInvalidPeakOptions", err)
	}
	invalidScore := append([]Impact(nil), validScores...)
	invalidScore[1].Score = 1.1
	if _, err := SelectPeaks(invalidScore, PeakOptions{}); !errors.Is(err, ErrInvalidPeakInput) {
		t.Fatalf("invalid score error = %v, want ErrInvalidPeakInput", err)
	}
	invalidTime := append([]Impact(nil), validScores...)
	invalidTime[2].StartUS = 1
	invalidTime[2].EndUS = 1
	if _, err := SelectPeaks(invalidTime, PeakOptions{}); !errors.Is(err, ErrInvalidPeakInput) {
		t.Fatalf("invalid timestamp error = %v, want ErrInvalidPeakInput", err)
	}
}
