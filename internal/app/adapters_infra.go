// Package app — adapters satisfying the typed ports declared in
// internal/application/<feature>/ports.go (or contract.go, after the
// Capability Standard migration).
//
// Capability Standard migration (June 2026): channelRepositoryAdapter
// has been relocated to internal/application/channels/adapters.go so
// the channels capability is the canonical owner of its own
// infrastructure adapter. This file keeps the cross-capability
// adapters that have no obvious single owner (sfx, drive, reconciler,
// doctor, artlist config, middleware flags).
package app

import (
	"context"
	"fmt"

	systemapi "github.com/Marcuss-ops/PipelineGen/internal/api/system"
	artlistPkg "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	sfxports "github.com/Marcuss-ops/PipelineGen/internal/application/assets/soundeffect"
	"github.com/Marcuss-ops/PipelineGen/internal/application/middleware"
	searchpkg "github.com/Marcuss-ops/PipelineGen/internal/application/search"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

// ── ClipsRepo adapter ────────────────────────────────────────────────

// sfxClipsRepoAdapter wraps *assets.ClipsRepository to satisfy
// sfxports.ClipRepositoryPort.
type sfxClipsRepoAdapter struct {
	repo *assets.ClipsRepository
}

// Compile-time assertion: sfxClipsRepoAdapter satisfies sfxports.ClipRepositoryPort.
var _ sfxports.ClipRepositoryPort = (*sfxClipsRepoAdapter)(nil)

func (a *sfxClipsRepoAdapter) Upsert(ctx context.Context, clip *asset.Asset) error {
	return a.repo.Upsert(ctx, clip)
}

// ── Drive uploader adapter ───────────────────────────────────────────

// sfxDriveUploaderAdapter wraps drive.Admin to satisfy
// sfxports.DriveUploaderPort. FASE 9 Step 4 (June 2026): migrated
// from *drive.Uploader to drive.Admin (Pattern 0 port). Only the
// fields used by sfx are mapped; MD5Checksum + DownloadLink from
// concrete drive.UploadResult are deliberately dropped.

// F2.10: sfxDriveUploaderAdapter + its assertion + methods were
// retired (override brutal). The sfx capability no longer reaches
// into the legacy drive.Admin.UploadFile + GetOrCreateFolder surface;
// every Drive write from sfx routes through delivery.Publisher.Publish
// (DestinationSoundEffect). The matching sfxports.DriveUploaderPort
// interface was removed from
// internal/application/assets/soundeffect/ports.go in the same
// commit — the structural compatibility check
// `var _ sfxports.DriveUploaderPort = (*sfxDriveUploaderAdapter)(nil)`
// would now fail to compile because the interface is gone.
//
// (per `97e6b71a`-era deletion): see git log for the exact diff.

// ── Semantic writer adapter ──────────────────────────────────────────

// sfxSemanticWriterAdapter wraps *semantic.MetadataWriter to satisfy
// sfxports.SemanticMetadataWriterPort. Translates between the narrow
// sfxports.MetadataWriteRequest / MetadataWriteResponse DTOs and the
// concrete semantic.WriteRequest / *semantic.WriteResult (with inner
// *Payload).
type sfxSemanticWriterAdapter struct {
	w *semantic.MetadataWriter
}

// Compile-time assertion: sfxSemanticWriterAdapter satisfies sfxports.SemanticMetadataWriterPort.
var _ sfxports.SemanticMetadataWriterPort = (*sfxSemanticWriterAdapter)(nil)

func (a *sfxSemanticWriterAdapter) Write(ctx context.Context, req sfxports.MetadataWriteRequest) (*sfxports.MetadataWriteResponse, error) {
	concreteReq := semantic.WriteRequest{
		AssetID:   req.AssetID,
		AssetType: req.AssetType,
		MediaType: req.MediaType,
		Source:    req.Source,
		Generator: req.Generator,
		Style:     req.Style,
		Prompt:    req.Prompt,
		LocalPath: req.LocalPath,
	}
	res, err := a.w.Write(ctx, concreteReq)
	if err != nil {
		return nil, err
	}
	// Preserve the pre-refactor contract: a nil inner Payload must
	// short-circuit the handler's `else if writeRes != nil` branch
	// (which gates the Drive upload). Returning an empty non-nil DTO
	// here would let the upload proceed with default searchText/tags.
	if res == nil || res.Payload == nil {
		return nil, nil
	}
	return &sfxports.MetadataWriteResponse{
		SearchText: res.Payload.SearchText,
		Tags:       res.Payload.Tags,
	}, nil
}

// ── Resolver adapter ─────────────────────────────────────────────────

// sfxResolverAdapter wraps *drive.Resolver to satisfy
// sfxports.DestinationResolverPort. Only LocalPath is propagated (the
// handler drops RelativePath).
type sfxResolverAdapter struct {
	r *drive.Resolver
}

// Compile-time assertion: sfxResolverAdapter satisfies sfxports.DestinationResolverPort.
var _ sfxports.DestinationResolverPort = (*sfxResolverAdapter)(nil)

func (a *sfxResolverAdapter) Resolve(req sfxports.AssetDestinationRequest) (sfxports.ResolvedDest, error) {
	concreteReq := drive.AssetDestinationRequest{
		Source:    drive.SourceType(req.Source),
		MediaType: drive.MediaType(req.MediaType),
		Group:     req.Group,
		Hash:      req.Hash,
		Ext:       req.Ext,
	}
	res, err := a.r.Resolve(concreteReq)
	if err != nil {
		return sfxports.ResolvedDest{}, err
	}
	if res == nil {
		return sfxports.ResolvedDest{}, nil
	}
	return sfxports.ResolvedDest{LocalPath: res.LocalPath}, nil
}

// ── Sfx dispatcher adapter ──────────────────────────────────────────────────
//
// sfxDispatcherAdapter wraps the canonical *outbox.Dispatcher to satisfy
// sfxports.DispatcherPort. Outbox.Dispatcher.EnqueueAndIndex (the production
// implementation audited per the SSOT assertion at
// internal/infrastructure/database/sqlite/outbox/repository.go:720) accepts
// *asset.Asset + contentHash and atomically UPSERTs media_assets + emits the
// matching asset.index.requested outbox event in a single transaction.
//
// PR 6 (June 2026, codex/qdrant-api-writers-fail-closed): the sfx handler
// is migrated off direct h.clipsRepo.Upsert to dispatcher routing so the
// QDRANT-002 atomicity invariant (media_assets write and outbox event
// emit committed in one tx) applies uniformly to sfx asset writes.
//
// Signature-drift detection: any change to (a) sfxports.DispatcherPort's
// EnqueueAndIndex signature, OR (b) the outbox.Dispatcher's same method
// signature, surfaces as a build failure at the compile-time `var _` line
// below. AGENTS.md Pattern 0 convention — compile-time over runtime checks.
type sfxDispatcherAdapter struct {
	disp *outbox.Dispatcher
}

// Compile-time assertion: sfxDispatcherAdapter satisfies sfxports.DispatcherPort.
var _ sfxports.DispatcherPort = (*sfxDispatcherAdapter)(nil)

func newSfxDispatcherAdapter(disp *outbox.Dispatcher) sfxports.DispatcherPort {
	if disp == nil {
		return nil
	}
	return &sfxDispatcherAdapter{disp: disp}
}

func (a *sfxDispatcherAdapter) EnqueueAndIndex(ctx context.Context, clip *asset.Asset, contentHash string) error {
	if a == nil || a.disp == nil {
		return nil
	}
	return a.disp.EnqueueAndIndex(ctx, clip, contentHash)
}

// ── DriveAdminOps adapter ────────────────────────────────────────────────────// driveAdminAdapter wraps drive.Admin + drive.Reader and satisfies
// systemapi.DriveAdminOps. FASE 9 Step 4 (June 2026): migrated from
// *drive.Uploader to Pattern 0 ports. ResolveFileInfo now uses
// Reader.GetFileMeta (eliminates raw SDK access).
type driveAdminAdapter struct {
	admin  drive.Admin
	reader drive.Reader
	log    *zap.Logger
}

// newDriveAdminAdapter constructs the DriveAdminOps port adapter. If the
// underlying admin port is nil (e.g. Drive OAuth not configured), this
// returns nil so the handler-side `h.driveOps == nil` check fires the
// documented 503 "drive uploader not configured".
func newDriveAdminAdapter(admin drive.Admin, reader drive.Reader, log *zap.Logger) systemapi.DriveAdminOps {
	if admin == nil {
		return nil
	}
	return &driveAdminAdapter{admin: admin, reader: reader, log: log}
}

func (a *driveAdminAdapter) GetOrCreateFolder(ctx context.Context, folderName, parentID string) (string, error) {
	return a.admin.GetOrCreateFolder(ctx, folderName, parentID)
}

func (a *driveAdminAdapter) MoveFile(ctx context.Context, fileID, fromFolderID, toFolderID string) error {
	return a.admin.MoveFile(ctx, fileID, fromFolderID, toFolderID)
}

func (a *driveAdminAdapter) ListFiles(ctx context.Context, folderID string) ([]systemapi.DriveFileInfoDTO, error) {
	files, err := a.reader.ListFiles(ctx, folderID)
	if err != nil {
		return nil, err
	}
	out := make([]systemapi.DriveFileInfoDTO, len(files))
	for i, f := range files {
		out[i] = systemapi.DriveFileInfoDTO{
			ID:             f.ID,
			Name:           f.Name,
			MimeType:       f.MimeType,
			WebViewLink:    f.WebViewLink,
			WebContentLink: f.WebContentLink,
			Parents:        f.Parents,
		}
	}
	return out, nil
}

func (a *driveAdminAdapter) RenameFile(ctx context.Context, fileID, newName string) error {
	return a.admin.RenameFile(ctx, fileID, newName)
}

// ResolveFileInfo performs a metadata lookup via Reader.GetFileMeta
// (FASE 9: eliminates raw SDK *gdrive.Service access). The Reader
// returns *drive.FileMeta which carries all needed fields.
func (a *driveAdminAdapter) ResolveFileInfo(ctx context.Context, fileID string) (systemapi.ResolveByIDsItem, error) {
	if a.reader == nil {
		return systemapi.ResolveByIDsItem{}, fmt.Errorf("driveAdminAdapter: reader not wired")
	}
	meta, err := a.reader.GetFileMeta(ctx, fileID)
	if err != nil {
		if a.log != nil {
			a.log.Warn("system_adapters driveAdminAdapter.ResolveFileInfo: GetFileMeta failed",
				zap.String("file_id", fileID), zap.Error(err))
		}
		return systemapi.ResolveByIDsItem{}, err
	}
	if meta == nil {
		return systemapi.ResolveByIDsItem{}, nil
	}
	return systemapi.ResolveByIDsItem{
		ID:          meta.ID,
		Name:        meta.Name,
		MimeType:    meta.MimeType,
		Parents:     meta.Parents,
		WebViewLink: meta.WebViewLink,
		Size:        meta.Size,
		Trashed:     meta.Trashed,
	}, nil
}

// ── Reconciler (no-op placeholder) ────────────────────────────────────────────

// noopReconciler satisfies systemapi.Reconciler with a zero-result response.
// Previously this was backed by the now-removed drivecleanup.Service compatibility
// shim; the real Drive→SQLite reconciliation logic has not been implemented yet.
// This keeps the /drive/reconcile and /drive/cleanup endpoints functional
// (returning {deleted:0,kept:0}) while the feature is built out.
type noopReconciler struct{}

func (noopReconciler) Reconcile(_ context.Context, _, _ string, _ bool) (*systemapi.ReconcileResult, error) {
	return &systemapi.ReconcileResult{}, nil
}

// ── DoctorConfig snapshot factory ────────────────────────────────────────────

// doctorConfigFrom reads the diagnostic-relevant fields off *config.Config
// and packs them into a value-typed snapshot. Eager path resolution
// (AssetsPath(), ImagesPath(), TempDir() etc.) means the handler holds
// plain strings, not method receivers — easier to test, easier to fake.
// Returns the zero-value DoctorConfig if cfg is nil so callers don't need
// to nil-check before passing it into NewModule.
func doctorConfigFrom(cfg *config.Config) systemapi.DoctorConfig {
	if cfg == nil {
		return systemapi.DoctorConfig{}
	}
	return systemapi.DoctorConfig{
		DataDir:                   cfg.Storage.DataDir,
		AssetsPath:                cfg.Storage.AssetsPath(),
		ImagesPath:                cfg.Storage.ImagesPath(),
		TempPath:                  cfg.Storage.TempPath(),
		AnimationsPath:            cfg.Storage.AnimationsPath(),
		YoutubeClipsPath:          cfg.Storage.YoutubeClipsPath(),
		PythonScriptsDir:          cfg.Paths.PythonScriptsDir,
		GoogleAccountingEnabled:   cfg.GoogleAccounting.Enabled,
		GoogleAccountingServerURL: cfg.GoogleAccounting.ServerURL,
	}
}

// ── Compile-time assertions (AGENTS.md Pattern 0) ────────────────────────────

// Compile-time guarantees that each adapter satisfies the port it claims
// to satisfy. Drift in either signature surfaces at build time, not at
// runtime panic. Note the construction functions return interface types
// (not *T) so the assertions live on the concrete types below; if the
// port signature drifts, the assignment `wrap → interface` fails compile.
// ── Compile-time assertions (AGENTS.md Pattern 0) ────────────────────────────

// Compile-time guarantees that each adapter satisfies the port it claims
// to satisfy. Drift in either signature surfaces at build time, not at
// runtime panic. Note the construction functions return interface types
// (not *T) so the assertions live on the concrete types below; if the
// port signature drifts, the assignment `wrap → interface` fails compile.
var (
	_ systemapi.DriveAdminOps = (*driveAdminAdapter)(nil)
	_ systemapi.Reconciler    = (*noopReconciler)(nil)
	_ searchpkg.QueryEmbedder = (*searchEmbedAdapter)(nil)
)

// embeddingRegistryAdapter compile-time assertion (PR-EMBEDDING-CHANNEL-
// REGISTRY) — declared next to the embeddingRegistryAdapter struct
// declaration below so the assertion lives alongside its concrete type
// (the canonical Pattern 0 layout per the existing searchEmbedAdapter
// declaration comment convention).
var _ searchpkg.EmbeddingChannelRegistry = (*embeddingRegistryAdapter)(nil)

// ── Search query embedder adapter (Fase 6 Spina Dorsale) ───────────────────
//
// searchEmbedAdapter bridges the infrastructure-layer qdrant.TextEmbedder
// to the application-layer search.QueryEmbedder port. The shape is
// identical (`Embed(ctx, text) -> ([]float32, error)`), so the adapter
// is a one-method delegation.
//
// Until compositions-everywhere migration (BACKFILL phase of
// architecture/deprecations.yaml#SEARCH-VECTORSEARCHPORT-MERGE), the
// legacy mediator `mediasearch.VectorSearchPort` continues to expose
// EmbedTextForVector directly via the qdrant concrete. THIS adapter
// is the Fase 6 path: orchestrator code that asks for
// `search.QueryEmbedder` will get this adapter wrapped around the
// same qdrant.TextEmbedder concrete — zero behavioural drift, plus
// the canonical split.
type searchEmbedAdapter struct {
	embedder qdrant.TextEmbedder
}

// Embed delegates to the underlying qdrant.TextEmbedder. The method
// name matches the application port shape so compile-time assertion
// `var _ searchpkg.QueryEmbedder = (*searchEmbedAdapter)(nil)` is
// non-trivial — drift in either signature is a build failure.
func (a *searchEmbedAdapter) Embed(ctx context.Context, text string) ([]float32, error) {
	if a == nil || a.embedder == nil {
		return nil, fmt.Errorf("searchEmbedAdapter: underlying qdrant embedder not wired")
	}
	return a.embedder.Embed(ctx, text)
}

// newSearchEmbedAdapter is the canonical composition-root constructor.
// Returns a nil interface when embedder is nil so the wiring site
// preserves the `embedPort != nil` discipline callers can rely on.
func newSearchEmbedAdapter(embedder qdrant.TextEmbedder) searchpkg.QueryEmbedder {
	if embedder == nil {
		return nil
	}
	return &searchEmbedAdapter{embedder: embedder}
}

// ── Embedding channel registry adapter (PR-EMBEDDING-CHANNEL-REGISTRY, July 2026) ───
//
// Composition-only-seam concrete for search.EmbeddingChannelRegistry
// (Pattern 0). The semantic backend (search_backend_semantic.go)
// consumes this port instead of inlining 5-model switch logic; adding
// a new channel encoder (e.g. SigLIP-text for cross-modal visual search
// per PR-CROSS-MODAL-TEXT-TO-VISUAL, deadline 2026-08-15) plugs in at
// composition root without touching the backend.
//
// godlike/06 SSOT: the per-channel adapter set lives ENTIRELY here.
// internal/application/search owns the channel-name vocabulary
// (ChannelText / ChannelTranscript / ChannelVisual / ChannelAudio /
// ChannelSparse) and the typed-error contract (ErrChannelUnknown /
// ErrChannelNotConfigured / ErrChannelNotApplicable). This file owns
// the wiring of per-channel adapters at composition root only.
//
// Channel status (July 2026):
//
//	text        LIVE   routes to qdrant.TextEmbedder via textChannelEncoderAdapter.
//	transcript  LIVE   SAME adapter (transcript content is text).
//	visual      forward-pointer  SigLIP-text encoder (PR-CROSS-MODAL-TEXT-TO-VISUAL)
//	                     returns search.ErrChannelNotConfigured.
//	audio       forward-pointer  CLAP-text encoder (PR-CROSS-MODAL-TEXT-TO-VISUAL)
//	                     returns search.ErrChannelNotConfigured.
//	sparse      server-side BM25 inference on Qdrant (no Go-side encoder needed)
//	                     returns search.ErrChannelNotApplicable.
type embeddingRegistryAdapter struct {
	// adapters is keyed by canonical channel-name string. Each entry
	// is a search.ChannelEncoder that produces a channel-specific vector.
	// godlike/06 SSOT: the lookup table is the canonical source-of-truth
	// for which channels the composition root has wired.
	adapters map[string]searchpkg.ChannelEncoder
}

// newEmbeddingRegistryAdapter wires the 5 canonical channels at composition
// root. text+transcript map to a textChannelEncoderAdapter wrapping the
// passed qdrant.TextEmbedder; visual maps to a passed siglipEncoder (the
// PR-CROSS-MODAL-TEXT-TO-VISUAL live path); audio/sparse are forward-pointer
// stubs returning the documented godlike/07 typed-error sentinels. The
// returned registry is the single source-of-truth for the semantic
// backend's embedding surface; the backend fans out per channel via
// EmbedQuery.
//
// PR-CROSS-MODAL-TEXT-TO-VISUAL (August 2026): the signature gains a
// 2nd argument siglipEncoder (search.ChannelEncoder concrete): when
// non-nil, ChannelVisual becomes LIVE — text queries embedded via SigLIP
// land in the same 768d space as image-encoded video frames, enabling
// end-to-end "search the concept in the pixels". When siglipEncoder is
// nil (composition root deferred), ChannelVisual falls back to the
// notConfiguredAdapter typed-error carrier so the failure surfaces
// with the documented sentinel rather than a panic-nil dereference.
//
// textEmbedder nil-tolerance: when the underlying qdrant.TextEmbedder is
// not wired (composition root deferred), text + transcript channels ship
// as notConfiguredAdapter stubs so the failure surfaces with the documented
// sentinel rather than a panic-nil dereference.
func newEmbeddingRegistryAdapter(textEmbedder qdrant.TextEmbedder, siglipEncoder searchpkg.ChannelEncoder) searchpkg.EmbeddingChannelRegistry {
	adapters := make(map[string]searchpkg.ChannelEncoder, len(searchpkg.CanonicalChannelNames()))

	// text + transcript: same text-channel encoder; transcript content is text.
	if textEmbedder != nil {
		enc := &textChannelEncoderAdapter{textEmbedder: textEmbedder}
		adapters[searchpkg.ChannelText] = enc
		adapters[searchpkg.ChannelTranscript] = enc
	} else {
		adapters[searchpkg.ChannelText] = notConfiguredAdapter{}
		adapters[searchpkg.ChannelTranscript] = notConfiguredAdapter{}
	}

	// PR-CROSS-MODAL-TEXT-TO-VISUAL (August 2026): visual channel
	// routes to the live SigLIP text encoder when composition root
	// provides one. The encoder concrete ships at
	// internal/infrastructure/embeddings/siglip_text_embedder.go
	// and satisfies the canonical ChannelEncoder port (var _ assertion
	// at the SigLIPTextEmbedder struct declaration site). When
	// siglipEncoder is nil (composition root deferred — typical for tests
	// and for production when the sidecar Python embedding server is not
	// yet wired), ChannelVisual falls back to NotConfigured so the failure
	// surfaces with the documented sentinel.
	if siglipEncoder != nil {
		adapters[searchpkg.ChannelVisual] = siglipEncoder
	} else {
		adapters[searchpkg.ChannelVisual] = notConfiguredAdapter{}
	}

	// audio: forward-pointer stub (CLAP-text encoder from
	// PR-CROSS-MODAL-AUDIO forward-pointer — out of scope for the
	// visual-only PR-CROSS-MODAL-TEXT-TO-VISUAL). Slot is filled
	// by a CLAP-text encoder in the audio-specific cutover PR.
	adapters[searchpkg.ChannelAudio] = notConfiguredAdapter{}

	// sparse: forward-pointer stub returning search.ErrChannelNotApplicable
	// (TypedError contract: channel is RECOGNIZED but a TEXT query cannot
	// produce a sparse vector). Qdrant handles BM25 inference server-side
	// (see internal/infrastructure/qdrant/client_search.go::SparseText
	// + SparseVectorName pair semantics); no Go-side encoder needed for
	// the canonical Qdrant-backed path.
	adapters[searchpkg.ChannelSparse] = notApplicableAdapter{}

	return &embeddingRegistryAdapter{adapters: adapters}
}

// EmbedQuery is the godlike/07 typed-error contract:
//   - unknown channel   → ErrChannelUnknown (wrapped %w; programming error)
//   - empty text input  → ErrChannelUnknown (no embed nothing, fail closed)
//   - known but no adapter → ErrChannelNotConfigured (wrapped %w)
//   - sparse/visual/audio stub → ErrChannelNotApplicable or
//     ErrChannelNotConfigured (forward-pointer)
//
// The wrapped %w guarantees errors.Is(err, sentinel) round-trips through
// any caller of the semantic backend, so the operator dashboard can
// surface "channel X forward-pointer" vs "channel X ad-hoc" cleanly.
func (r *embeddingRegistryAdapter) EmbedQuery(ctx context.Context, channel string, text string) ([]float32, error) {
	if r == nil {
		return nil, fmt.Errorf("embeddingRegistryAdapter: registry not wired: %w", searchpkg.ErrChannelUnknown)
	}
	if !searchpkg.IsKnownChannel(channel) {
		return nil, fmt.Errorf("embeddingRegistryAdapter: channel %q: %w", channel, searchpkg.ErrChannelUnknown)
	}
	if text == "" {
		return nil, fmt.Errorf("embeddingRegistryAdapter: channel %q: empty text query: %w", channel, searchpkg.ErrChannelUnknown)
	}
	adapter, ok := r.adapters[channel]
	if !ok || adapter == nil {
		return nil, fmt.Errorf("embeddingRegistryAdapter: channel %q: %w", channel, searchpkg.ErrChannelNotConfigured)
	}
	return adapter.EmbedTextQuery(ctx, text)
}

// textChannelEncoderAdapter wraps the qdrant.TextEmbedder as a
// search.ChannelEncoder. Same shape (Embed(ctx, text) -> []float32, error);
// the adapter is a one-method delegation matching the canonical port.
type textChannelEncoderAdapter struct {
	textEmbedder qdrant.TextEmbedder
}

// EmbedTextQuery delegates to the underlying qdrant.TextEmbedder.
// Nil-tolerant: a nil underlying qdrant embedder returns a typed-error
// wrapped via %w so callers can errors.Is the canonical sentinel.
func (a *textChannelEncoderAdapter) EmbedTextQuery(ctx context.Context, text string) ([]float32, error) {
	if a == nil || a.textEmbedder == nil {
		return nil, fmt.Errorf("textChannelEncoderAdapter: underlying qdrant.TextEmbedder not wired: %w",
			searchpkg.ErrChannelNotConfigured)
	}
	return a.textEmbedder.Embed(ctx, text)
}

// notConfiguredAdapter is the godlike/07 typed-error carrier for
// channels that are RECOGNIZED but UNWIRED (visual + audio forward-pointers
// + the nil-textEmbedder fallback for text + transcript channels).
// Returns search.ErrChannelNotConfigured so callers can errors.Is
// the canonical sentinel directly without unwrapping. The receiver is
// empty struct (zero-cost) per the typed-error carrier convention.
type notConfiguredAdapter struct{}

func (notConfiguredAdapter) EmbedTextQuery(_ context.Context, _ string) ([]float32, error) {
	return nil, searchpkg.ErrChannelNotConfigured
}

// notApplicableAdapter is the godlike/07 typed-error carrier for
// channels that are RECOGNIZED but NOT encodable via a TEXT query.
// Today this is the sparse channel (Qdrant handles BM25 inference
// server-side via SparseText in HybridSearchRequest; no Go-side
// encoder needed). Empty struct (zero-cost) per typed-error carrier.
type notApplicableAdapter struct{}

func (notApplicableAdapter) EmbedTextQuery(_ context.Context, _ string) ([]float32, error) {
	return nil, searchpkg.ErrChannelNotApplicable
}

// artlistConfigAdapter wraps *config.Config to satisfy
// artlistPkg.ArtlistConfigPort. The composition-root keeps the
// config concrete; this adapter exposes only the narrow port
// surface to the api/ layer.
type artlistConfigAdapter struct {
	cfg *config.Config
}

// Compile-time assertion: artlistConfigAdapter satisfies artlistPkg.ArtlistConfigPort.
var _ artlistPkg.ArtlistConfigPort = (*artlistConfigAdapter)(nil)

// ArtlistRootFolderID resolves the canonical artlist root folder.
// Nil-tolerant: a nil underlying cfg returns "" matching the
// pre-refactor behaviour of artlist.ResolveRootFolderID(nil).
func (a *artlistConfigAdapter) ArtlistRootFolderID() string {
	return artlistPkg.ResolveRootFolderID(a.cfg)
}

// newArtlistConfigAdapter is the canonical composition-root constructor.
// Returns a nil interface when cfg is nil so the wiring site preserves
// the `cfgPort != nil` discipline callers can rely on (production
// wiring always passes a non-nil cfg).
func newArtlistConfigAdapter(cfg *config.Config) artlistPkg.ArtlistConfigPort {
	if cfg == nil {
		return nil
	}
	return &artlistConfigAdapter{cfg: cfg}
}

// middlewareRateLimitAdapter wraps *config.Config to satisfy
// middleware.RateLimitPort. Same one-method-per-call-site discipline.
type middlewareRateLimitAdapter struct {
	cfg *config.Config
}

// Compile-time assertion.
var _ middleware.RateLimitPort = (*middlewareRateLimitAdapter)(nil)

func newMiddlewareRateLimitAdapter(cfg *config.Config) middleware.RateLimitPort {
	if cfg == nil {
		return nil
	}
	return &middlewareRateLimitAdapter{cfg: cfg}
}

func (a *middlewareRateLimitAdapter) RateLimitEnabled() bool {
	if a.cfg == nil {
		return false
	}
	return a.cfg.Security.RateLimitEnabled
}

func (a *middlewareRateLimitAdapter) RateLimitRequests() int {
	if a.cfg == nil {
		return 0
	}
	return a.cfg.Security.RateLimitRequests
}

// middlewareFeatureFlagsAdapter wraps *config.Config to satisfy
// middleware.FeatureFlagsPort. The per-feature bools read on
// `cfg.Features.<X>Enabled` get one delegation method each so the
// future ScriptImagesEnabled flag lands cleanly without changing
// the port surface.
type middlewareFeatureFlagsAdapter struct {
	cfg *config.Config
}

// Compile-time assertion.
var _ middleware.FeatureFlagsPort = (*middlewareFeatureFlagsAdapter)(nil)

func newMiddlewareFeatureFlagsAdapter(cfg *config.Config) middleware.FeatureFlagsPort {
	if cfg == nil {
		return nil
	}
	return &middlewareFeatureFlagsAdapter{cfg: cfg}
}

func (a *middlewareFeatureFlagsAdapter) ArtlistEnabled() bool {
	if a.cfg == nil {
		return false
	}
	return a.cfg.Features.ArtlistEnabled
}

func (a *middlewareFeatureFlagsAdapter) ScriptDocsEnabled() bool {
	if a.cfg == nil {
		return false
	}
	return a.cfg.Features.ScriptDocsEnabled
}

func (a *middlewareFeatureFlagsAdapter) ScriptClipsEnabled() bool {
	if a.cfg == nil {
		return false
	}
	return a.cfg.Features.ScriptClipsEnabled
}
