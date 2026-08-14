// Package search — external_candidate_test.go pins the discovery-side
// identity contract: a noop resolver never fabricates an identity, and
// ExternalCandidate carries only the identity + display surface a provider
// hit legitimately owns.
package search

import (
	"context"
	"testing"
)

func TestNoopCanonicalIdentityResolverNeverResolves(t *testing.T) {
	r := NewNoopCanonicalIdentityResolver()

	src, err := r.ResolveSource(context.Background(), "artlist", "123456")
	if err != nil {
		t.Fatalf("ResolveSource err = %v, want nil", err)
	}
	if src.Resolved {
		t.Fatalf("noop ResolveSource must return Resolved=false, got %+v", src)
	}
	if src.AssetID != "" {
		t.Fatalf("noop ResolveSource must return empty AssetID, got %q", src.AssetID)
	}

	c, err := r.ResolveContent(context.Background(), "abc")
	if err != nil {
		t.Fatalf("ResolveContent err = %v, want nil", err)
	}
	if c.Resolved || c.AssetID != "" {
		t.Fatalf("noop ResolveContent must return unresolved empty identity, got %+v", c)
	}
}

func TestExternalCandidateShape(t *testing.T) {
	c := ExternalCandidate{
		SourceType:   "artlist",
		SourceRef:    "123456",
		Title:        "Sunset",
		URL:          "https://artlist.io/123456",
		KnownAssetID: "canonical-abc",
	}
	if c.SourceType != "artlist" || c.SourceRef != "123456" || c.Title != "Sunset" ||
		c.URL != "https://artlist.io/123456" || c.KnownAssetID != "canonical-abc" {
		t.Fatalf("ExternalCandidate field round-trip broken: %+v", c)
	}
}
