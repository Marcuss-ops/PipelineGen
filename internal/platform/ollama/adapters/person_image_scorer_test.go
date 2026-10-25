package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
)

type personImageScorerFakeClient struct {
	responses []string
	models    []string
	messages  [][]types.Message
}

func (f *personImageScorerFakeClient) Model() string { return "gemma4:e4b" }

func (f *personImageScorerFakeClient) Chat(_ context.Context, messages []types.Message, options map[string]any, format json.RawMessage) (string, error) {
	f.messages = append(f.messages, messages)
	f.models = append(f.models, options["model"].(string))
	if string(format) != `"json"` {
		return "", errUnexpectedImageScorerFormat
	}
	response := f.responses[0]
	f.responses = f.responses[1:]
	return response, nil
}

func TestOllamaPersonImageScorerRejectsCollageAndKeepsSinglePortrait(t *testing.T) {
	client := &personImageScorerFakeClient{responses: []string{
		`{"single_person_headshot":true,"split_panel_or_collage":false,"confidence":0.94}`,
		`{"single_person_headshot":false,"split_panel_or_collage":true,"confidence":0.97}`,
	}}
	scorer := NewOllamaPersonImageScorer(client)
	imageBytes := testPersonScorerJPEG(t)
	scorer.download = func(context.Context, scriptpkg.SegmentAssetCandidate) ([]byte, string, error) {
		return imageBytes, "image/jpeg", nil
	}
	candidates := []scriptpkg.SegmentAssetCandidate{
		{Provider: scriptpkg.VidRushProviderInternetImages, Entity: "Chiara Poggi", SourceURL: "https://images.example/chiara-poggi-portrait.jpg"},
		{Provider: scriptpkg.VidRushProviderInternetImages, Entity: "Chiara Poggi", SourceURL: "https://images.example/chiara-poggi-collage.jpg"},
		{Provider: scriptpkg.VidRushProviderInternetImages, SourceURL: "https://images.example/generic.jpg"},
	}

	got, err := scorer.ScoreImageCandidates(context.Background(), "Chiara Poggi", candidates)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].SemanticStatus != "accepted" || got[0].SemanticScore != 0.94 {
		t.Fatalf("verified portrait verdict = (%q, %v), want accepted at 0.94", got[0].SemanticStatus, got[0].SemanticScore)
	}
	if got[1].SemanticStatus != "rejected" || !strings.Contains(got[1].QualityReason, "collage") {
		t.Fatalf("collage verdict = (%q, %q), want rejected with collage reason", got[1].SemanticStatus, got[1].QualityReason)
	}
	if got[2].SemanticStatus != "" {
		t.Fatalf("generic image was scored as a person image: %+v", got[2])
	}
	if len(client.models) != 2 || client.models[0] != "gemma4:e4b" || client.models[1] != "gemma4:e4b" {
		t.Fatalf("requested models = %v, want configured Ollama model for each candidate", client.models)
	}
	if len(client.messages[0]) != 2 || len(client.messages[0][1].Images) != 1 || len(client.messages[0][1].Images[0]) == 0 {
		t.Fatalf("Ollama did not receive image pixels: %+v", client.messages[0])
	}
	prompt := client.messages[0][1].Content
	if !strings.Contains(prompt, "full-body") || !strings.Contains(prompt, "background people") || !strings.Contains(prompt, "not co-primary subjects") {
		t.Fatalf("image layout prompt still excludes contextual single-subject news photos: %q", prompt)
	}
}

