package texttracks

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

func TestSubtitleMaterializerPublishesAndPersistsPerClipDriveReference(t *testing.T) {
	repo := &subtitleArtifactRepoRecorder{}
	publisher := &subtitlePublisherRecorder{}
	materializer := NewSubtitleArtifactMaterializer(repo, t.TempDir(), publisher)
	cues := []asset.TimedCue{
		{StartMs: 0, EndMs: 3280, Text: "hello"},
		{StartMs: 3280, EndMs: 3289, Text: "short cue"},
	}

	for _, tc := range []struct {
		assetID string
		folder  string
	}{
		{assetID: "clip-a", folder: "drive-folder-a"},
		{assetID: "clip-b", folder: "drive-folder-b"},
	} {
		if _, err := materializer.Materialize(context.Background(), SubtitleMaterializerInput{
			AssetID:        tc.assetID,
			LanguageCode:   "en",
			TextTrackID:    10,
			ClipDurationMs: 4000,
			TimedCues:      cues,
			DriveFolderID:  tc.folder,
		}); err != nil {
			t.Fatalf("materialize %s: %v", tc.assetID, err)
		}
	}

	if len(publisher.requests) != 2 {
		t.Fatalf("publish calls = %d, want 2", len(publisher.requests))
	}
	for _, tc := range []struct {
		assetID string
		folder  string
		fileID  string
	}{
		{assetID: "clip-a", folder: "drive-folder-a", fileID: "drive-ass-clip-a"},
		{assetID: "clip-b", folder: "drive-folder-b", fileID: "drive-ass-clip-b"},
	} {
		req := publisher.byAsset[tc.assetID]
		if req.DestinationFolderID != tc.folder {
			t.Errorf("%s folder = %q, want %q", tc.assetID, req.DestinationFolderID, tc.folder)
		}
		if req.Filename != tc.assetID+".en.ass" {
			t.Errorf("%s filename = %q", tc.assetID, req.Filename)
		}
		got := repo.byAsset[tc.assetID]
		if got.DriveFileID != tc.fileID || got.DriveURL == "" || got.Status != asset.SubtitleStatusReady {
			t.Errorf("%s artifact Drive state = %+v", tc.assetID, got)
		}
		if filepath.Dir(got.LocalPath) == "" {
			t.Errorf("%s local ASS path is empty", tc.assetID)
		}
	}
}

type subtitleArtifactRepoRecorder struct {
	byAsset map[string]asset.SubtitleArtifact
}

func (r *subtitleArtifactRepoRecorder) Upsert(_ context.Context, artifact *asset.SubtitleArtifact) error {
	if r.byAsset == nil {
		r.byAsset = make(map[string]asset.SubtitleArtifact)
	}
	r.byAsset[artifact.AssetID] = *artifact
	return nil
}

func (r *subtitleArtifactRepoRecorder) FindCurrent(_ context.Context, assetID, _ string, _ asset.SubtitleFormat) (*asset.SubtitleArtifact, error) {
	artifact, ok := r.byAsset[assetID]
	if !ok {
		return nil, nil
	}
	return &artifact, nil
}

func (r *subtitleArtifactRepoRecorder) ListByAsset(_ context.Context, assetID string) ([]asset.SubtitleArtifact, error) {
	artifact, ok := r.byAsset[assetID]
	if !ok {
		return nil, nil
	}
	return []asset.SubtitleArtifact{artifact}, nil
}

type subtitlePublisherRecorder struct {
	requests []delivery.PublishRequest
	byAsset  map[string]delivery.PublishRequest
}

func (p *subtitlePublisherRecorder) Publish(_ context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	if p.byAsset == nil {
		p.byAsset = make(map[string]delivery.PublishRequest)
	}
	p.requests = append(p.requests, req)
	p.byAsset[req.AssetID] = req
	return &delivery.PublishResult{
		FileID:      "drive-ass-" + req.AssetID,
		WebViewLink: "https://drive.google.com/file/d/drive-ass-" + req.AssetID + "/view",
	}, nil
}

func (p *subtitlePublisherRecorder) ResolveFolder(context.Context, delivery.PublishRequest) (string, error) {
	return "", nil
}
