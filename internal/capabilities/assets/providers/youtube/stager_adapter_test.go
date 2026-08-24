package youtube

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
)

func TestYouTubeStager_Prepare_NilAdapter(t *testing.T) {
	s := NewYouTubeStager(nil)
	got, err := s.Prepare(context.Background(), acquisition.PrepareRequest{
		Source: acquisition.SourceRef{URL: "https://youtu.be/dQw4w9WgXcQ"},
	})
	if got != nil {
		t.Fatalf("expected nil receipt, got %+v", got)
	}
	if !errors.Is(err, ErrYoutubeStagerNotWired) {
		t.Fatalf("expected ErrYoutubeStagerNotWired, got %v", err)
	}
}

func TestYouTubeStager_Prepare_EmptyURL(t *testing.T) {
	s := NewYouTubeStager(&Adapter{})
	_, err := s.Prepare(context.Background(), acquisition.PrepareRequest{})
	if !errors.Is(err, ErrYoutubeStagerEmptyURL) {
		t.Fatalf("expected ErrYoutubeStagerEmptyURL, got %v", err)
	}
}

func TestYouTubeStager_Release_InvalidAndAlreadyReleased(t *testing.T) {
	s := NewYouTubeStager(nil)
	if err := s.Release(context.Background(), ""); !errors.Is(err, acquisition.ErrAcquisitionInvalidToken) {
		t.Fatalf("empty token: expected ErrAcquisitionInvalidToken, got %v", err)
	}
	if err := s.Release(context.Background(), "unknown"); !errors.Is(err, acquisition.ErrAcquisitionInvalidToken) {
		t.Fatalf("unknown token: expected ErrAcquisitionInvalidToken, got %v", err)
	}
}

func TestYouTubeStager_ParseDownloadSection(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantStart time.Duration
		wantEnd   time.Duration
		wantErr   bool
	}{
		{name: "happy_3component", input: "00:01:20-00:01:35", wantStart: 80 * time.Second, wantEnd: 95 * time.Second},
		{name: "happy_2component", input: "01:30-02:45", wantStart: 90 * time.Second, wantEnd: 165 * time.Second},
		{name: "empty_returns_zeros", input: ""},
		{name: "malformed_no_dash", input: "00:01:20", wantErr: true},
		{name: "malformed_garbage", input: "not-a-section", wantErr: true},
		{name: "permissive_mixed_width", input: "00:01:20-02:45", wantStart: 80 * time.Second, wantEnd: 165 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotStart, gotEnd, err := parseDownloadSection(tc.input)
			if tc.wantErr {
				if err == nil || !errors.Is(err, ErrYoutubeStagerInvalidSection) {
					t.Fatalf("expected invalid-section error, got start=%v end=%v err=%v", gotStart, gotEnd, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotStart != tc.wantStart || gotEnd != tc.wantEnd {
				t.Fatalf("got %v-%v, want %v-%v", gotStart, gotEnd, tc.wantStart, tc.wantEnd)
			}
		})
	}
}
