package asset

import (
	"strings"
	"testing"
)

func TestResolveEvidence_Precedence(t *testing.T) {
	cases := []struct {
		name     string
		in       EvidenceInput
		wantText string
		wantSrc  EvidenceTextSource
	}{
		{
			name:     "transcript wins over all",
			in:       EvidenceInput{AssetID: "a", Transcript: "T", SemanticSummary: "S", VisualSummary: "V", Summary: "U", Description: "D"},
			wantText: "T",
			wantSrc:  EvidenceTranscript,
		},
		{
			name:     "semantic_summary after transcript",
			in:       EvidenceInput{AssetID: "a", SemanticSummary: "S", VisualSummary: "V", Summary: "U", Description: "D"},
			wantText: "S",
			wantSrc:  EvidenceSemanticSummary,
		},
		{
			name:     "visual_summary after semantic_summary",
			in:       EvidenceInput{AssetID: "a", VisualSummary: "V", Summary: "U", Description: "D"},
			wantText: "V",
			wantSrc:  EvidenceVisualSummary,
		},
		{
			name:     "summary after visual_summary",
			in:       EvidenceInput{AssetID: "a", Summary: "U", Description: "D"},
			wantText: "U",
			wantSrc:  EvidenceSummary,
		},
		{
			name:     "description last",
			in:       EvidenceInput{AssetID: "a", Description: "D"},
			wantText: "D",
			wantSrc:  EvidenceDescription,
		},
		{
			name:     "whitespace-only tiers are skipped",
			in:       EvidenceInput{AssetID: "a", Transcript: "   ", SemanticSummary: "\t", Description: "D"},
			wantText: "D",
			wantSrc:  EvidenceDescription,
		},
		{
			name:     "empty bag is not groundable",
			in:       EvidenceInput{AssetID: "a"},
			wantText: "",
			wantSrc:  "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := ResolveEvidence(tc.in)
			if doc.Text != tc.wantText || doc.SourceType != tc.wantSrc {
				t.Fatalf("ResolveEvidence(%+v) = (Text=%q, SourceType=%q), want (%q, %q)",
					tc.in, doc.Text, doc.SourceType, tc.wantText, tc.wantSrc)
			}
			if tc.wantText == "" {
				if doc.Groundable() {
					t.Fatalf("empty evidence must not be groundable: %+v", doc)
				}
				if doc.SourceHash != "" {
					t.Fatalf("not-groundable document must have empty SourceHash, got %q", doc.SourceHash)
				}
				return
			}
			if !doc.Groundable() {
				t.Fatalf("resolved evidence must be groundable: %+v", doc)
			}
			if len(doc.SourceHash) != 64 {
				t.Fatalf("SourceHash length = %d, want 64 (sha256 hex)", len(doc.SourceHash))
			}
		})
	}
}

func TestResolveEvidence_TrimsTextAndLanguage(t *testing.T) {
	doc := ResolveEvidence(EvidenceInput{
		AssetID:     "a",
		Description: "  padded description  ",
		Language:    "  en  ",
	})
	if doc.Text != "padded description" {
		t.Fatalf("Text = %q, want trimmed", doc.Text)
	}
	if doc.Language != "en" {
		t.Fatalf("Language = %q, want en", doc.Language)
	}
	if doc.AssetID != "a" {
		t.Fatalf("AssetID = %q, want a", doc.AssetID)
	}
}

func TestResolveEvidence_DeterministicSourceHash(t *testing.T) {
	in := EvidenceInput{AssetID: "a", Description: "hello"}
	a := ResolveEvidence(in)
	b := ResolveEvidence(in)
	if a.SourceHash != b.SourceHash || a.SourceHash == "" {
		t.Fatalf("SourceHash not deterministic: %q vs %q", a.SourceHash, b.SourceHash)
	}
	if strings.Contains(a.SourceHash, " ") {
		t.Fatalf("SourceHash must be a compact hex digest, got %q", a.SourceHash)
	}
	// Changing the winning text must change the hash.
	if ResolveEvidence(EvidenceInput{AssetID: "a", Description: "world"}).SourceHash == a.SourceHash {
		t.Fatal("changing evidence text must change SourceHash")
	}
}

func TestCanonicalEvidenceTextSourceValues(t *testing.T) {
	vals := CanonicalEvidenceTextSourceValues()
	want := []EvidenceTextSource{
		EvidenceTranscript,
		EvidenceSemanticSummary,
		EvidenceVisualSummary,
		EvidenceSummary,
		EvidenceDescription,
	}
	if len(vals) != len(want) {
		t.Fatalf("len = %d, want %d", len(vals), len(want))
	}
	for i := range want {
		if vals[i] != want[i] {
			t.Fatalf("vals[%d] = %q, want %q", i, vals[i], want[i])
		}
	}
}
