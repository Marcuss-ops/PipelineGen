package wiring

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	imagesapp "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"
	imageadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	ollamaadapters "github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/adapters"
	ollamaclient "github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/client"
	"go.uber.org/zap"
)

// TestPersonImageE2ELiveBattery runs the real-world T01-T10 entity retrieval
// and VLM identity verification battery against live Wikipedia/DDG/SearXNG and
// local Ollama Vision.
//
// Run with:
//
//	PERSON_E2E_LIVE=1 go test ./internal/app/wiring -run '^TestPersonImageE2ELiveBattery$' -v -count=1 -timeout 15m
func TestPersonImageE2ELiveBattery(t *testing.T) {
	if os.Getenv("PERSON_E2E_LIVE") != "1" {
		t.Skip("set PERSON_E2E_LIVE=1 to run the live person image E2E battery")
	}

	ollamaURL := strings.TrimSpace(os.Getenv("OLLAMA_ADDR"))
	if ollamaURL == "" {
		ollamaURL = "http://127.0.0.1:11434"
	}
	visionModel := strings.TrimSpace(os.Getenv("OLLAMA_VISION_MODEL"))
	if visionModel == "" {
		visionModel = "gemma4:e4b"
	}
	searxURL := strings.TrimSpace(os.Getenv("SEARXNG_URL"))
	if searxURL == "" {
		searxURL = "http://127.0.0.1:8080"
	}

	log := zap.NewNop()
	cfg := &config.Config{}
	cfg.External.SearxngURL = searxURL
	service := imagesapp.NewService(imagesapp.ImagesDeps{
		Core:     imagesapp.ImagesCoreDeps{Cfg: cfg, Log: log},
		External: imagesapp.ImagesExternalDeps{RemoteFetch: &http.Client{Timeout: 20 * time.Second}},
	})
	resolver, err := imagesapp.NewImageSearchResolver(
		imagesapp.WithRetrievalBackend(service.RetrievalRegistry()),
		imagesapp.WithImageListRepository(liveEmptyImageRepo{}),
	)
	if err != nil {
		t.Fatalf("image search resolver: %v", err)
	}
	searcher := newInternetImageSearchAdapter(resolver, log)
	skipVLM := os.Getenv("PERSON_E2E_SKIP_VLM") == "1"
	visionClient := ollamaclient.NewClient(ollamaURL, visionModel, 90)
	scorer := ollamaadapters.NewOllamaPersonImageScorer(visionClient)

	cases := []struct {
		id     string
		entity string
		query  string
		class  string
	}{
		{"T01", "Mike Tyson", "Mike Tyson 1991", "PERSON"},
		{"T02a", "Chiara Poggi", "Chiara Poggi", "PERSON"},
		{"T02b", "Alberto Stasi", "Alberto Stasi", "PERSON"},
		{"T03", "Michael Burry", "Michael Burry investor", "PERSON"},
		{"T04", "George Washington", "George Washington", "PERSON"},
		{"T05a", "José María Aznar", "José María Aznar", "PERSON"},
		{"T05b", "Conan O'Brien", "Conan O'Brien", "PERSON"},
		{"T05c", "Michael B. Jordan", "Michael B. Jordan", "PERSON"},
		{"T06a", "Bruce Lee", "Bruce Lee", "PERSON"},
		{"T06b", "Hayao Miyazaki", "Hayao Miyazaki", "PERSON"},
		{"T07", "Elon Musk", "Elon Musk", "PERSON"},
		{"T09", "Gianluca Ferretti", "Gianluca Ferretti ingegnere", "PERSON"},
		{"T10", "Steve Jobs", "Steve Jobs Stanford 2005", "PERSON"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	for _, tc := range cases {
		t.Run(tc.id+"_"+tc.entity, func(t *testing.T) {
			candidates, searchErr := searcher.SearchImages(ctx, imageadapters.InternetImageSearchRequest{
				SegmentID:    "e2e-" + tc.id,
				Query:        tc.query,
				Entity:       tc.entity,
				QueryClass:   tc.class,
				Language:     "en",
				Limit:        8,
				ForceRefresh: true,
			})
			if searchErr != nil {
				t.Logf("[%s] search error: %v", tc.id, searchErr)
			}
			t.Logf("[%s] entity=%q query=%q discovered=%d", tc.id, tc.entity, tc.query, len(candidates))
			for i, c := range candidates {
				if i >= 5 {
					break
				}
				t.Logf("  cand[%d] provider=%s url=%s", i, c.Provider, c.SourceURL)
			}

			if skipVLM {
				verifiedOK := 0
				for i, c := range candidates {
					if i >= 6 {
						break
					}
					data, mime, dlErr := imageadapters.DownloadVidRushImageForCandidate(ctx, http.DefaultClient, c, imageadapters.DefaultVidRushImagePolicy())
					if dlErr != nil {
						t.Logf("  DL_FAIL[%d] provider=%s err=%v url=%s", i, c.Provider, dlErr, c.SourceURL)
						continue
					}
					v, vErr := imageadapters.VerifyVidRushImageBytes(c, data, mime, imageadapters.DefaultVidRushImagePolicy())
					if vErr != nil {
						t.Logf("  VERIFY_FAIL[%d] provider=%s err=%v url=%s", i, c.Provider, vErr, c.SourceURL)
						continue
					}
					verifiedOK++
					t.Logf("  VERIFIED[%d] provider=%s %dx%d mime=%s url=%s", i, c.Provider, v.Width, v.Height, v.MIMEType, c.SourceURL)
				}
				t.Logf("[%s] summary (no-VLM): discovered=%d verified_ok=%d", tc.id, len(candidates), verifiedOK)
				return
			}

			scored, scoreErr := scorer.ScoreImageCandidates(ctx, tc.query, candidates)
			if scoreErr != nil {
				t.Logf("[%s] scorer error: %v", tc.id, scoreErr)
			}
			accepted := 0
			rejected := 0
			unknown := 0
			for i, c := range scored {
				switch strings.ToLower(c.SemanticStatus) {
				case "accepted":
					accepted++
					t.Logf("  ACCEPTED[%d] provider=%s score=%.2f reason=%q url=%s", i, c.Provider, c.SemanticScore, c.QualityReason, c.SourceURL)
				case "rejected":
					rejected++
					if rejected <= 4 {
						t.Logf("  REJECTED[%d] provider=%s reason=%q url=%s", i, c.Provider, c.QualityReason, c.SourceURL)
					}
				default:
					unknown++
				}
			}
			t.Logf("[%s] summary: discovered=%d accepted=%d rejected=%d unknown=%d", tc.id, len(candidates), accepted, rejected, unknown)
			if tc.id == "T09" && accepted > 0 {
				t.Errorf("[%s] expected 0 accepted for non-public person Gianluca Ferretti, got %d", tc.id, accepted)
			}
		})
	}
	_ = scriptpkg.VidRushProviderInternetImages
}
