package wiring

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// recordingAssetMutator captures the patches the admin-console Save path hands
// to the canonical media writer. The store under test must never issue media
// SQL itself, so this double is the ONLY observation channel.
type recordingAssetMutator struct {
	patches []persistence.AssetPatch
}

func (m *recordingAssetMutator) PatchAsset(_ context.Context, patch persistence.AssetPatch) error {
	m.patches = append(m.patches, patch)
	return nil
}

func (m *recordingAssetMutator) PatchAssetTx(context.Context, persistence.Transaction, persistence.AssetPatch) error {
	return nil
}

func (m *recordingAssetMutator) ReconcileDriveLocations(context.Context, []persistence.DriveLocationPatch) error {
	return nil
}

func (m *recordingAssetMutator) ReconcileDriveLocationsTx(context.Context, persistence.Transaction, []persistence.DriveLocationPatch) error {
	return nil
}

// TestPgMediaAssetStore_Save_MapsEditableFieldsOntoCanonicalPatch pins the
// admin-console → canonical-writer mapping: every editable field must reach the
// writer through a typed patch field, and tags/search_terms must be written as
// JSON arrays (the encoding the read side decodes).
func TestPgMediaAssetStore_Save_MapsEditableFieldsOntoCanonicalPatch(t *testing.T) {
	mutator := &recordingAssetMutator{}
	store := &pgMediaAssetStore{mutator: mutator}

	details := &asset.Details{Asset: &asset.Asset{
		ID:           "asset-1",
		Name:         "name-1",
		Category:     "category-1",
		Group:        "group-1",
		SearchText:   "search text",
		ReviewStatus: asset.ReviewStatus("approved"),
		Tags:         []string{"a", "b"},
		SearchTerms:  []string{"k1"},
	}}
	details.Asset.SetDescription("a description")
	details.Asset.SetLanguage("it")

	if err := store.Save(context.Background(), details); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if len(mutator.patches) != 1 {
		t.Fatalf("expected exactly one patch, got %d", len(mutator.patches))
	}
	patch := mutator.patches[0]

	if patch.AssetID != "asset-1" {
		t.Errorf("AssetID = %q", patch.AssetID)
	}
	for field, got := range map[string]*string{
		"Name":         patch.Name,
		"Category":     patch.Category,
		"Group":        patch.Group,
		"SearchText":   patch.SearchText,
		"ReviewStatus": patch.ReviewStatus,
	} {
		if got == nil {
			t.Errorf("%s was not mapped onto the canonical patch", field)
		}
	}
	if patch.Name == nil || *patch.Name != "name-1" {
		t.Errorf("Name = %v", patch.Name)
	}
	if patch.Category == nil || *patch.Category != "category-1" {
		t.Errorf("Category = %v", patch.Category)
	}
	if patch.Group == nil || *patch.Group != "group-1" {
		t.Errorf("Group = %v", patch.Group)
	}
	if patch.SearchText == nil || *patch.SearchText != "search text" {
		t.Errorf("SearchText = %v", patch.SearchText)
	}
	if patch.ReviewStatus == nil || *patch.ReviewStatus != "approved" {
		t.Errorf("ReviewStatus = %v", patch.ReviewStatus)
	}

	if patch.Tags == nil {
		t.Fatal("Tags were not mapped onto the canonical patch")
	}
	var tags []string
	if err := json.Unmarshal([]byte(*patch.Tags), &tags); err != nil {
		t.Fatalf("Tags is not JSON: %q (%v)", *patch.Tags, err)
	}
	if len(tags) != 2 || tags[0] != "a" || tags[1] != "b" {
		t.Errorf("Tags JSON = %q", *patch.Tags)
	}

	if patch.SearchTerms == nil {
		t.Fatal("SearchTerms were not mapped onto the canonical patch")
	}
	var terms []string
	if err := json.Unmarshal([]byte(*patch.SearchTerms), &terms); err != nil {
		t.Fatalf("SearchTerms is not JSON: %q (%v)", *patch.SearchTerms, err)
	}
	if len(terms) != 1 || terms[0] != "k1" {
		t.Errorf("SearchTerms JSON = %q", *patch.SearchTerms)
	}

	if patch.MetadataPatchJSON == nil {
		t.Fatal("description/language were not mapped onto the metadata merge patch")
	}
	var meta map[string]string
	if err := json.Unmarshal([]byte(*patch.MetadataPatchJSON), &meta); err != nil {
		t.Fatalf("MetadataPatchJSON is not JSON: %q (%v)", *patch.MetadataPatchJSON, err)
	}
	if meta["description"] != "a description" {
		t.Errorf("metadata description = %q", meta["description"])
	}
	if meta["language"] != "it" {
		t.Errorf("metadata language = %q", meta["language"])
	}
}

// TestPgMediaAssetStore_Save_OmitsEmptyCollections pins that an empty tag or
// search-term list does NOT emit a patch field: an empty JSON array would wipe
// the stored projection, and the admin console only sends changed fields.
func TestPgMediaAssetStore_Save_OmitsEmptyCollections(t *testing.T) {
	mutator := &recordingAssetMutator{}
	store := &pgMediaAssetStore{mutator: mutator}

	details := &asset.Details{Asset: &asset.Asset{ID: "asset-2", Name: "n"}}
	if err := store.Save(context.Background(), details); err != nil {
		t.Fatalf("Save: %v", err)
	}
	patch := mutator.patches[0]
	if patch.Tags != nil {
		t.Errorf("Tags must be omitted for an empty list, got %q", *patch.Tags)
	}
	if patch.SearchTerms != nil {
		t.Errorf("SearchTerms must be omitted for an empty list, got %q", *patch.SearchTerms)
	}
}

// TestPgMediaAssetStore_Save_FailsClosedWithoutWriter pins the degradation
// contract: with no canonical writer the store returns a typed error instead of
// silently falling back to a second (SQLite) media writer.
func TestPgMediaAssetStore_Save_FailsClosedWithoutWriter(t *testing.T) {
	store := &pgMediaAssetStore{}
	err := store.Save(context.Background(), &asset.Details{Asset: &asset.Asset{ID: "asset-3"}})
	if err == nil {
		t.Fatal("expected an error when the canonical writer is unavailable")
	}
	if !strings.Contains(err.Error(), "canonical writer unavailable") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestPgMediaAssetStore_Save_RejectsIncompleteDocuments pins the validation
// boundary so a malformed admin-console save cannot reach the writer.
func TestPgMediaAssetStore_Save_RejectsIncompleteDocuments(t *testing.T) {
	mutator := &recordingAssetMutator{}
	store := &pgMediaAssetStore{mutator: mutator}

	for name, details := range map[string]*asset.Details{
		"nil details": nil,
		"nil asset":   {},
		"empty id":    {Asset: &asset.Asset{Name: "no-id"}},
	} {
		if err := store.Save(context.Background(), details); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if len(mutator.patches) != 0 {
		t.Fatalf("no patch must reach the writer, got %d", len(mutator.patches))
	}
}
