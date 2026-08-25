package autotag

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ai/vlm"
)

type fakeCommitter struct {
	asset *asset.Asset
	err   error
}

func (c *fakeCommitter) CommitAndIndex(ctx context.Context, req persistence.CommitRequest) (persistence.CommitResult, error) {
	c.asset = &asset.Asset{
		ID:       req.AssetID,
		Source:   asset.Source(req.Source),
		Tags:     append([]string(nil), req.Metadata.Tags...),
		Metadata: req.Metadata.ToMap(),
	}
	return persistence.CommitResult{}, c.err
}
func (c *fakeCommitter) CommitAsset(ctx context.Context, req persistence.AssetCommitRequest) (persistence.CommittedAsset, error) {
	return persistence.CommitResult{}, nil
}
func (c *fakeCommitter) CommitTx(ctx context.Context, tx persistence.Transaction, req persistence.CommitRequest) (persistence.CommitResult, error) {
	return persistence.CommitResult{}, nil
}

func newTestFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "test.mp4")
	if err := os.WriteFile(p, []byte("fake video content"), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return p
}

func TestTagAsset_SavesModelAndDuration(t *testing.T) {
	wantModel := "sidecar/model-v3"
	wantVersion := "sidecar-version-7"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := vlm.VisualTagResponse{
			Tags: vlm.VisualTag{
				SceneType:      "urban landscape",
				VisualObjects:  []string{"skyline", "clouds"},
				Mood:           []string{"peaceful"},
				TextOnScreen:   []string{},
				DominantColors: []string{"orange"},
				Composition:    "wide shot",
				Lighting:       "sunset",
			},
			Model: wantModel,
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := vlm.NewClient(vlm.Config{
		Enabled:      true,
		Endpoint:     server.URL,
		Model:        "configured/model",
		ModelVersion: wantVersion,
		TimeoutMs:    5000,
	})

	dispatcher := &fakeCommitter{}
	svc := NewService(ServiceDeps{VLMClient: client, Committer: dispatcher, Log: zap.NewNop()})

	a := &asset.Asset{ID: "asset-1", MediaType: asset.MediaTypeClip}
	a.SetLocalPath(newTestFile(t))

	ctx := context.Background()
	if err := svc.TagAsset(ctx, a); err != nil {
		t.Fatalf("TagAsset() error = %v", err)
	}

	if dispatcher.asset == nil {
		t.Fatal("expected dispatcher to receive asset")
	}

	m := dispatcher.asset.Metadata
	if got := m["vlm_model"]; got != wantModel {
		t.Errorf("vlm_model = %q, want %q", got, wantModel)
	}
	if got := m["vlm_model_version"]; got != wantVersion {
		t.Errorf("vlm_model_version = %q, want %q", got, wantVersion)
	}

	duration, ok := m["vlm_analysis_duration_ms"].(int)
	if !ok {
		t.Fatalf("vlm_analysis_duration_ms must be an int, got %T", m["vlm_analysis_duration_ms"])
	}
	if duration < 0 {
		t.Errorf("vlm_analysis_duration_ms must be non-negative, got %d", duration)
	}

	if got := m["vlm_tagged"]; got != "success" {
		t.Errorf("vlm_tagged = %q, want success", got)
	}

	wantTags := []string{"skyline", "clouds", "peaceful", "urban landscape", "sunset"}
	for _, tag := range wantTags {
		found := false
		for _, got := range dispatcher.asset.Tags {
			if got == tag {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected aggregated tag %q, got %v", tag, dispatcher.asset.Tags)
		}
	}
}

func TestTagAsset_FallsBackToConfiguredModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sidecar omits the model field.
		resp := map[string]any{
			"tags": map[string]any{
				"scene_type": "portrait",
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := vlm.NewClient(vlm.Config{
		Enabled:      true,
		Endpoint:     server.URL,
		Model:        "fallback/model",
		ModelVersion: "fallback-v1",
		TimeoutMs:    5000,
	})

	dispatcher := &fakeCommitter{}
	svc := NewService(ServiceDeps{VLMClient: client, Committer: dispatcher, Log: zap.NewNop()})

	a := &asset.Asset{ID: "asset-2", MediaType: asset.MediaTypeImage}
	a.SetLocalPath(newTestFile(t))

	if err := svc.TagAsset(context.Background(), a); err != nil {
		t.Fatalf("TagAsset() error = %v", err)
	}

	if dispatcher.asset == nil {
		t.Fatal("expected dispatcher to receive asset")
	}
	m := dispatcher.asset.Metadata
	if got := m["vlm_model"]; got != "fallback/model" {
		t.Errorf("vlm_model = %q, want %q", got, "fallback/model")
	}
	if got := m["vlm_model_version"]; got != "fallback-v1" {
		t.Errorf("vlm_model_version = %q, want %q", got, "fallback-v1")
	}
}
