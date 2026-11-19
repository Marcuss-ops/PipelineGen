package wiring

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

func TestIsAIImageIngestSource(t *testing.T) {
	if !isAIImageIngestSource(&ingest.Request{Source: "google-slides"}) {
		t.Fatal("expected google-slides to be treated as AI image source")
	}
	if !isAIImageIngestSource(&ingest.Request{Source: "google-vids"}) {
		t.Fatal("expected google-vids to be treated as AI image source")
	}
	if !isAIImageIngestSource(&ingest.Request{Source: "google-vids-image"}) {
		t.Fatal("expected google-vids-image to be treated as AI image source")
	}
	if !isAIImageIngestSource(&ingest.Request{Source: "google-flow"}) {
		t.Fatal("expected google-flow to be treated as AI image source")
	}
	if !isAIImageIngestSource(&ingest.Request{Source: "local-nim"}) {
		t.Fatal("expected local-nim to be treated as AI image source")
	}
	if isAIImageIngestSource(&ingest.Request{Source: "duckduckgo"}) {
		t.Fatal("expected duckduckgo to stay in web image root")
	}
}

// isAIImageIngestSource is a test fixture for the ingest routing table; it had
// no production caller (TICKET-APP-WIRING-TEST-ONLY-HELPERS-2026-11-19).
func isAIImageIngestSource(req *ingest.Request) bool {
	if req == nil {
		return false
	}
	return detail.IsAIImageSource(req.Source)
}
