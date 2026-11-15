package scriptgeneration

import (
	"strings"
	"testing"
)

func TestBestVerifiedSemanticHitSkipsUnverifiedProviderOrder(t *testing.T) {
	unverified := SemanticAssetHit{AssetID: "provider-first", URL: "https://example.invalid/first.jpg"}
	verified := SemanticAssetHit{
		AssetID: "verified-second", URL: "https://example.invalid/second.jpg",
		SemanticStatus: "accepted", VerifierVersion: "person-image-visual/v3", SHA256: strings.Repeat("a", 64),
	}
	best, ok := bestVerifiedSemanticHit([]SemanticAssetHit{unverified, verified})
	if !ok || best.AssetID != "verified-second" {
		t.Fatalf("best verified hit = %+v ok=%v", best, ok)
	}
	if _, ok := bestVerifiedSemanticHit([]SemanticAssetHit{unverified}); ok {
		t.Fatal("an unverified provider order must not be admitted")
	}
}
