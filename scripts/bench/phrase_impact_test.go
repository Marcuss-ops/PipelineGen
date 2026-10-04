package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRunScoresUntimedSentencesAndReportsStageTimings(t *testing.T) {
	input := `{"action":"score","sentences":[{"text":"first","embedding":[1,0]},{"text":"second","embedding":[0.9,0.1]},{"text":"different","embedding":[0,1]}]}`
	var output bytes.Buffer
	if err := run(strings.NewReader(input), &output); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	var got struct {
		Scores []struct {
			Index  int     `json:"index"`
			Impact float64 `json:"impact"`
		} `json:"scores"`
		Timings struct {
			TotalMS float64 `json:"total_ms"`
		} `json:"timings"`
	}
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got.Scores) != 3 || got.Timings.TotalMS <= 0 {
		t.Fatalf("response = %+v, want three scores and nonzero total timing", got)
	}
	for i, score := range got.Scores {
		if score.Index != i || score.Impact < 0 || score.Impact > 1 {
			t.Fatalf("scores[%d] = %+v, want source order and impact in [0,1]", i, score)
		}
	}
}

func TestRunDecodesSnakeCasePeakOptions(t *testing.T) {
	input := `{"action":"peaks","options":{"min_distance_us":5000000,"threshold_percentile":80,"max_peaks":1},"scores":[{"index":0,"text":"a","start_us":0,"end_us":1000000,"impact":0.1},{"index":1,"text":"b","start_us":6000000,"end_us":7000000,"impact":0.95},{"index":2,"text":"c","start_us":12000000,"end_us":13000000,"impact":0.1}]}`
	var output bytes.Buffer
	if err := run(strings.NewReader(input), &output); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	var got struct {
		Peaks []struct {
			Index int `json:"index"`
		} `json:"peaks"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Error != "" || len(got.Peaks) != 1 || got.Peaks[0].Index != 1 {
		t.Fatalf("response = %+v, want sentence 1 as sole peak: %s", got, output.String())
	}
}
