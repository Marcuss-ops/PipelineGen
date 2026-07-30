package scriptgeneration

import (
	"encoding/json"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func TestBuildGenerateRequest_MapsExplicitDocsConfig(t *testing.T) {
	var env scriptpkg.GenerationEnvelopeV2
	err := json.Unmarshal([]byte(`{
		"version": 2,
		"items": [{
			"title": "test",
			"language": "it",
			"source": {"type": "text", "topic": "topic"},
			"docs": {"enabled": true, "languages": ["it"], "folder_id": "folder"}
		}]
	}`), &env)
	if err != nil {
		t.Fatal(err)
	}

	got, err := BuildGenerateRequest(&env, "key")
	if err != nil {
		t.Fatal(err)
	}

	if !got.Docs.Enabled || got.Docs.FolderID != "folder" {
		t.Fatalf("docs config not mapped: %+v", got.Docs)
	}
	if len(got.Docs.Languages) != 1 || got.Docs.Languages[0] != "it" {
		t.Fatalf("docs languages not mapped: %v", got.Docs.Languages)
	}
}
