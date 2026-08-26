package models

import (
	"sort"
	"testing"

	coreembedding "github.com/Marcuss-ops/PipelineGen/internal/kernel/embedding"
)

func TestCanonicalText_MatchesEmbeddingContract(t *testing.T) {
	// SSOT coherence: the registry's text entry must be in lockstep with the
	// EmbeddingContract SSOT (internal/kernel/embedding) — same id, revision,
	// and dimension. If one is bumped without the other, this fails.
	c := CanonicalText
	if c.ID != coreembedding.ModelIDMultilingualE5 {
		t.Fatalf("CanonicalText.ID = %q, want %q (embedding contract)", c.ID, coreembedding.ModelIDMultilingualE5)
	}
	if c.Revision != coreembedding.ModelRevisionMultilingualE5 {
		t.Fatalf("CanonicalText.Revision = %q, want %q (embedding contract)", c.Revision, coreembedding.ModelRevisionMultilingualE5)
	}
	if c.Dimensions != coreembedding.DimensionText {
		t.Fatalf("CanonicalText.Dimensions = %d, want %d (embedding contract)", c.Dimensions, coreembedding.DimensionText)
	}
}

func TestCanonicalEntries_Values(t *testing.T) {
	cases := []struct {
		name       string
		got        Model
		wantID     string
		wantRev    string
		wantDim    int
		wantLic    string
		wantRole   Role
		wantEnable bool
	}{
		{"text", CanonicalText, "intfloat/multilingual-e5-base", "2026-06-26-v1", 768, "MIT", RoleTextEmbedding, true},
		{"visual", CanonicalVisual, "google/siglip-so400m-patch14-384", "2026-06-26-v1", 768, "Apache-2.0", RoleVisualEmbedding, true},
		{"audio", CanonicalAudio, "laion/clap-htsat-fused", "2026-06-26-v1", 512, "Apache-2.0", RoleAudioEmbedding, false},
		{"reranker", CanonicalReranker, "BAAI/bge-reranker-v2-m3", "", 0, "Apache-2.0", RoleReranker, true},
		{"asr", CanonicalASR, "openai/whisper-large-v3-turbo", "", 0, "MIT", RoleTranscription, false},
		{"bm25", CanonicalBM25, "qdrant/bm25", "", 0, "Apache-2.0", RoleSparse, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got.ID != tc.wantID {
				t.Fatalf("ID = %q, want %q", tc.got.ID, tc.wantID)
			}
			if tc.got.Revision != tc.wantRev {
				t.Fatalf("Revision = %q, want %q", tc.got.Revision, tc.wantRev)
			}
			if tc.got.Dimensions != tc.wantDim {
				t.Fatalf("Dimensions = %d, want %d", tc.got.Dimensions, tc.wantDim)
			}
			if tc.got.License != tc.wantLic {
				t.Fatalf("License = %q, want %q", tc.got.License, tc.wantLic)
			}
			if tc.got.Role != tc.wantRole {
				t.Fatalf("Role = %q, want %q", tc.got.Role, tc.wantRole)
			}
			if tc.got.Enabled != tc.wantEnable {
				t.Fatalf("Enabled = %t, want %t", tc.got.Enabled, tc.wantEnable)
			}
		})
	}
}

func TestAll_HasSixCanonicalModels(t *testing.T) {
	all := All()
	if len(all) != 6 {
		t.Fatalf("All() has %d models, want 6 (5 ML models + BM25)", len(all))
	}
	seen := make(map[string]bool)
	for _, m := range all {
		if seen[m.ID] {
			t.Fatalf("duplicate model id in registry: %q", m.ID)
		}
		seen[m.ID] = true
	}
}

func TestLookup(t *testing.T) {
	m, ok := Lookup("intfloat/multilingual-e5-base")
	if !ok {
		t.Fatal("Lookup(multilingual-e5-base) must succeed")
	}
	if m.Role != RoleTextEmbedding {
		t.Fatalf("Lookup role = %q, want embedding", m.Role)
	}

	if _, ok := Lookup("nomic-embed-text"); ok {
		t.Fatal("Lookup of a legacy/non-canonical model must fail")
	}
}

func TestByRole(t *testing.T) {
	m, ok := ByRole(RoleReranker)
	if !ok {
		t.Fatal("ByRole(reranker) must succeed")
	}
	if m.ID != "BAAI/bge-reranker-v2-m3" {
		t.Fatalf("ByRole(reranker).ID = %q, want BAAI/bge-reranker-v2-m3", m.ID)
	}

	if _, ok := ByRole(Role("nonexistent")); ok {
		t.Fatal("ByRole of an unknown role must fail")
	}
}

func TestEnabled_HasFourCore(t *testing.T) {
	enabled := Enabled()
	if len(enabled) != 4 {
		t.Fatalf("Enabled() has %d models, want 4 (text, visual, reranker, bm25)", len(enabled))
	}
	var ids []string
	for _, m := range enabled {
		ids = append(ids, m.ID)
	}
	sort.Strings(ids)
	want := []string{"BAAI/bge-reranker-v2-m3", "google/siglip-so400m-patch14-384", "intfloat/multilingual-e5-base", "qdrant/bm25"}
	if len(ids) != len(want) {
		t.Fatalf("Enabled() ids = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("Enabled() ids = %v, want %v", ids, want)
		}
	}
}

func TestValidate_OK(t *testing.T) {
	if err := Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestHash_DeterministicAndSensitive(t *testing.T) {
	h1, h2 := Hash(), Hash()
	if h1 != h2 {
		t.Fatal("Hash is not deterministic")
	}
	if len(h1) != 64 {
		t.Fatalf("Hash length = %d, want 64 (sha256 hex)", len(h1))
	}

	// Changing any identity fact must change the fingerprint.
	orig := CanonicalVisual
	CanonicalVisual.Revision = "2026-06-16-v1"
	if Hash() == h1 {
		t.Fatal("changing a model identity fact must change the registry hash")
	}
	CanonicalVisual = orig
	if Hash() != h1 {
		t.Fatal("restoring the canonical instance must restore the registry hash")
	}
}
