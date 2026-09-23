package veloxclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestClipTranscript_CallsCanonicalRouteAndDecodes pins the transcript
// client arm: the request MUST hit RouteClipsTranscript with url + the
// non-zero window params, and the payload must decode losslessly.
func TestClipTranscript_CallsCanonicalRouteAndDecodes(t *testing.T) {
	const wantURL = "https://www.youtube.com/watch?v=506AyzC7d-k"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != RouteClipsTranscript {
			t.Errorf("path = %s, want %s", r.URL.Path, RouteClipsTranscript)
		}
		q := r.URL.Query()
		if got := q.Get("url"); got != wantURL {
			t.Errorf("url query = %q, want %q", got, wantURL)
		}
		if got := q.Get("start"); got != "60" {
			t.Errorf("start = %q, want \"60\"", got)
		}
		if got := q.Get("end"); got != "180" {
			t.Errorf("end = %q, want \"180\"", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Transcript{
			OK: true, VideoID: "506AyzC7d-k", Language: "it",
			SourceType: "youtube_subtitle", IsOriginal: true, Provider: "yt-dlp",
			Text:     "Ciao, sono Venanzio.",
			Cues:     []TranscriptCue{{StartMs: 1000, EndMs: 2400, Text: "Ciao, sono Venanzio."}},
			CueCount: 1,
		})
	}))
	defer server.Close()

	c := New(server.URL, "")
	tr, err := c.ClipTranscript(context.Background(), wantURL, 60, 180)
	if err != nil {
		t.Fatalf("ClipTranscript: %v", err)
	}
	if !tr.OK || tr.VideoID != "506AyzC7d-k" || tr.Language != "it" {
		t.Errorf("unexpected transcript: %+v", tr)
	}
	if tr.CueCount != 1 || len(tr.Cues) != 1 || tr.Cues[0].StartMs != 1000 {
		t.Errorf("cues did not round-trip: %+v", tr.Cues)
	}
	if !tr.IsOriginal || tr.SourceType != "youtube_subtitle" {
		t.Errorf("provenance lost in decode: %+v", tr)
	}
}

// TestClipTranscript_OmitsZeroWindow pins the whole-video default:
// start/end at 0 are NOT sent, so the server applies its 0/0 meaning.
func TestClipTranscript_OmitsZeroWindow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if _, ok := q["start"]; ok {
			t.Errorf("start must be omitted when zero, got %q", q.Get("start"))
		}
		if _, ok := q["end"]; ok {
			t.Errorf("end must be omitted when zero, got %q", q.Get("end"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"text":"full"}`))
	}))
	defer server.Close()

	c := New(server.URL, "")
	if _, err := c.ClipTranscript(context.Background(), "https://youtu.be/abc12345678", 0, 0); err != nil {
		t.Fatalf("ClipTranscript: %v", err)
	}
}

// TestClipTranscript_EmptyURLFailsClientSide pins the input guard: an
// empty url never reaches the wire.
func TestClipTranscript_EmptyURLFailsClientSide(t *testing.T) {
	c := New("http://127.0.0.1:0", "")
	if _, err := c.ClipTranscript(context.Background(), " ", 0, 0); err == nil {
		t.Fatal("empty url must fail client-side")
	}
}
