package models_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	coreembedding "github.com/Marcuss-ops/PipelineGen/internal/kernel/embedding"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/models"
	qdrantschema "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func TestRegistryEntriesHaveCanonicalMetadata(t *testing.T) {
	wantRevisions := map[string]string{
		models.E5.ID:       models.CanonicalTextModelRevision,
		models.SigLIP.ID:   models.CanonicalVisualModelRevision,
		models.Reranker.ID: "",
		models.CLAP.ID:     "2026-06-26-v1",
		models.Whisper.ID:  "",
	}
	wantLicenses := map[string]string{
		models.E5.ID:       "MIT",
		models.SigLIP.ID:   "Apache-2.0",
		models.Reranker.ID: "Apache-2.0",
		models.CLAP.ID:     "Apache-2.0",
		models.Whisper.ID:  "MIT",
	}
	wantDimensions := map[string]int{
		models.E5.ID:       768,
		models.SigLIP.ID:   768,
		models.Reranker.ID: 0,
		models.CLAP.ID:     512,
		models.Whisper.ID:  0,
	}
	wantRoles := map[string]models.Role{
		models.E5.ID:       models.RoleTextEmbedding,
		models.SigLIP.ID:   models.RoleVisualEmbedding,
		models.Reranker.ID: models.RoleReranker,
		models.CLAP.ID:     models.RoleAudioEmbedding,
		models.Whisper.ID:  models.RoleTranscription,
	}
	wantLanguages := map[string]int{
		models.E5.ID:       0,
		models.SigLIP.ID:   0,
		models.Reranker.ID: 0,
		models.CLAP.ID:     0,
		models.Whisper.ID:  99, // ASR capability fact: 99 languages
	}

	seen := make(map[string]struct{}, len(models.Canonical()))
	for _, model := range models.Canonical() {
		if _, duplicate := seen[model.ID]; duplicate {
			t.Errorf("duplicate registry ID %q", model.ID)
		}
		seen[model.ID] = struct{}{}

		if model.Revision != wantRevisions[model.ID] {
			t.Errorf("%s revision = %q, want %q", model.ID, model.Revision, wantRevisions[model.ID])
		}
		if model.License != wantLicenses[model.ID] {
			t.Errorf("%s license = %q, want %q", model.ID, model.License, wantLicenses[model.ID])
		}
		if model.Dimensions != wantDimensions[model.ID] {
			t.Errorf("%s dimensions = %d, want %d", model.ID, model.Dimensions, wantDimensions[model.ID])
		}
		if model.Role != wantRoles[model.ID] {
			t.Errorf("%s role = %q, want %q", model.ID, model.Role, wantRoles[model.ID])
		}
		if model.Languages != wantLanguages[model.ID] {
			t.Errorf("%s languages = %d, want %d", model.ID, model.Languages, wantLanguages[model.ID])
		}
		if err := model.Validate(); err != nil {
			t.Errorf("%s registry entry is invalid: %v", model.ID, err)
		}
	}
}

func TestRegistryAndE5ContractAreCoherent(t *testing.T) {
	contract := coreembedding.CanonicalText
	if contract.ModelID != models.E5.ID {
		t.Fatalf("contract model = %q, want registry E5 ID %q", contract.ModelID, models.E5.ID)
	}
	if contract.ModelRevision != models.E5.Revision {
		t.Fatalf("contract revision = %q, want registry E5 revision %q", contract.ModelRevision, models.E5.Revision)
	}
	if contract.Dimension != models.E5.Dimensions {
		t.Fatalf("contract dimension = %d, want registry E5 dimension %d", contract.Dimension, models.E5.Dimensions)
	}
	if contract.Normalization != "l2" || contract.Distance != "Cosine" {
		t.Fatalf("contract metric = normalization=%q distance=%q, want l2/Cosine", contract.Normalization, contract.Distance)
	}
	if contract.QueryPrefix != "query: " || contract.DocumentPrefix != "passage: " {
		t.Fatalf("E5 prefixes = query=%q document=%q, want %q/%q", contract.QueryPrefix, contract.DocumentPrefix, "query: ", "passage: ")
	}
	if contract.Hash() == "" || len(contract.Hash()) != 64 {
		t.Fatalf("contract hash = %q, want 64-character SHA-256 hex", contract.Hash())
	}
}

