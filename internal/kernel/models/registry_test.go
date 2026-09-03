package models

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// TestCanonical_ContainsModelsInStableOrder pins the registry size and order.
// Adding a model is a deliberate policy change: update this test and the
// godlike/06 CORE/OPTIONAL documentation together.
func TestCanonical_ContainsFiveModelsInStableOrder(t *testing.T) {
	got := Canonical()
	if len(got) != 6 {
		t.Fatalf("Canonical() returned %d models, want 6 (E5, SigLIP, Reranker, SegmentUnderstanding, CLAP, Whisper)", len(got))
	}
	wantOrder := []string{
		"intfloat/multilingual-e5-base",
		"google/siglip-so400m-patch14-384",
		"BAAI/bge-reranker-v2-m3",
		"gemma3:1b",
		"laion/clap-htsat-fused",
		"openai/whisper-large-v3-turbo",
	}
	for i, want := range wantOrder {
		if got[i].ID != want {
			t.Errorf("Canonical()[%d].ID = %q, want %q (stable order)", i, got[i].ID, want)
		}
	}
}

// TestE5_AnchoredToRegistryConstants pins the linkage between the
// canonical model entry and its identity constants.
func TestE5_AnchoredToRegistryConstants(t *testing.T) {
	if E5.ID != CanonicalTextModelID {
		t.Errorf("E5.ID = %q, want CanonicalTextModelID %q", E5.ID, CanonicalTextModelID)
	}
	if E5.Revision != CanonicalTextModelRevision {
		t.Errorf("E5.Revision = %q, want CanonicalTextModelRevision %q", E5.Revision, CanonicalTextModelRevision)
	}
	if E5.Dimensions != CanonicalTextModelDimensions {
		t.Errorf("E5.Dimensions = %d, want CanonicalTextModelDimensions %d", E5.Dimensions, CanonicalTextModelDimensions)
	}
}

// TestSigLIP_AnchoredToRegistryConstants pins the SigLIP identity and
// dimension linkage to the registry constants.
func TestSigLIP_AnchoredToRegistryConstants(t *testing.T) {
	if SigLIP.Revision != CanonicalVisualModelRevision {
		t.Errorf("SigLIP.Revision = %q, want CanonicalVisualModelRevision %q", SigLIP.Revision, CanonicalVisualModelRevision)
	}
	if SigLIP.Dimensions != CanonicalVisualModelDimensions {
		t.Errorf("SigLIP.Dimensions = %d, want CanonicalVisualModelDimensions %d", SigLIP.Dimensions, CanonicalVisualModelDimensions)
	}
	if SigLIP.ID != CanonicalVisualModelID {
		t.Errorf("SigLIP.ID = %q, want CanonicalVisualModelID %q", SigLIP.ID, CanonicalVisualModelID)
	}
}

// TestEnabled_CoreVsOptional pins the CORE/OPTIONAL split: E5 + SigLIP +
// Reranker + segment understanding are the canonical production set; CLAP +
// Whisper are optional (audio channel inactive in DefaultV3Schema, ASR upstream of indexing).
func TestEnabled_CoreVsOptional(t *testing.T) {
	for _, m := range []Model{E5, SigLIP, Reranker} {
		if !m.Enabled {
			t.Errorf("%s (%s) must be enabled (CORE set)", m.ID, m.Role)
		}
	}
	for _, m := range []Model{CLAP, Whisper} {
		if m.Enabled {
			t.Errorf("%s (%s) must be disabled (OPTIONAL set)", m.ID, m.Role)
		}
	}
}

// TestLicenses_Pinned locks the license facts so a model upgrade that
// changes licensing is a reviewed decision.
func TestLicenses_Pinned(t *testing.T) {
	want := map[string]string{
		"intfloat/multilingual-e5-base":    "MIT",
		"google/siglip-so400m-patch14-384": "Apache-2.0",
		"BAAI/bge-reranker-v2-m3":          "Apache-2.0",
		"gemma3:1b":                        "unknown",
		"laion/clap-htsat-fused":           "Apache-2.0",
		"openai/whisper-large-v3-turbo":    "MIT",
	}
	for _, m := range Canonical() {
		if got := want[m.ID]; got != m.License {
			t.Errorf("%s license = %q, want %q", m.ID, m.License, got)
		}
	}
}