func TestOllamaPersonImageScorerSkipsDefinitiveVerdicts(t *testing.T) {
	client := &personImageScorerFakeClient{}
	scorer := NewOllamaPersonImageScorer(client)
	downloads := 0
	scorer.download = func(context.Context, scriptpkg.SegmentAssetCandidate) ([]byte, string, error) {
		downloads++
		return testPersonScorerJPEG(t), "image/jpeg", nil
	}
	accepted := scriptpkg.SegmentAssetCandidate{
		Provider: scriptpkg.VidRushProviderInternetImages, Entity: "Chiara Poggi",
		SourceURL:      "https://images.example/chiara-poggi-portrait.jpg",
		SemanticStatus: "accepted", SemanticScore: 0.94,
		QualityReason: "Ollama visual verification confirmed one dominant subject",
	}
	rejected := scriptpkg.SegmentAssetCandidate{
		Provider: scriptpkg.VidRushProviderInternetImages, Entity: "Chiara Poggi",
		SourceURL:      "https://images.example/chiara-poggi-collage.jpg",
		SemanticStatus: "rejected",
		QualityReason:  "Ollama visual verification detected a split panel or collage",
	}

	got, err := scorer.ScoreImageCandidates(context.Background(), "Chiara Poggi", []scriptpkg.SegmentAssetCandidate{accepted, rejected})
	if err != nil {
		t.Fatal(err)
	}
	if downloads != 0 || len(client.messages) != 0 {
		t.Fatalf("downloads=%d vlm_calls=%d, want cached verdicts reused", downloads, len(client.messages))
	}
	if got[0].SemanticStatus != "accepted" || got[1].SemanticStatus != "rejected" {
		t.Fatalf("cached verdicts were rewritten: %+v", got)
	}
}

func TestOllamaPersonImageScorerRetriesTransientRejection(t *testing.T) {
	client := &personImageScorerFakeClient{responses: []string{
		`{"single_person_headshot":true,"split_panel_or_collage":false,"confidence":0.94}`,
	}}
	scorer := NewOllamaPersonImageScorer(client)
	downloads := 0
	scorer.download = func(context.Context, scriptpkg.SegmentAssetCandidate) ([]byte, string, error) {
		downloads++
		return testPersonScorerJPEG(t), "image/jpeg", nil
	}
	transient := scriptpkg.SegmentAssetCandidate{
		Provider: scriptpkg.VidRushProviderInternetImages, Entity: "Chiara Poggi",
		SourceURL:      "https://images.example/chiara-poggi-transient.jpg",
		SemanticStatus: "rejected",
		QualityReason:  "visual verification failed: context deadline exceeded",
	}

	got, err := scorer.ScoreImageCandidates(context.Background(), "Chiara Poggi", []scriptpkg.SegmentAssetCandidate{transient})
	if err != nil {
		t.Fatal(err)
	}
	if downloads != 1 || len(client.messages) != 1 {
		t.Fatalf("downloads=%d vlm_calls=%d, want one retry for a transient rejection", downloads, len(client.messages))
	}
	if got[0].SemanticStatus != "accepted" || got[0].SemanticScore != 0.94 {
		t.Fatalf("retried verdict = (%q, %v), want accepted at 0.94", got[0].SemanticStatus, got[0].SemanticScore)
	}
}

func TestNormalizePersonImageForVisionConvertsPNGToJPEG(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 10), G: uint8(y * 10), A: 255})
		}
	}
	var source bytes.Buffer
	if err := png.Encode(&source, img); err != nil {
		t.Fatal(err)
	}
	got, err := normalizePersonImageForVision(source.Bytes())
	if err != nil {
		t.Fatalf("normalize image for Ollama: %v", err)
	}
	if !bytes.HasPrefix(got, []byte{0xff, 0xd8, 0xff}) {
		t.Fatalf("normalized bytes lack JPEG signature: %x", got[:min(3, len(got))])
	}
	if _, err := jpeg.Decode(bytes.NewReader(got)); err != nil {
		t.Fatalf("normalized bytes are not a decodable JPEG: %v", err)
	}
}

func testPersonScorerJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 640, 360))
	for y := 0; y < 360; y++ {
		for x := 0; x < 640; x++ {
			img.Set(x, y, color.RGBA{R: 90, G: 120, B: 160, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

var errUnexpectedImageScorerFormat = &imageScorerFormatError{}

type imageScorerFormatError struct{}

func (*imageScorerFormatError) Error() string { return "expected JSON output mode" }
