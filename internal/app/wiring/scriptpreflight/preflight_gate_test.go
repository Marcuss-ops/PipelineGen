package scriptpreflight

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/operator"
)

type preflightInventoryStub struct {
	inspection *operator.AssetInspection
}

func (s preflightInventoryStub) List(context.Context, operator.AssetInventoryQuery) (operator.AssetInventoryPage, error) {
	return operator.AssetInventoryPage{}, nil
}

func (s preflightInventoryStub) Get(context.Context, string) (*operator.AssetInspection, error) {
	return s.inspection, nil
}

func (s preflightInventoryStub) Facets(context.Context) (*operator.AssetInventoryFacets, error) {
	return &operator.AssetInventoryFacets{}, nil
}

func TestScriptAssetPreflightGateDerivesDriveLinkFromFileID(t *testing.T) {
	gate := scriptAssetPreflightGate{reader: preflightInventoryStub{inspection: &operator.AssetInspection{
		AssetInventoryItem: operator.AssetInventoryItem{ID: "clip-1", LifecycleState: "ACTIVE", AssetState: "READY", DurationMS: 14000, HasDriveFile: true},
		Metadata:           map[string]any{"drive_file_id": "drive-file-123"},
	}}}
	fact, found, err := gate.PreflightAssetFacts(context.Background(), "clip-1")
	if err != nil || !found {
		t.Fatalf("PreflightAssetFacts = found %v, err %v", found, err)
	}
	if want := "https://drive.google.com/file/d/drive-file-123/view"; fact.DriveLink != want {
		t.Fatalf("DriveLink = %q, want %q", fact.DriveLink, want)
	}
}
