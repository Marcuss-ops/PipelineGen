package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
)

type personImageScorerOnceFake struct {
	onceCalls int
	chatCalls int
}

func (f *personImageScorerOnceFake) Model() string { return "vision-test" }

func (f *personImageScorerOnceFake) Chat(context.Context, []types.Message, map[string]any, json.RawMessage) (string, error) {
	f.chatCalls++
	return "", errors.New("Chat must not be used when ChatOnce is available")
}

func (f *personImageScorerOnceFake) ChatOnce(context.Context, []types.Message, map[string]any, json.RawMessage) (string, error) {
	f.onceCalls++
	return `{"identity_match":true,"single_person_headshot":true,"split_panel_or_collage":false,"confidence":0.93}`, nil
}

func TestOllamaPersonImageScorerUsesChatOnce(t *testing.T) {
	fake := &personImageScorerOnceFake{}
	scorer := NewOllamaPersonImageScorer(fake)
	scorer.download = func(context.Context, scriptpkg.SegmentAssetCandidate) ([]byte, string, error) {
		return testPersonScorerJPEG(t), "image/jpeg", nil
	}

	got, err := scorer.ScoreImageCandidates(context.Background(), "Chiara Poggi", []scriptpkg.SegmentAssetCandidate{{
		Provider:  scriptpkg.VidRushProviderInternetImages,
		Entity:    "Chiara Poggi",
		SourceURL: "https://images.example/chiara-poggi.jpg",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if fake.onceCalls != 1 || fake.chatCalls != 0 {
		t.Fatalf("chat_once=%d chat=%d, want one ChatOnce and zero Chat calls", fake.onceCalls, fake.chatCalls)
	}
	if got[0].SemanticStatus != "accepted" {
		t.Fatalf("verdict status = %q, want accepted", got[0].SemanticStatus)
	}
}
