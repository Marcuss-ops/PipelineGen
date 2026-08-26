// Package retrieved — ingest.go declares the narrow ingest-
// surface for the Retrieved territory.
//
// Per the July 2026 image-restructuring plan, the retrieved
// subpackage owns the ingestion of externally-sourced images
// (Wikipedia / SearXNG / DuckDuckGo / Drive short-circuit hits).
// This file declares:
//
//   - IngestServicePort — the structural interface that any
//     "ingest a candidate URL into media_assets" implementation
//     must satisfy. The parent's *ImageStorageService already
//     satisfies this via its IngestImage method.
//   - IngestServiceAdapter — wraps an IngestServicePort so the
//     parent wiring can route Upload-Origin requests through
//     the same Router as retrieval.
//
// Like search_service.go, this file uses the structural-port
// pattern to avoid a parent-import cycle.
package retrieved

import (
	"context"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"io"
)

// IngestServicePort is the structural ingest port. Parent
// package's *ImageStorageService satisfies this via its
// IngestImage method.
type IngestServicePort interface {
	// IngestImage writes the supplied data to media_assets.
	// Mirrors the parent images.service.go façade.
	IngestImage(
		ctx context.Context,
		slug, style, genID string,
		data io.Reader,
		filename, sourceURL, description string,
		tags []string,
		skipDrive, skipMetadata bool,
	) (*detail.ImageAsset, error)

	// UploadToStyleDrive uploads an already-ingested asset to
	// the per-style Drive folder. Used by the Drive-territory
	// pipeline.
	UploadToStyleDrive(ctx context.Context, asset *detail.ImageAsset, style string) (string, string, error)
}

// IngestServiceAdapter wraps an IngestServicePort for callers
// that consume Service. Today the parent's
// *ImageStorageService satisfies IngestServicePort; the
// adapter is the bridge that lets Router handle Upload-territory
// requests through the same dispatch surface.
type IngestServiceAdapter struct {
	port IngestServicePort
}

// NewIngestServiceAdapter constructs an IngestServiceAdapter
// around the supplied port. nil port yields an adapter that
// returns ErrIngestPortNotWired on Search.
func NewIngestServiceAdapter(port IngestServicePort) *IngestServiceAdapter {
	return &IngestServiceAdapter{port: port}
}

// Search delegates to IngestImage. The Router
// interface requires a Search method; on Upload-territory
// requests this adapter synthesises a Search from the request
// origin metadata (slug + lang) and triggers ingest.
//
// Today this returns an empty result (in-place ingest only,
// not search). Composition roots using this adapter in Upload
// territory should pre-catalog the asset before the request —
// this adapter is a TRANSPORT SHIM, not an ingest orchestrator.
func (a *IngestServiceAdapter) Search(ctx context.Context, req SearchRequest) (SearchResponse, error) {
	if a == nil || a.port == nil {
		return SearchResponse{}, ErrIngestPortNotWired
	}
	_ = ctx
	if req.Query == "" {
		return SearchResponse{SubService: a.Name()}, nil
	}
	// Returning an empty result is the documented behaviour
	// for the Upload-territory fast-path: catalog lookups go
	// through catalog.CatalogSearch (read-only), not here.
	// This adapter exists only so Router has a uniform
	// service shape; the Upload-path semantics live elsewhere.
	return SearchResponse{SubService: a.Name()}, nil
}

// Name returns the territory identifier for the upload/
// ingest service.
func (a *IngestServiceAdapter) Name() string {
	return "ingest"
}

// ── Sentinel errors ──

// ErrIngestPortNotWired is returned by the ingest adapter when
// invoked without a backing port.
var ErrIngestPortNotWired = errRetrievedIngest("retrieved.IngestServiceAdapter: port not wired")

type errRetrievedIngest string

func (e errRetrievedIngest) Error() string { return string(e) }
