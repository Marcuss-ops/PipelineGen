package embedding

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/models"
)

func TestCanonicalText_Values(t *testing.T) {
	c := CanonicalText
	if c.ModelID != models.E5.ID {
		t.Fatalf("ModelID = %q, want %s", c.ModelID, models.E5.ID)
	}
	if c.ModelRevision != models.E5.Revision {
		t.Fatalf("ModelRevision = %q, want %s", c.ModelRevision, models.E5.Revision)
	}
	if c.Dimension != models.E5.Dimensions {
		t.Fatalf("Dimension = %d, want %d", c.Dimension, models.E5.Dimensions)
	}
	if c.Normalization != "l2" {
		t.Fatalf("Normalization = %q, want l2", c.Normalization)
	}
	if c.Distance != "Cosine" {
		t.Fatalf("Distance = %q, want Cosine", c.Distance)
	}
	if c.QueryPrefix != "query: " {
		t.Fatalf("QueryPrefix = %q, want %q", c.QueryPrefix, "query: ")
	}
	if c.DocumentPrefix != "passage: " {
		t.Fatalf("DocumentPrefix = %q, want %q", c.DocumentPrefix, "passage: ")
	}
}

func TestContract_Hash_Deterministic(t *testing.T) {
	if got, want := CanonicalText.Hash(), CanonicalText.Hash(); got != want {
		t.Fatalf("Hash is not deterministic: %q vs %q", got, want)
	}
	if CanonicalText.Hash() == "" {
		t.Fatal("Hash must not be empty")
	}
	if len(CanonicalText.Hash()) != 64 {
		t.Fatalf("Hash length = %d, want 64 (sha256 hex)", len(CanonicalText.Hash()))
	}
}

func TestContract_Hash_ChangesWithField(t *testing.T) {
	base := CanonicalText.Hash()
	alt := CanonicalText
	alt.ModelID = "nomic-embed-text"
	if alt.Hash() == base {
		t.Fatal("changing ModelID must change the hash")
	}
	alt = CanonicalText
	alt.Dimension = 384
	if alt.Hash() == base {
		t.Fatal("changing Dimension must change the hash")
	}
	alt = CanonicalText
	alt.QueryPrefix = ""
	if alt.Hash() == base {
		t.Fatal("changing QueryPrefix must change the hash")
	}
}

func TestContract_Equal(t *testing.T) {
	if !CanonicalText.Equal(CanonicalText) {
		t.Fatal("a contract must equal itself")
	}
	alt := CanonicalText
	alt.ModelRevision = "different"
	if CanonicalText.Equal(alt) {
		t.Fatal("contracts differing in ModelRevision must not be equal")
	}
}

func TestContract_MatchesPartial(t *testing.T) {
	// Only dimension + distance populated: matches the canonical text channel.
	qdrant := Contract{Dimension: 768, Distance: "Cosine"}
	if !CanonicalText.MatchesPartial(qdrant) {
		t.Fatal("partial contract matching on dim+distance must pass")
	}
	if !CanonicalText.MatchesPartial(Contract{}) {
		t.Fatal("empty partial contract must trivially pass (nothing asserted)")
	}

	if CanonicalText.MatchesPartial(Contract{Dimension: 512}) {
		t.Fatal("partial dimension mismatch must fail")
	}
	if CanonicalText.MatchesPartial(Contract{Distance: "Euclid"}) {
		t.Fatal("partial distance mismatch must fail")
	}
	if CanonicalText.MatchesPartial(Contract{ModelID: "nomic-embed-text"}) {
		t.Fatal("partial model mismatch must fail")
	}
}
