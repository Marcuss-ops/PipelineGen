package collections

import (
	"testing"

	qdrantschema "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

func TestNewProjectionManagerFor_BindsContractSchema(t *testing.T) {
	cases := []struct {
		name     string
		contract qdrantschema.ProjectionContract
		wantKind qdrantschema.ProjectionKind
	}{
		{
			name:     "media_assets",
			contract: qdrantschema.MediaAssetsProjection(),
			wantKind: qdrantschema.ProjectionMediaAssets,
		},
		{
			name:     "media_frames",
			contract: qdrantschema.MediaFramesProjection(),
			wantKind: qdrantschema.ProjectionMediaFrames,
		},
		{
			name:     "media_concepts",
			contract: qdrantschema.MediaConceptsProjection(),
			wantKind: qdrantschema.ProjectionMediaConcepts,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr, err := NewProjectionManagerFor(tc.contract, nil, nil)
			if err != nil {
				t.Fatalf("NewProjectionManagerFor() = %v, want nil", err)
			}
			if mgr == nil {
				t.Fatal("NewProjectionManagerFor() returned nil manager")
			}
			if mgr.schema == nil {
				t.Fatal("manager bound to a nil schema")
			}
			if mgr.schema.RuntimeAlias != tc.contract.Alias() {
				t.Fatalf("manager alias=%q, want %q", mgr.schema.RuntimeAlias, tc.contract.Alias())
			}
			if mgr.schema.PhysicalName != tc.contract.PhysicalName() {
				t.Fatalf("manager physical name=%q, want %q", mgr.schema.PhysicalName, tc.contract.PhysicalName())
			}
		})
	}
}

func TestNewProjectionManagerFor_RejectsInvalidContract(t *testing.T) {
	contract := qdrantschema.MediaAssetsProjection()
	contract.Kind = "other"
	if _, err := NewProjectionManagerFor(contract, nil, nil); err == nil {
		t.Fatal("NewProjectionManagerFor(invalid contract) = nil error, want error")
	}
}

func TestNewProjectionManagers_DedicatedAndDistinct(t *testing.T) {
	managers, err := NewProjectionManagers(nil, nil)
	if err != nil {
		t.Fatalf("NewProjectionManagers() = %v, want nil", err)
	}
	if managers.Assets == nil || managers.Frames == nil || managers.Concepts == nil {
		t.Fatalf("expected three dedicated managers, got assets=%v frames=%v concepts=%v",
			managers.Assets != nil, managers.Frames != nil, managers.Concepts != nil)
	}
	// The three managers must be distinct instances bound to distinct schemas.
	if managers.Assets == managers.Frames || managers.Frames == managers.Concepts || managers.Assets == managers.Concepts {
		t.Fatal("projection managers are not distinct instances")
	}
	if managers.Assets.schema.PhysicalName == managers.Frames.schema.PhysicalName ||
		managers.Frames.schema.PhysicalName == managers.Concepts.schema.PhysicalName ||
		managers.Assets.schema.PhysicalName == managers.Concepts.schema.PhysicalName {
		t.Fatal("projection managers share a physical collection name")
	}
}

func TestProjectionManagers_For(t *testing.T) {
	managers, err := NewProjectionManagers(nil, nil)
	if err != nil {
		t.Fatalf("NewProjectionManagers() = %v, want nil", err)
	}
	if managers.For(qdrantschema.ProjectionMediaAssets) != managers.Assets {
		t.Fatal("For(media_assets) != Assets")
	}
	if managers.For(qdrantschema.ProjectionMediaFrames) != managers.Frames {
		t.Fatal("For(media_frames) != Frames")
	}
	if managers.For(qdrantschema.ProjectionMediaConcepts) != managers.Concepts {
		t.Fatal("For(media_concepts) != Concepts")
	}
	if managers.For("unknown") != nil {
		t.Fatal("For(unknown kind) != nil")
	}
	var nilManagers *ProjectionManagers
	if nilManagers.For(qdrantschema.ProjectionMediaAssets) != nil {
		t.Fatal("nil ProjectionManagers.For() should be nil")
	}
}