// TestDenseDimensions pins the vector lengths: E5 768, SigLIP 1152 (the
// LIVE sentence-transformers so400m pooled output probed from the running
// production sidecar), CLAP 512; cross-encoder and ASR models carry no
// vector space (0).
func TestDenseDimensions(t *testing.T) {
	want := map[string]int{
		"intfloat/multilingual-e5-base":    768,
		"google/siglip-so400m-patch14-384": 1152,
		"BAAI/bge-reranker-v2-m3":          0,
		"laion/clap-htsat-fused":           512,
		"openai/whisper-large-v3-turbo":    0,
	}
	for _, m := range Canonical() {
		if m.Dimensions != want[m.ID] {
			t.Errorf("%s dimensions = %d, want %d", m.ID, m.Dimensions, want[m.ID])
		}
		if (m.Role == RoleReranker || m.Role == RoleTranscription) && m.HasVectorSpace() {
			t.Errorf("%s must not claim a vector space (role %s)", m.ID, m.Role)
		}
	}
}

// TestLanguages_Pinned locks the ASR language-support fact: only Whisper
// (99 languages) carries a language count; embedding/reranker models
// report 0 (not applicable). Changing the Whisper language count is a
// registry review, not a silent edit.
func TestLanguages_Pinned(t *testing.T) {
	for _, m := range Canonical() {
		if m.ID == Whisper.ID {
			if m.Languages != 99 {
				t.Errorf("Whisper languages = %d, want 99 (openai/whisper-large-v3-turbo covers 99 languages)", m.Languages)
			}
			continue
		}
		if m.Languages != 0 {
			t.Errorf("%s languages = %d, want 0 (language count applies to ASR models only)", m.ID, m.Languages)
		}
	}
}

// TestByID_RoundTrip verifies lookup by canonical id and fail-closed
// behavior for unknown ids.
func TestByID_RoundTrip(t *testing.T) {
	for _, m := range Canonical() {
		got, ok := ByID(m.ID)
		if !ok {
			t.Fatalf("ByID(%q) not found", m.ID)
		}
		if got != m {
			t.Errorf("ByID(%q) = %+v, want %+v", m.ID, got, m)
		}
	}
	if _, ok := ByID("nonexistent/model"); ok {
		t.Error("ByID(nonexistent/model) must return false (fail-closed)")
	}
}

// TestIdentity_Stable pins the identity fingerprint: id|revision, unique
// per model, deterministic across calls.
func TestIdentity_Stable(t *testing.T) {
	seen := map[string]string{}
	for _, m := range Canonical() {
		id := m.Identity()
		if id != m.ID+"|"+m.Revision {
			t.Errorf("%s Identity() = %q, want %q", m.ID, id, m.ID+"|"+m.Revision)
		}
		if prev, dup := seen[id]; dup {
			t.Errorf("duplicate identity %q (models %s and %s)", id, prev, m.ID)
		}
		seen[id] = m.ID
		if m.Identity() != id {
			t.Errorf("%s Identity() not deterministic", m.ID)
		}
	}
}

// TestNoDuplicateIDs guards against two entries sharing an id, which
// would silently break ByID and Identity-based cache keys.
func TestNoDuplicateIDs(t *testing.T) {
	seen := map[string]string{}
	for _, m := range Canonical() {
		if prev, dup := seen[m.ID]; dup {
			t.Errorf("duplicate model id %q (models %s and %s)", m.ID, prev, m.ID)
		}
		seen[m.ID] = string(m.Role)
	}
}

// TestChecksums_ValidSHA256OrEmpty pins the checksum contract: empty until
// the first download verifies against the upstream hub; once populated it
// MUST be valid SHA-256 hex (weights are never committed to Git).
func TestChecksums_ValidSHA256OrEmpty(t *testing.T) {
	for _, m := range Canonical() {
		if m.Checksum == "" {
			continue
		}
		if !digest.IsSHA256(m.Checksum) {
			t.Errorf("%s checksum %q is not SHA-256 hex", m.ID, m.Checksum)
		}
	}
}

// TestValidate_AllEntriesValid runs the internal consistency validator
// over the whole registry.
func TestValidate_AllEntriesValid(t *testing.T) {
	for _, m := range Canonical() {
		if err := m.Validate(); err != nil {
			t.Errorf("Validate(%s): %v", m.ID, err)
		}
	}
}
