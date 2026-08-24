// Package soundeffect ports — application-layer structural ports for the
// sound effect HTTP transport.
//
// PG-003 (June 2026): the sfx handler previously imported four concrete
// infrastructure types (*assets.ClipsRepository, *drive.Uploader,
// semantic.MetadataWriterPort, *drive.Resolver) which violated AGENTS.md
// Pattern 0 (typed ports). These ports replace those reach-throughs.
// Concrete adapters live in internal/app/adapters_soundeffect.go and
// implement compile-time `var _ <Port> = (*<Adapter>)(nil)` assertions.
package assets

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── DTOs (minimal surface — only fields the handler reads) ───────────

// F2.10: UploadResultDTO RETIRED (override brutal). The legacy
// drive.Uploader + sfxports.DriveUploaderPort plumbing is gone;
// delivery.Publisher.Publish + delivery.PublishResult are the only
// surfaces for the sfx Generate write path. Handler now reads
// FileID + WebViewLink + DownloadLink + MD5Checksum + Action from
// the publisher's PublishResult type directly.

// MetadataWriteRequest mirrors the sfx-relevant subset of
// semantic.WriteRequest. The concrete adapter drops unused fields
// (SourceType, Retriever, PageURL, ImageURL, License, Author,
// Extensions, GroupID, Assets).
type MetadataWriteRequest struct {
	AssetID   string
	AssetType string
	MediaType string
	Source    string
	Generator string
	Style     string
	Prompt    string
	LocalPath string
}

// MetadataWriteResponse mirrors sfx-relevant fields of semantic.Payload:
// only SearchText + Tags are read by the handler.
type MetadataWriteResponse struct {
	SearchText string
	Tags       []string
}

// AssetDestinationRequest mirrors the sfx-relevant subset of
// drive.AssetDestinationRequest: only Source, MediaType, Group, Hash,
// Ext are set in the handler body. The remaining fields (Style,
// Subject, DriveRootOverride, GenerationID) are translated to empty
// strings at the adapter boundary.
type AssetDestinationRequest struct {
	Source    string
	MediaType string
	Group     string
	Hash      string
	Ext       string
}

// ResolvedDest mirrors the sfx-relevant subset of drive.ResolvedDest.
// Only LocalPath is consumed; RelativePath is dropped.
type ResolvedDest struct {
	LocalPath string
}

// ── Structural ports ─────────────────────────────────────────────────

// ClipRepositoryPort persists the generated sound effect asset to the
// canonical media_assets store. The sfx path generates a single clip per
// request and writes it once via Upsert; no other operations are
// required at the handler boundary.
type ClipRepositoryPort interface {
	Upsert(ctx context.Context, clip *asset.Asset) error
}

// F2.10: DriveUploaderPort RETIRED (override brutal). The sfx
// Generate write path routes through delivery.Publisher.Publish
// (PublisherPort below). GetOrCreateFolder + UploadFile are no
// longer reached from the sfx package — DestinationSoundEffect's
// PathBuilder provides folder resolution internally on every
// Publish. The legacy `*drive.Uploader` plumbing is gone from
// internal/application/assets/soundeffect/.

// SemanticMetadataWriterPort produces the semantic metadata.json for
// the generated sfx asset. The handler reads only SearchText + Tags;
// adapter discards the rest.
type SemanticMetadataWriterPort interface {
	Write(ctx context.Context, req MetadataWriteRequest) (*MetadataWriteResponse, error)
}

// DestinationResolverPort resolves the on-disk path the generated sfx
// should be saved under. Pure-path (no Drive I/O); implemented in
// internal/app/adapters_soundeffect.go around drive.Resolver.
type DestinationResolverPort interface {
	Resolve(req AssetDestinationRequest) (ResolvedDest, error)
}

// PublisherPort is the narrow surface of delivery.Publisher consumed
// by the sfx Generate handler. Replaces the legacy GetOrCreateFolder
// + UploadFile drive calls (FASE 7 migration).
type PublisherPort interface {
	Publish(ctx context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error)
}

// DispatcherPort is the canonical narrow surface of
// mutations.AssetMutationDispatcher consumed by the sfx HTTP handler.
// The implementation atomically UPSERTs the sfx asset row in
// media_assets AND emits a corresponding asset.index.requested outbox
// event in a single transaction — the canonical QDRANT-002 pattern
// that closes the SQLite → Qdrant indexing gap and prevents orphan
// media_assets rows that never reach the vector store.
//
// contentHash is the ingest-time content fingerprint used by the
// dispatcher's supersede-gate dedup. Sfx consumers pass the MD5
// hash computed in step 2 of Generate; the dispatcher falls back to
// clip.ID when contentHash is empty (mirroring clip_update.go's
// pattern).
//
// PR 6 (June 2026, codex/qdrant-api-writers-fail-closed): narrow
// 1-method port mirroring appclips.ClipIndexDispatcherPort. Future
// PR 7/8 will promote the sfx consumer to the canonical
// mutations.AssetMutationDispatcher SSOT (3 methods) atomically
// alongside the application-layer migration. The adapter lives in
// internal/app/adapters_infra.go::sfxDispatcherAdapter (single
// composition bridge).
type DispatcherPort interface {
	EnqueueAndIndex(ctx context.Context, clip *asset.Asset, contentHash string) error
}