func TestRegistryAndQdrantSchemaAreCoherent(t *testing.T) {
	schema := qdrantschema.DefaultV3Schema()
	if err := schema.Validate(); err != nil {
		t.Fatalf("canonical Qdrant schema is invalid: %v", err)
	}

	for _, channel := range []string{"text", "transcript"} {
		spec := schema.GetDense(channel)
		if spec == nil {
			t.Fatalf("missing Qdrant %s channel", channel)
		}
		if spec.Model != models.E5.ID || spec.ModelVersion != models.E5.Revision || spec.Dimensions != models.E5.Dimensions {
			t.Errorf("Qdrant %s identity drift: got %q/%q/%d, want %q/%q/%d", channel, spec.Model, spec.ModelVersion, spec.Dimensions, models.E5.ID, models.E5.Revision, models.E5.Dimensions)
		}
		if spec.Distance != coreembedding.DistanceCosine || !spec.Normalized {
			t.Errorf("Qdrant %s metric = distance=%q normalized=%v, want Cosine/true", channel, spec.Distance, spec.Normalized)
		}
		if spec.QueryPrefix != coreembedding.QueryPrefixE5 || spec.IndexPrefix != coreembedding.DocumentPrefixE5 {
			t.Errorf("Qdrant %s prefixes = query=%q index=%q, want %q/%q", channel, spec.QueryPrefix, spec.IndexPrefix, coreembedding.QueryPrefixE5, coreembedding.DocumentPrefixE5)
		}
	}

	visual := schema.GetDense("visual")
	if visual == nil {
		t.Fatal("missing Qdrant visual channel")
	}
	if visual.Model != models.SigLIP.ID || visual.ModelVersion != models.SigLIP.Revision || visual.Dimensions != models.SigLIP.Dimensions {
		t.Errorf("Qdrant visual identity drift: got %q/%q/%d, want %q/%q/%d", visual.Model, visual.ModelVersion, visual.Dimensions, models.SigLIP.ID, models.SigLIP.Revision, models.SigLIP.Dimensions)
	}
	if visual.Distance != coreembedding.DistanceCosine || !visual.Normalized {
		t.Errorf("Qdrant visual metric = distance=%q normalized=%v, want Cosine/true", visual.Distance, visual.Normalized)
	}
}

func TestRegistryAndGeneratedPythonMirrorAreCoherent(t *testing.T) {
	path := filepath.Join(repositoryRoot(t), "scripts", "services", "model_registry_generated.py")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read generated model registry mirror: %v", err)
	}
	content := string(data)

	for _, model := range models.Canonical() {
		entry := fmt.Sprintf("%q: {", model.ID)
		if !strings.Contains(content, entry) {
			t.Errorf("Python mirror missing model entry %q", model.ID)
			continue
		}
		for _, field := range []string{
			fmt.Sprintf("\"revision\": %q", model.Revision),
			fmt.Sprintf("\"checksum\": %q", model.Checksum),
			fmt.Sprintf("\"dimensions\": %d", model.Dimensions),
			fmt.Sprintf("\"license\": %q", model.License),
			fmt.Sprintf("\"role\": %q", model.Role),
			fmt.Sprintf("\"languages\": %d", model.Languages),
			fmt.Sprintf("\"enabled\": %s", pythonBool(model.Enabled)),
		} {
			if !strings.Contains(content, field) {
				t.Errorf("Python mirror is missing %s for model %q", field, model.ID)
			}
		}
	}
}

func pythonBool(value bool) string {
	if value {
		return "True"
	}
	return "False"
}

func TestModelValidateRejectsInvalidMetadata(t *testing.T) {
	base := models.Model{
		ID: "example/model", Role: models.RoleTextEmbedding, License: "MIT", Dimensions: 768,
	}
	tests := []struct {
		name string
		edit func(*models.Model)
	}{
		{"empty id", func(m *models.Model) { m.ID = "" }},
		{"empty role", func(m *models.Model) { m.Role = "" }},
		{"empty license", func(m *models.Model) { m.License = "" }},
		{"negative dimensions", func(m *models.Model) { m.Dimensions = -1 }},
		{"invalid checksum", func(m *models.Model) { m.Checksum = "not-a-sha256" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := base
			tt.edit(&model)
			if err := model.Validate(); err == nil {
				t.Fatal("expected metadata validation error")
			}
		})
	}

	base.Checksum = strings.Repeat("a", 64)
	if err := base.Validate(); err != nil {
		t.Fatalf("valid SHA-256 checksum rejected: %v", err)
	}
}
