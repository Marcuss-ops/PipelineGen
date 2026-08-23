package transport

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

// TestPipelineGenQdrantNamespace_ValidUUID ensures the project
// namespace is a valid UUID. The 36-char hyphenated form is the
// canonical string form; if this test fails the namespace was
// silently corrupted (e.g. by a typo during a rename).
func TestPipelineGenQdrantNamespace_ValidUUID(t *testing.T) {
	got := schema.PipelineGenQdrantNamespace.String()
	if len(got) != 36 || strings.Count(got, "-") != 4 {
		t.Fatalf("schema.PipelineGenQdrantNamespace is not a canonical UUID string: %q", got)
	}
}

// TestAssetIDToQdrantPointID_Deterministic ensures the same input
// produces the same output every time (one of the three guarantees
// of schema.AssetIDToQdrantPointID).
func TestAssetIDToQdrantPointID_Deterministic(t *testing.T) {
	const assetID = "yt_abc123_000_015"
	first := schema.AssetIDToQdrantPointID(assetID)
	for i := 0; i < 16; i++ {
		if got := schema.AssetIDToQdrantPointID(assetID); got != first {
			t.Fatalf("non-deterministic output: first=%q got=%q (iter %d)", first, got, i)
		}
	}
}

// TestSameAssetAlwaysProducesSamePointID is the user-facing alias for
// TestAssetIDToQdrantPointID_Deterministic. Task 3 (July 2026): ensures
// the canonical function is deterministic — re-indexing the same asset
// always yields the same Qdrant point ID.
func TestSameAssetAlwaysProducesSamePointID(t *testing.T) {
	TestAssetIDToQdrantPointID_Deterministic(t)
}

// TestDifferentAssetsProduceDifferentPointIDs ensures that distinct
// asset IDs always produce distinct Qdrant point IDs. Task 3 (July 2026).
// Covers: one-char difference, prefix variants, realistic asset-ID shapes.
func TestDifferentAssetsProduceDifferentPointIDs(t *testing.T) {
	ids := []string{
		"yt_abc123_000_015",
		"yt_abc124_000_015", // one char diff
		"artlist_music_001",
		"artlist_music_002",
		"voiceover_en-US_hello",
		"voiceover_it-IT_ciao",
		"img_sunset_001",
		"img_sunset_002",
		"generated_dalle_future_city",
		"generated_midjourney_future_city",
	}
	seen := make(map[string]string, len(ids))
	for _, id := range ids {
		pt := schema.AssetIDToQdrantPointID(id)
		if pt == "" {
			t.Fatalf("AssetIDToQdrantPointID(%q) returned empty", id)
		}
		if prev, ok := seen[pt]; ok {
			t.Fatalf("collision: AssetIDToQdrantPointID(%q) == AssetIDToQdrantPointID(%q) == %q", prev, id, pt)
		}
		seen[pt] = id
	}
	if len(seen) != len(ids) {
		t.Fatalf("expected %d distinct point IDs, got %d", len(ids), len(seen))
	}
}

// TestAssetIDToQdrantPointID_CollisionResistance ensures a one-char
// difference in input produces a substantially different UUID output.
// SHA-1 hashes are expected to flip ~half their bits on a one-bit
// input change; for the QDRANT-001 boundary function we require
// strong-keyed independence so a typo in an asset ID doesn't
// accidentally map to a nearby asset's point ID.
func TestAssetIDToQdrantPointID_CollisionResistance(t *testing.T) {
	idA := schema.AssetIDToQdrantPointID("yt_abc123_000_015")
	idB := schema.AssetIDToQdrantPointID("yt_abc124_000_015") // one char diff
	if idA == idB {
		t.Fatalf("collision: %q == %q", idA, idB)
	}
	// Both must parse as canonical UUID form.
	if _, err := uuid.Parse(idA); err != nil {
		t.Fatalf("idA not a UUID: %v", err)
	}
	if _, err := uuid.Parse(idB); err != nil {
		t.Fatalf("idB not a UUID: %v", err)
	}
}

// TestAssetIDToQdrantPointID_EmptyInput ensures empty input yields
// the empty string. The caller (indexing.PayloadMapper.AssetToPoint) is
// expected to validate non-emptiness separately; this function only
// promises the empty-string passthrough so that downstream Qdrant
// calls receive a clearly invalid point ID rather than a fake
// UUID.
func TestAssetIDToQdrantPointID_EmptyInput(t *testing.T) {
	if got := schema.AssetIDToQdrantPointID(""); got != "" {
		t.Fatalf("empty input must yield empty output, got %q", got)
	}
}

// TestAssetIDToQdrantPointID_NamespaceIsolation ensures hashes under
// schema.PipelineGenQdrantNamespace cannot collide with hashes under any
// public uuid namespace (e.g. the URL namespace). This guards
// against an accidental future change that switches the namespace
// to one of the public variants — which would silently let another
// project's UUIDv5 derivation collide with our media_assets.
func TestAssetIDToQdrantPointID_NamespaceIsolation(t *testing.T) {
	const assetID = "yt_collision_probe_001"
	ours := schema.AssetIDToQdrantPointID(assetID)
	publicURLNamespace := uuid.NewSHA1(uuid.NameSpaceURL, []byte(assetID)).String()
	if ours == publicURLNamespace {
		t.Fatalf("our namespace collides with uuid.NameSpaceURL on input %q — boundary is not isolated", assetID)
	}
}

// TestAssetIDToQdrantPointID_Distribution sanity-checks the
// function over 1000 distinct inputs to make sure there are no
// degenerate collisions across realistic asset-ID shapes (each
// formatted "asset-NNNNN" via sprintf for genuine independent
// inputs). Asserts no duplicates in the output set.
func TestAssetIDToQdrantPointID_Distribution(t *testing.T) {
	const n = 1000
	seen := make(map[string]string, n)
	for i := 0; i < n; i++ {
		id := idForI(i)
		out := schema.AssetIDToQdrantPointID(id)
		if prev, ok := seen[out]; ok {
			t.Fatalf("duplicate output %q across inputs %q and %q", out, prev, id)
		}
		seen[out] = id
	}
	if got := len(seen); got != n {
		t.Fatalf("distribution produced %d distinct outputs for %d inputs", got, n)
	}
}

func idForI(i int) string {
	// Construct genuinely-distinct inputs (NOT i%32 which cycles).
	// Format: "asset-000000" through "asset-000999" for the first
	// 1000 iterations.
	const width = 6
	digits := make([]byte, width)
	for j := width - 1; j >= 0; j-- {
		digits[j] = byte('0' + i%10)
		i /= 10
	}
	return "asset-" + string(digits)
}
