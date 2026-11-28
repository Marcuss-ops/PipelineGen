package scriptpreflight

// stock_gate.go adapts the media registry (the same rows the remote final-job
// handoff resolves in internal/app/modules/script/remote_final_job_resolve.go)
// and the Drive folder metadata to the two narrow ports POST
// /api/script/preflight consumes (CS27-06).
//
// Why an adapter instead of passing the searcher through: the script capability
// must not import the postgres media plane, and the gate must read the SAME
// registry the handoff reads. If the preflight resolved stock through a
// different projection, it could answer ok=true for a source the handoff then
// refuses — which is exactly the r14 failure this port exists to prevent.

import (
	"context"
	"strings"

	scriptapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/script"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// StockRegistryReader is the narrow read surface of the media registry: the
// same two lookups the remote handoff performs.
type StockRegistryReader interface {
	ResolveByDriveFileID(ctx context.Context, fileID string) ([]*asset.Asset, error)
	ResolveByMediaAssetID(ctx context.Context, id string) (*asset.Asset, error)
}

// StockFolderNamer is the one Drive admin method the folder check needs.
type StockFolderNamer interface {
	GetFolderName(ctx context.Context, folderID string) (string, error)
}

type scriptStockPreflightGate struct {
	registry StockRegistryReader
	folders  StockFolderNamer
}

type scriptStockFoldersGate struct {
	folders StockFolderNamer
}

// NewScriptStockPreflightGate returns nil when the registry is unavailable, so
// the caller leaves the port unwired and the route reports unverified=true
// instead of pretending the stock inventory is clean.
func NewScriptStockPreflightGate(registry StockRegistryReader, folders StockFolderNamer) scriptapi.StockPreflightReader {
	if registry == nil {
		return nil
	}
	return scriptStockPreflightGate{registry: registry, folders: folders}
}

// NewScriptStockFoldersGate exposes only the Drive folder reader. It is a
// separate constructor so a deployment without a registry can still name a
// wrong folder, and so neither port is ever satisfied by a typed nil.
func NewScriptStockFoldersGate(folders StockFolderNamer) scriptapi.StockFolderPreflightReader {
	if folders == nil {
		return nil
	}
	return scriptStockFoldersGate{folders: folders}
}

func (g scriptStockPreflightGate) PreflightStockFacts(ctx context.Context, ref scriptapi.StockPreflightRef) (scriptapi.StockPreflightFact, bool, error) {
	saved, err := g.resolve(ctx, ref)
	if err != nil {
		return scriptapi.StockPreflightFact{}, false, err
	}
	if saved == nil {
		return scriptapi.StockPreflightFact{}, false, nil
	}
	return scriptapi.StockPreflightFact{
		DriveFileID:      saved.DriveFileID(),
		Name:             saved.Name,
		DurationUS:       saved.Duration.Microseconds(),
		SizeBytes:        saved.SizeBytes(),
		ContentSHA256Len: len(strings.ToLower(saved.ContentHash())),
	}, true, nil
}

// resolve reads the same row the handoff reads, by the ONE identity the payload
// declared. A registry row without a Drive identity is still a hit: the verdict
// reports the missing file id instead of pretending the source is absent.
func (g scriptStockPreflightGate) resolve(ctx context.Context, ref scriptapi.StockPreflightRef) (*asset.Asset, error) {
	id := strings.TrimSpace(ref.ID)
	if id == "" {
		return nil, nil
	}
	switch ref.Kind {
	case scriptapi.StockReferenceKindAsset:
		return g.registry.ResolveByMediaAssetID(ctx, id)
	case scriptapi.StockReferenceKindDriveFile:
		assets, err := g.registry.ResolveByDriveFileID(ctx, id)
		if err != nil {
			return nil, err
		}
		for _, candidate := range assets {
			if candidate != nil && candidate.DriveFileID() == id {
				return candidate, nil
			}
		}
		return nil, nil
	default:
		return nil, nil
	}
}

// PreflightStockFolderName is best-effort by contract: a Drive that does not
// answer must degrade to "not verified" in the report, never to a false red.
func (g scriptStockFoldersGate) PreflightStockFolderName(ctx context.Context, folderID string) (string, bool, error) {
	name, err := g.folders.GetFolderName(ctx, strings.TrimSpace(folderID))
	if err != nil {
		return "", false, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false, nil
	}
	return name, true, nil
}
