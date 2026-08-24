package assets

import (
	"testing"
)

func TestExpandRoundIndexing(t *testing.T) {
	tests := []struct {
		name          string
		rounds        []RoundIndexSpec
		clipDuration  int
		expectClips   int
		expectError   bool
		expectedStart float64
		expectedEnd   float64
	}{
		{
			name: "valid integer round, simple duration",
			rounds: []RoundIndexSpec{
				{
					Round:          1,
					TimestampStart: "00:00:10",
					TimestampEnd:   "00:00:20",
					Description:    "Test round 1",
				},
			},
			clipDuration:  5,
			expectClips:   2,
			expectedStart: 10,
			expectedEnd:   15,
		},
		{
			name: "valid float round, default duration",
			rounds: []RoundIndexSpec{
				{
					Round:          2.0,
					TimestampStart: "00:01:00",
					TimestampEnd:   "00:01:04",
				},
			},
			clipDuration:  0, // uses default duration (4s)
			expectClips:   1,
			expectedStart: 60,
			expectedEnd:   64,
		},
		{
			name: "valid string round, custom duration",
			rounds: []RoundIndexSpec{
				{
					Round:          "3",
					TimestampStart: "00:02:00",
					TimestampEnd:   "00:02:12",
				},
			},
			clipDuration:  6,
			expectClips:   2,
			expectedStart: 120,
			expectedEnd:   126,
		},
		{
			name: "invalid timestamp format",
			rounds: []RoundIndexSpec{
				{
					Round:          1,
					TimestampStart: "00:10",
					TimestampEnd:   "00:20",
				},
			},
			expectError: true,
		},
		{
			name: "invalid range",
			rounds: []RoundIndexSpec{
				{
					Round:          1,
					TimestampStart: "00:00:20",
					TimestampEnd:   "00:00:10",
				},
			},
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clips, err := ExpandRoundIndexing(tc.rounds, tc.clipDuration)
			if tc.expectError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(clips) != tc.expectClips {
				t.Errorf("expected %d clips, got %d", tc.expectClips, len(clips))
			}
			if tc.expectClips > 0 {
				if clips[0].StartSec != tc.expectedStart || clips[0].EndSec != tc.expectedEnd {
					t.Errorf("first clip: expected range [%f, %f], got [%f, %f]", tc.expectedStart, tc.expectedEnd, clips[0].StartSec, clips[0].EndSec)
				}
			}
		})
	}
}
