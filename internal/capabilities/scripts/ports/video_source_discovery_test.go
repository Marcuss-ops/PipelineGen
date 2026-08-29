package ports

import (
	"encoding/json"
	"testing"
)

func TestVideoSourceCandidateValidate(t *testing.T) {
	valid := VideoSourceCandidate{Provider: "youtube", VideoID: "abc", URL: "https://youtube.com/watch?v=abc", MetadataScore: 0.8}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid candidate rejected: %v", err)
	}

	cases := []VideoSourceCandidate{
		{VideoID: "abc", URL: "https://example.com"},
		{Provider: "youtube", URL: "https://example.com"},
		{Provider: "youtube", VideoID: "abc"},
		{Provider: "youtube", VideoID: "abc", URL: "https://example.com", MetadataScore: 1.1},
	}
	for i, candidate := range cases {
		if err := candidate.Validate(); err == nil {
			t.Errorf("case %d unexpectedly accepted", i)
		}
	}
}

func TestVideoSourceDiscoveryRequestJSONRoundTrip(t *testing.T) {
	request := VideoSourceDiscoveryRequest{
		SegmentID: "segment-1", Queries: []string{"focused query"}, Language: "en",
		MaxVideos: 12, MinVideoDurationMs: 7500, ExcludeLive: true,
	}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var decoded VideoSourceDiscoveryRequest
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SegmentID != request.SegmentID || decoded.MaxVideos != request.MaxVideos || !decoded.ExcludeLive {
		t.Fatalf("round-trip mismatch: got %+v", decoded)
	}
}
