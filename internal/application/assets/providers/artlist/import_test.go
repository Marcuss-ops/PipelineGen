package artlist

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// fakeDetailFetcher is a test-only implementation of the DetailFetcher port.
type fakeDetailFetcher struct {
	candidate *Candidate
	err       error
	calledURL string
}

func (f *fakeDetailFetcher) FetchDetails(_ context.Context, clipPageURL string) (*Candidate, error) {
	f.calledURL = clipPageURL
	if f.err != nil {
		return nil, f.err
	}
	return f.candidate, nil
}

// fakeDispatcherForImport records SaveDiscoveredAsset calls.
type fakeDispatcherForImport struct {
	mu                  sync.Mutex
	saved               *asset.Asset
	saveDiscoveredCalls int
	saveDiscoveredErr   error
}

func (f *fakeDispatcherForImport) EnqueueAndIndex(_ context.Context, _ *asset.Asset, _ string) error {
	return nil
}

func (f *fakeDispatcherForImport) SaveDiscoveredAsset(_ context.Context, clip *asset.Asset, _ asset.LifecycleState, _ asset.IndexState) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saved = clip
	f.saveDiscoveredCalls++
	return f.saveDiscoveredErr
}

func (f *fakeDispatcherForImport) EnqueueAndRestore(_ context.Context, _ string) error { return nil }
func (f *fakeDispatcherForImport) EnqueueAndDelete(_ context.Context, _ string) error  { return nil }

func TestCandidateToAsset_MapsProviderMetadata(t *testing.T) {
	candidate := &Candidate{
		ID:           "123456",
		Title:        "Skyline at Sundown",
		Description:  "City skyline during sunset",
		Creator:      "John Richter",
		PageURL:      "https://artlist.io/stock-footage/clip/skyline-at-sundown/123456",
		SourceRef:    "https://cdn.artlist.io/123456.mp4",
		ThumbnailURL: "https://cdn.artlist.io/123456/thumb.jpg",
		PreviewURL:   "https://cdn.artlist.io/123456/preview.mp4",
		Keywords:     []string{"Skyline", "Evening", "Clouds"},
		Categories:   []string{"Cities", "Travel"},
		RawMetadata: map[string]any{
			"country":  "Spain",
			"location": "Barcelona",
		},
	}

	clip := candidateToAsset(candidate, candidate.PageURL)

	assert.Equal(t, "123456", clip.ID)
	assert.Equal(t, "Skyline at Sundown", clip.Name)
	assert.Equal(t, asset.Source("artlist"), clip.Source)
	assert.Equal(t, asset.MediaType("video"), clip.MediaType)
	assert.Equal(t, "https://artlist.io/stock-footage/clip/skyline-at-sundown/123456", clip.ClipPageURL)
	assert.Equal(t, "https://cdn.artlist.io/123456.mp4", clip.SourceURL)
	assert.Equal(t, "https://cdn.artlist.io/123456/thumb.jpg", clip.ThumbnailURL)
	assert.ElementsMatch(t, []string{"Skyline", "Evening", "Clouds"}, clip.Tags)
	assert.Contains(t, clip.Metadata, "creator")
	assert.Equal(t, "John Richter", clip.Metadata["creator"])
	assert.Equal(t, []string{"Skyline", "Evening", "Clouds"}, clip.Metadata["provider_tags"])
	assert.Equal(t, []string{"Cities", "Travel"}, clip.Metadata["provider_categories"])
	assert.Equal(t, "Spain", clip.Metadata["country"])
	assert.Equal(t, "Barcelona", clip.Metadata["location"])
	assert.Equal(t, "artlist", clip.Metadata["metadata_origin"])
	assert.Contains(t, clip.Metadata["description"], "sunset")
	assert.Equal(t, "https://cdn.artlist.io/123456/preview.mp4", clip.Metadata["preview_url"])
}

func TestImportClip_MetadataOnly(t *testing.T) {
	rec := &fakeDispatcherForImport{}
	svc := &Service{
		log:        zap.NewNop(),
		dispatcher: rec,
		detailFetcher: &fakeDetailFetcher{
			candidate: &Candidate{
				ID:        "123",
				Title:     "Forest",
				PageURL:   "https://artlist.io/clip/forest/123",
				SourceRef: "https://cdn.artlist.io/123.mp4",
				Keywords:  []string{"forest", "trees"},
			},
		},
	}

	resp, err := svc.ImportClip(context.Background(), &ImportClipRequest{
		ClipPageURL: "https://artlist.io/clip/forest/123",
		Download:    false,
	})

	require.NoError(t, err)
	assert.True(t, resp.OK)
	assert.Equal(t, "123", resp.ClipID)
	assert.Equal(t, "discovered", resp.Status)
	assert.Equal(t, "Forest", resp.Name)
	assert.Equal(t, 1, rec.saveDiscoveredCalls)
	require.NotNil(t, rec.saved)
	assert.Equal(t, "123", rec.saved.ID)
	assert.Equal(t, asset.Source("artlist"), rec.saved.Source)
}

func TestImportClip_RequiresClipPageURL(t *testing.T) {
	svc := &Service{
		log:           zap.NewNop(),
		detailFetcher: &fakeDetailFetcher{},
	}

	_, err := svc.ImportClip(context.Background(), &ImportClipRequest{ClipPageURL: ""})
	assert.ErrorIs(t, err, ErrEmpty)
}

func TestImportClip_DetailFetcherUnavailable(t *testing.T) {
	svc := &Service{
		log: zap.NewNop(),
	}

	_, err := svc.ImportClip(context.Background(), &ImportClipRequest{ClipPageURL: "https://artlist.io/clip/123"})
	assert.ErrorIs(t, err, ErrUnavailable)
}

func TestStringSliceFromMetadata(t *testing.T) {
	assert.Equal(t, []string{"a", "b"}, stringSliceFromMetadata(map[string]any{"k": []string{"a", "b"}}, "k"))
	assert.Equal(t, []string{"a", "b"}, stringSliceFromMetadata(map[string]any{"k": []any{"a", "b"}}, "k"))
	assert.Equal(t, []string{"a", "b"}, stringSliceFromMetadata(map[string]any{"k": []any{"a", 1, "b"}}, "k"))
	assert.Nil(t, stringSliceFromMetadata(map[string]any{"k": "not-a-slice"}, "k"))
	assert.Nil(t, stringSliceFromMetadata(map[string]any{}, "missing"))
	assert.Nil(t, stringSliceFromMetadata(nil, "missing"))
}
