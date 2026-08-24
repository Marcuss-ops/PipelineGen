package stockplan

import (
	"encoding/json"
	"math"
	"testing"
)

func TestYouTubeAcquisitionContracts(t *testing.T) {
	v, e := ParseYouTubeURL("https://youtu.be/abcDEF_123?si=x")
	if e != nil || v.ID != "abcDEF_123" {
		t.Fatal(v, e)
	}
	if _, e = ParseYouTubeURL("https://www.youtube.com/playlist?list=x"); e == nil {
		t.Fatal("playlist accepted")
	}
	s := NewHighlightSelector(DefaultHighlightWeights())
	got := s.Select([]HighlightCandidate{{0, 7000, 0, "generic intro", nil, 0, 0, 0, ""}, {30000, 37000, 0, "Mike Tyson enters the boxing ring for training.", nil, 0, 0, 0, ""}, {90000, 97000, 0, "Mike Tyson trains intensely.", nil, 0, 0, 0, ""}}, "Mike Tyson training ring", "Mike Tyson", 2, 3000)
	if len(got) != 2 || got[0].DurationMs != 7000 || got[1].StartMs != 90000 {
		t.Fatalf("%+v", got)
	}
	p := PartialDownloadPlan{"abcDEF_123", 120000, 127000, 7000, "stock-video-v1"}
	if e = p.Validate(); e != nil || p.YTDLPSection() != "*120.000-127.000" || p.CacheKey() == "" {
		t.Fatal(e, p)
	}
	if _, ok := NewHighlightRegistry().Resolve("youtube"); !ok {
		t.Fatal("selector missing")
	}
	seg := NewTranscriptSegmenter().Segment([]TranscriptCue{{0, 10000, "intro"}, {30000, 45000, "Mike Tyson enters the ring"}}, 30000, 5000)
	if len(seg) == 0 || seg[0].DurationMs != 30000 {
		t.Fatal(seg)
	}
}

func TestDefaultResolverRegistersYouTubeSelector(t *testing.T) {
	r := NewDefaultResolver().(*defaultResolver)
	if _, ok := r.HighlightSelector("youtube"); !ok {
		t.Fatal("youtube selector missing from resolver")
	}
}

func TestYouTubeStockJSONRoundTripPreservesTranscriptContract(t *testing.T) {
	req := YouTubeStockRequest{Subject: "Mike Tyson", Query: "Mike Tyson ring training", YouTubeURLs: []string{"https://www.youtube.com/watch?v=abcDEF_123"}, ClipsPerVideo: 3, ClipDurationMs: 7000}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var decoded YouTubeStockRequest
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := decoded.Validate(); err != nil || decoded.ClipDurationMs != 7000 {
		t.Fatalf("request=%+v err=%v", decoded, err)
	}
	segment := SelectedSegment{YouTubeVideoID: "abcDEF_123", SourceURL: "https://www.youtube.com/watch?v=abcDEF_123", StartMs: 120000, EndMs: 127000, DurationMs: 7000, Transcript: "Testo del segmento", RelevanceScore: .91, SelectionReason: "Relevant discussion of ring training", SelectionBasis: "transcript", VisualVerified: false, CacheKey: "deterministic-key", Status: "SEGMENTS_PLANNED"}
	if err := segment.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(segment)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip SelectedSegment
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if err := roundTrip.Validate(); err != nil {
		t.Fatal(err)
	}
	if roundTrip.DurationMs != roundTrip.EndMs-roundTrip.StartMs || roundTrip.SelectionBasis != "transcript" || roundTrip.VisualVerified {
		t.Fatalf("round trip=%+v", roundTrip)
	}
	segment.RelevanceScore = math.NaN()
	if err := segment.Validate(); err == nil {
		t.Fatal("NaN score accepted")
	}
}
