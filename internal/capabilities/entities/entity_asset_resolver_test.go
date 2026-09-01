package entities

import (
	"context"
	"testing"

	assetpersistence "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
)

type entityAssetSourceStub struct {
	calls int
	asset EntityAsset
}

func (s *entityAssetSourceStub) Acquire(context.Context, EntityAssetRequest) (EntityAsset, error) {
	s.calls++
	return s.asset, nil
}

type entityAssetCommitStub struct {
	calls   int
	request assetpersistence.AssetCommitRequest
}

func (s *entityAssetCommitStub) CommitAsset(_ context.Context, req assetpersistence.AssetCommitRequest) (assetpersistence.CommittedAsset, error) {
	s.calls++
	s.request = req
	return assetpersistence.CommittedAsset{}, nil
}

func TestEntityAssetResolverLocalHitDoesNotCallProviderOrCommitter(t *testing.T) {
	media := NewEntityMediaResolver()
	canonical := CanonicalEntityID("PERSON", "Gerard Butler")
	if err := media.index.IndexForCanonicalID(canonical, EntityAsset{AssetID: "asset-local", AssetType: "PHOTO", SHA256: "sha-local", StorageURL: "https://cdn/local.jpg", QualityScore: .9}); err != nil {
		t.Fatal(err)
	}
	source := &entityAssetSourceStub{}
	committer := &entityAssetCommitStub{}
	resolver, err := NewEntityAssetResolver(media, source, committer)
	if err != nil {
		t.Fatal(err)
	}
	got, err := resolver.Resolve(context.Background(), EntityAssetRequest{EntityType: "PERSON", CanonicalName: "Gerard Butler"})
	if err != nil {
		t.Fatal(err)
	}
	if got.AssetID != "asset-local" || !got.Verified || source.calls != 0 || committer.calls != 0 {
		t.Fatalf("got=%+v source=%d commits=%d", got, source.calls, committer.calls)
	}
}

func TestEntityAssetResolverFallbackCommitsAndIndexes(t *testing.T) {
	media := NewEntityMediaResolver()
	source := &entityAssetSourceStub{asset: EntityAsset{AssetID: "asset-remote", AssetType: "PHOTO_JPEG", SHA256: "sha-remote", StorageURL: "https://cdn/remote.jpg", QualityScore: .95, Source: "remote"}}
	committer := &entityAssetCommitStub{}
	resolver, err := NewEntityAssetResolver(media, source, committer)
	if err != nil {
		t.Fatal(err)
	}
	got, err := resolver.Resolve(context.Background(), EntityAssetRequest{EntityType: "PERSON", CanonicalName: "Gerard Butler"})
	if err != nil {
		t.Fatal(err)
	}
	if got.AssetID != "asset-remote" || source.calls != 1 || committer.calls != 1 {
		t.Fatalf("got=%+v source=%d commits=%d", got, source.calls, committer.calls)
	}
	if committer.request.ContentHash != "sha-remote" || committer.request.SourceURL != source.asset.StorageURL {
		t.Fatalf("commit request=%+v", committer.request)
	}
	second, err := resolver.Resolve(context.Background(), EntityAssetRequest{EntityType: "PERSON", CanonicalName: "Gerard Butler"})
	if err != nil || second.AssetID != got.AssetID || source.calls != 1 || committer.calls != 1 {
		t.Fatalf("cache after commit got=%+v err=%v source=%d commits=%d", second, err, source.calls, committer.calls)
	}
}
