package artifacts

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// commitRequestCapturingCommitter records every CommitRequest the registry
// hands to the canonical persistence.AssetCommitter, so a test can assert on
// the exact bytes-identity payload that reaches the media SSOT.
type commitRequestCapturingCommitter struct {
	requests []persistence.CommitRequest
}

func (c *commitRequestCapturingCommitter) CommitTx(context.Context, persistence.Transaction, persistence.CommitRequest) (persistence.CommitResult, error) {
	return persistence.CommitResult{}, nil
}

func (c *commitRequestCapturingCommitter) CommitAndIndex(_ context.Context, req persistence.CommitRequest) (persistence.CommitResult, error) {
	c.requests = append(c.requests, req)
	return persistence.CommitResult{}, nil
}

func (c *commitRequestCapturingCommitter) CommitAsset(context.Context, persistence.AssetCommitRequest) (persistence.CommittedAsset, error) {
	return persistence.CommittedAsset{}, nil
}

var _ persistence.AssetCommitter = (*commitRequestCapturingCommitter)(nil)

// TestClipsRegistryUpsertMediaSendsTheContentAddressNotTheLegacyMD5 is the
// boundary regression pin for the media-identity programme.
//
// The registry used to pass rec.LegacyFileMD5 as the committer's ContentHash, so
// a caller could compute the canonical SHA-256 of the bytes and have the very
// next boundary silently replace it with the compatibility-only MD5. The
// mutation this test forbids is exactly that single-field substitution: put
// LegacyFileMD5 back in the CommitRequest and every assertion below fails.
func TestClipsRegistryUpsertMediaSendsTheContentAddressNotTheLegacyMD5(t *testing.T) {
	// A real canonical SHA-256 (64 hex chars) and a real MD5 (32 hex chars), so
	// the two are distinguishable by shape as well as by value.
	contentAddress := digest.SHA256String("canonical bytes that own this address")
	const legacyMD5 = "7f83b1657ff1fc53b92dc18148a1d65dfa135e2f"
	if !digest.IsCanonicalSHA256(contentAddress) {
		t.Fatalf("fixture digest %q is not a canonical SHA-256", contentAddress)
	}

	committer := &commitRequestCapturingCommitter{}
	registry := NewClipsRegistry(nil, nil, nil, committer)

	err := registry.UpsertMedia(context.Background(), &MediaRecord{
		ID:            "asset-content-address",
		Source:        "youtube",
		Name:          "clip",
		Filename:      "clip.mp4",
		MediaType:     "video",
		Status:        "ACTIVE",
		LocalPath:     "/tmp/clip.mp4",
		ContentHash:   contentAddress,
		LegacyFileMD5: legacyMD5,
	})
	if err != nil {
		t.Fatalf("UpsertMedia: %v", err)
	}
	if len(committer.requests) != 1 {
		t.Fatalf("commit requests = %d, want exactly 1", len(committer.requests))
	}

	req := committer.requests[0]
	if req.ContentHash != contentAddress {
		t.Errorf("committer ContentHash = %q, want the byte identity %q", req.ContentHash, contentAddress)
	}
	if req.ContentHash == legacyMD5 {
		t.Error("committer ContentHash = the caller's legacy MD5: MD5 must not reach the media SSOT as a content address")
	}

	// The legacy digest is not deleted — it keeps riding the compatibility
	// bucket (asset_locations), which is the only place a reader may find it.
	if len(req.Locations) == 0 {
		t.Fatal("no locations were committed; the legacy digest has no compatibility home")
	}
	foundLegacy := false
	for _, loc := range req.Locations {
		if loc.LegacyFileMD5 == legacyMD5 {
			foundLegacy = true
		}
		if loc.LegacyFileMD5 == contentAddress {
			t.Errorf("location %q carries the content address as its legacy digest", loc.Kind)
		}
	}
	if !foundLegacy {
		t.Errorf("no location preserved the caller's legacy digest %q", legacyMD5)
	}
}
