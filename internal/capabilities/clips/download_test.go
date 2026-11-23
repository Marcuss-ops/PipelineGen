package clips

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

type localPathAssetReader struct {
	asset *asset.Asset
}

func (r localPathAssetReader) Get(context.Context, string) (*asset.Asset, error) {
	return r.asset, nil
}

// TestDownloadUseCaseResolveExtensionlessLocalPath pins the regression where a
// local path without a dot made strings.LastIndex return -1 and the slice
// expression panic with "slice bounds out of range [-1:]".
func TestDownloadUseCaseResolveExtensionlessLocalPath(t *testing.T) {
	a := &asset.Asset{ID: "asset_no_ext", Metadata: asset.Metadata{}}
	a.SetLocalPath("/var/lib/pipelinegen/media/no-extension")

	uc := NewDownloadUseCase(localPathAssetReader{asset: a}, nil)
	result, err := uc.Resolve(context.Background(), "local", "asset_no_ext")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if result.Source != DownloadSourceLocal {
		t.Fatalf("Source = %v, want DownloadSourceLocal", result.Source)
	}
	if result.LocalPath != "/var/lib/pipelinegen/media/no-extension" {
		t.Fatalf("LocalPath = %q", result.LocalPath)
	}
}

// TestDownloadUseCaseResolveBlockedExtension keeps the existing guard honest:
// a text sidecar next to a clip is never proxied as the clip bytes.
func TestDownloadUseCaseResolveBlockedExtension(t *testing.T) {
	a := &asset.Asset{ID: "asset_json", Metadata: asset.Metadata{}}
	a.SetLocalPath("/var/lib/pipelinegen/media/metadata.json")

	uc := NewDownloadUseCase(localPathAssetReader{asset: a}, nil)
	result, err := uc.Resolve(context.Background(), "local", "asset_json")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if result.Source != DownloadSourceNone {
		t.Fatalf("Source = %v, want DownloadSourceNone", result.Source)
	}
}
