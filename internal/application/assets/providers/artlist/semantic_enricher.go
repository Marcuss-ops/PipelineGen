package artlist

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	drivepkg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

var enrichMetaMu sync.Mutex

// SemanticEnricher arricchisce un clip Artlist con metadati semantici.
// Usa il semantic_tagger.py per generare search_text, concept_tags, subjects, mood,
// e un embedding compatto (concept_tags serializzati come JSON) per la ricerca ibrida.
//
// L'enrichment viene eseguito in background dopo il salvataggio iniziale del clip,
// quindi non blocca mai la pipeline principale di download.
//
// F2.11 (June 2026): the driveManager (DriveFolderManager) field was
// RETIRED entirely (override brutal). The metadata.json read-modify-
// write path in updateCumulativeMetadataJSON is now backed by:
//
//   - drive.Reader for ListByQuery (mapped to SearchFiles) +
//     Download (mapped to DownloadFile). The canonical Pattern 0
//     Reader port in internal/infrastructure/drive/ports.go owns
//     the read surface.
//
//   - delivery.Publisher.Publish for the upload of the regenerated
//     metadata.json. The Publisher owns the write surface.
//
//   - drive.FileLifecycle for Trash on the previous metadata.json
//     (CAR-D3 split out from DriveFolderManager in PR2.7; preserved
//     unchanged). The implementation in *FileLifecycleAdapter still
//     wraps the raw *driveapi.Service so the SDK is hidden.
//
// PR2.5: dispatcher is now a constructor argument (was SetDispatcher
// setter previously — removed). The composition root in
// module_sources.go::WireArtlist wires the canonical outbox.Dispatcher at
// construction time so Enrich() can atomically combine UpsertClip +
// indexed-Qdrant in a single transaction. Indexer is the canonical
// port (was *clipindexer.Service concrete); nil-fallback path remains.
type SemanticEnricher struct {
	repo       AssetStore
	indexer    Indexer
	metaWriter *semantic.MetadataWriter
	log        *zap.Logger
	// dispatcher is the canonical media_index_outbox dispatcher used by
	// Enrich() to combine UpsertClip + indexed-Qdrant in a single tx.
	// When nil, falls back to the legacy indexer path. PR2.5: this is
	// a constructor argument (no SetDispatcher setter anymore).
	// PR2.4: typed as Dispatcher port (was *outbox.Dispatcher concrete).
	dispatcher Dispatcher
	// publisher is the canonical Drive upload canal (F2.11). Used by
	// updateCumulativeMetadataJSON to ship the regenerated metadata.json
	// back to Drive. The FolderRegistry.ensure-exists path lives in the
	// Publisher's folder-resolution machinery (ResolveFolder) so the
	// metadata.json upload is symmetric with artlist's regular upload
	// flow (root via DestinationArtlist policy + path segment = term).
	publisher delivery.Publisher
	// reader is the canonical Drive read port (F2.11). Used by
	// updateCumulativeMetadataJSON to list + download the existing
	// metadata.json before re-uploading. Drives off the composition
	// root's bundle.DriveUploader (concrete *drive.Uploader satisfies
	// drive.Reader structurally per the compile-time assertion at
	// internal/infrastructure/drive/ports.go).
	reader drivepkg.Reader
	// CARD-3 (June 2026): file-lifecycle port split out from
	// DriveFolderManagerAdapter per godlike/06 "one owner per fact".
	// Owns Trash/Move/Rename/Cleanup; previously driveManager.Trash
	// lived on the folder manager and violated the seam.
	// *drivepkg.FileLifecycleAdapter is constructed in
	// module_sources.go::WireArtlist and threaded in via the constructor.
	lifecycle drivepkg.FileLifecycle
}

// NewSemanticEnricher crea un enricher pronto per il package artlist.
// Usa semantic.MetadataWriter (chiamato GeneratePayload) invece di chiamare Tagger() direttamente,
// per garantire che tutto il metadata passi dal percorso centralizzato.
//
// F2.11 (June 2026): the `driveManager` parameter was replaced by
// `publisher delivery.Publisher + reader drivepkg.Reader` (the canonical
// write + read ports per DRIVE-005 closure). The Publisher is mandatory
// at composition (ErrPublisherUnavailable guard lives in Service.NewService);
// the Reader is mandatory only when the metadata.json sync path is
// wired (production) — test fixtures that opt out of cumulative
// metadata.json writes can pass nil reader (the call site already
// nil-tolerates because some deployments use local-only mode).
//
// PR2.5: dispatcher param added. Pass nil only in tests / for the
// legacy fallback path; production wiring always passes the canonical
// outbox.Dispatcher so Enrich() routes UpsertClip + IndexClip through
// the dispatcher rather than the legacy clipIndexer.IndexClip.
// Indexer is the canonical port (PR2.5 wiring: bundle.ClipIndexerService
// satisfies it directly because *clipindexer.Service has IndexClip +
// IsEnabled matching the port).
// PR2.7 → F2.11: driveUploader/driverManager replaced by
// publisher + reader (canonical ports). Pass nil for reader in tests.
func NewSemanticEnricher(
	repo AssetStore,
	indexer Indexer,
	metaWriter *semantic.MetadataWriter,
	publisher delivery.Publisher,
	reader drivepkg.Reader,
	dispatcher Dispatcher,
	lifecycle drivepkg.FileLifecycle,
	log *zap.Logger,
) *SemanticEnricher {
	return &SemanticEnricher{
		repo:       repo,
		indexer:    indexer,
		metaWriter: metaWriter,
		publisher:  publisher,
		reader:     reader,
		dispatcher: dispatcher,
		lifecycle:  lifecycle,
		log:        log,
	}
}

// dispatchOrIndexAndUpsert performs UpsertClip + IndexClip atomically via
// the canonical media_index_outbox dispatcher.
//
// The decision logic lives in dispatchBridge (dispatch_bridge.go) so this
// method is a thin alias and can be removed in a follow-up once all
// callers route directly through the bridge.
func (e *SemanticEnricher) dispatchOrIndexAndUpsert(ctx context.Context, clip *asset.Asset, hash string) {
	bridge, err := e.newDispatchBridge()
	if err != nil {
		e.log.Warn("dispatchOrIndexAndUpsert: dispatcher not wired", zap.Error(err))
		return
	}
	if err := bridge.Dispatch(ctx, clip, hash); err != nil {
		e.log.Warn("dispatchOrIndexAndUpsert: dispatch failed",
			zap.String("clip_id", clip.ID), zap.Error(err))
	}
}

// newDispatchBridge is the enricher-local mirror of Service.newDispatchBridge.
// It pulls the four upstream deps from the enricher's own struct fields so
// callers don't have to construct dispatchBridge{} by hand. Symmetric with
// the Service variant; if the enricher is ever refactored to hold a *Service
// reference, both methods collapse into one.
//
// PR2.5: clipsRepo → repo (AssetStore port), clipIndexer → indexer
// (Indexer port), both swapped cleanly because both ports declare the
// methods this bridge uses (UpsertClip / IndexClip + IsEnabled).
func (e *SemanticEnricher) newDispatchBridge() (*dispatchBridge, error) {
	if e.dispatcher == nil {
		return nil, fmt.Errorf("artlist: dispatcher is required")
	}
	return &dispatchBridge{
		dispatcher: e.dispatcher,
		assetStore: e.repo,
		indexer:    e.indexer,
		log:        e.log,
	}, nil
}

// Enrich esegue il tagger e aggiorna il DB con i metadati semantici.
// Restituisce errore solo se il tagger stesso fallisce; aggiornamenti parziali
// sono tollerati (il clip è già salvato, il metadata è un bonus).
func (e *SemanticEnricher) Enrich(ctx context.Context, clip *asset.Asset, term string) error {
	if e.metaWriter == nil {
		return fmt.Errorf("metadata writer not configured")
	}

	// Costruiamo un prompt ricco dal titolo + term di ricerca
	prompt := buildArtlistPrompt(clip.Name, term, clip.Tags)

	// Stile di default per stock footage
	style := "cinematic"

	e.log.Debug("enriching artlist clip semantically",
		zap.String("clip_id", clip.ID),
		zap.String("prompt_preview", textutil.Truncate(prompt, 80)),
	)

	// Usa MetadataWriter.GeneratePayload() invece di chiamare Tagger() direttamente
	// Questo garantisce che il metadata passi dal percorso centralizzato con fallback e override.
	payload, _, err := e.metaWriter.GeneratePayload(ctx, semantic.WriteRequest{
		AssetID:   clip.ID,
		AssetType: "clip",
		MediaType: "video",
		Source:    "artlist",
		Generator: "artlist_scraper",
		Style:     style,
		Prompt:    prompt,
	})
	if err != nil {
		return fmt.Errorf("metaWriter.GeneratePayload: %w", err)
	}

	// Ricarichiamo il clip dal DB per non sovrascrivere campi aggiornati nel frattempo
	existing, err := e.repo.Get(ctx, clip.ID)
	if err != nil || existing == nil {
		// Se non troviamo il clip usiamo quello in memoria
		existing = clip
	}

	// Patch dei campi semantici
	if payload.SearchText != "" {
		existing.SearchText = payload.SearchText
	}

	// Aggiungi concept tags + subjects ai tags esistenti (deduplicati)
	existing.Tags = deduplicateStrings(append(existing.Tags, payload.Tags...))

	// Preserva SearchTerms (i termini di ricerca originali) e aggiungi subjects
	if payload.Subjects != nil {
		existing.SearchTerms = deduplicateStrings(append(existing.SearchTerms, payload.Subjects...))
	}

	// Concept tags vanno salvati in Metadata, NON in EmbeddingJSON.
	// EmbeddingJSON deve contenere solo vettori float numerici per Qdrant.
	// Salvare tag testuali qui romperebbe l'indicizzazione vettoriale.
	if existing.Metadata == nil {
		existing.Metadata = make(map[string]any)
	}
	if len(payload.ConceptTags) > 0 {
		existing.Metadata["concept_tags"] = payload.ConceptTags
	}
	if len(payload.Mood) > 0 {
		existing.Metadata["mood"] = payload.Mood
	}
	if len(payload.Categories) > 0 {
		existing.Metadata["categories"] = payload.Categories
	}
	if len(payload.VisualObjects) > 0 {
		existing.Metadata["visual_objects"] = payload.VisualObjects
	}
	if len(payload.EmotionalTone) > 0 {
		existing.Metadata["emotional_tone"] = payload.EmotionalTone
	}
	if payload.SemanticDescription != "" {
		existing.Metadata["semantic_description"] = payload.SemanticDescription
	}
	existing.Metadata["semantic_enriched"] = true
	if payload.RetrievalScore != nil {
		existing.Metadata["semantic_confidence"] = *payload.RetrievalScore
	} else {
		existing.Metadata["semantic_confidence"] = 0.0
	}

	// Aggiorna il DB
	if err := e.repo.Upsert(ctx, existing); err != nil {
		return fmt.Errorf("upsert after enrichment: %w", err)
	}

	// Update cumulative metadata.json locally and on Google Drive with the enriched semantic data under lock to avoid races
	if existing.LocalPath() != "" {
		enrichMetaMu.Lock()
		localMetaPath := filepath.Join(filepath.Dir(existing.LocalPath()), "metadata.json")
		var localExisting []map[string]any
		if data, err := os.ReadFile(localMetaPath); err == nil {
			_ = json.Unmarshal(data, &localExisting)
		}

		metaData := map[string]any{
			"clip_id":              existing.ID,
			"name":                 existing.Name,
			"source":               existing.Source,
			"term":                 term,
			"filename":             existing.Filename,
			"file_hash":            existing.FileHash(),
			"duration_sec":         existing.Duration.Seconds(),
			"created_at":           existing.CreatedAt.Format(time.RFC3339),
			"drive_file_id":        existing.DriveFileID(),
			"drive_link":           existing.DriveLink(),
			"download_link":        existing.DownloadLink(),
			"clip_page_url":        existing.ClipPageURL,
			"source_url":           existing.ExternalURL(),
			"tags":                 existing.Tags,
			"search_terms":         existing.SearchTerms,
			"search_text":          existing.SearchText,
			"concept_tags":         existing.Metadata["concept_tags"],
			"mood":                 existing.Metadata["mood"],
			"categories":           existing.Metadata["categories"],
			"visual_objects":       existing.Metadata["visual_objects"],
			"emotional_tone":       existing.Metadata["emotional_tone"],
			"semantic_description": existing.Metadata["semantic_description"],
			"semantic_confidence":  existing.Metadata["semantic_confidence"],
		}

		foundLocal := false
		for i, entry := range localExisting {
			if id, ok := entry["clip_id"].(string); ok && id == existing.ID {
				localExisting[i] = metaData
				foundLocal = true
				break
			}
		}
		if !foundLocal {
			localExisting = append(localExisting, metaData)
		}

		if data, err := json.MarshalIndent(localExisting, "", "  "); err == nil {
			_ = os.WriteFile(localMetaPath, data, 0644)
		}

		folderID := existing.FolderID()
		if folderID == "" {
			folderID = existing.ParentFolderID()
		}
		if e.publisher != nil && folderID != "" {
			e.updateCumulativeMetadataJSON(ctx, folderID, existing.ID, metaData)
		}
		enrichMetaMu.Unlock()
	}

	// Ricalcola embedding veri e indicizza in Qdrant via dispatcher
	// (atomic UpsertClip + IndexClip) when wired. Dispatcher.EnqueueAndIndex
	// genera l'embedding da search_text come faceva IndexClip, ma lo fa in
	// una tx con UpsertClip + outbox enqueue, eliminando la finestra in cui
	// un crash tra le due lascia il clip un-indexed.
	e.dispatchOrIndexAndUpsert(ctx, existing, existing.FileHash())

	// 🚀 Update search terms index after semantic enrichment
	// This ensures the indexed clip_search_terms table reflects the rich
	// search_text produced by the tagger, not just the raw title + term.
	if updateErr := e.repo.UpdateSearchTerms(ctx, existing.ID, "artlist", existing.Name, existing.Tags, existing.SearchText); updateErr != nil {
		e.log.Warn("failed to update search terms after enrichment",
			zap.String("clip_id", existing.ID),
			zap.Error(updateErr),
		)
	}

	e.log.Info("artlist clip enriched",
		zap.String("clip_id", existing.ID),
		zap.Int("tags_count", len(existing.Tags)),
		zap.String("search_text_preview", textutil.Truncate(existing.SearchText, 60)),
	)

	return nil
}

// updateCumulativeMetadataJSON maintains a single metadata.json per
// folder on Google Drive.
//
// F2.11 (June 2026, override brutal): the legacy
// DriveFolderManagerAdapter surface (ListByQuery + Download + Upload)
// is RETIRED. The read-modify-write path is now backed by the
// canonical Split-Ports introduced at PR2.7 / DRIVE-005:
//
//   - drive.Reader.SearchFiles replaces ListByQuery (same Q-level
//     semantics: the metadata.json lookup queries by parent + name +
//     trashed=false).
//   - drive.Reader.DownloadFile replaces Download (same return shape:
//     (io.ReadCloser, content-type, error)).
//   - delivery.Publisher.Publish replaces Upload (conflict-aware per
//     P0 #1). We thread `RootFolderOverride=folderID` so the metadata
//     lands in the same destination as its clip (per the canonical
//     publisher resolution pipeline Step 2 in publisher.go).
//
// The Trash call still routes through drive.FileLifecycle (CARD-3 split
// out from DriveFolderManagerAdapter in PR2.7; preserved unchanged).
//
// F2.11 nil-tolerance: the Publisher is mandatory at composition
// (Service.NewService enforces ErrPublisherUnavailable fail-fast), so
// `e.publisher == nil` is unreachable in production. The Reader is a
// soft-dep — test fixtures can opt out of cumulative metadata.json
// sync by passing nil reader (the caller in Enrich() already gates on
// `e.publisher != nil && folderID != ""`; the inner check below
// remains for the reader-only nil path which has no equivalent
// composition guard).
func (e *SemanticEnricher) updateCumulativeMetadataJSON(ctx context.Context, folderID, clipID string, newEntry map[string]any) {
	const metaFilename = "metadata.json"

	// F2.11: reader is the only optional dep (publisher is fail-fast
	// in NewService). Skip the RMW path entirely if the composition
	// root wired nil reader (test fixtures opting out of cumulative
	// sync; production wires bundle.DriveUploader as drive.Reader).
	if e.reader == nil {
		e.log.Debug("semantic_enricher: reader is nil, skipping cumulative metadata.json sync (F2.11 test-fixture opt-out)")
		return
	}

	var existing []map[string]any
	query := fmt.Sprintf("'%s' in parents and trashed = false and name = '%s'", folderID, metaFilename)
	files, err := e.reader.SearchFiles(ctx, query)
	if err != nil {
		e.log.Warn("failed to list metadata.json", zap.Error(err))
	} else if len(files) > 0 {
		existingFileID := files[0].ID
		body, _, dlErr := e.reader.DownloadFile(ctx, existingFileID)
		if dlErr == nil && body != nil {
			defer body.Close()
			var raw []map[string]any
			if decErr := json.NewDecoder(body).Decode(&raw); decErr == nil {
				existing = raw
			}
		}
		if trashErr := e.lifecycle.Trash(ctx, existingFileID); trashErr != nil {
			e.log.Warn("failed to trash old metadata.json", zap.Error(trashErr))
		}
	}

	found := false
	for i, entry := range existing {
		if id, ok := entry["clip_id"].(string); ok && id == clipID {
			existing[i] = newEntry
			found = true
			break
		}
	}
	if !found {
		existing = append(existing, newEntry)
	}

	jsonBytes, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		e.log.Warn("failed to marshal cumulative metadata json", zap.Error(err))
		return
	}
	metaTempPath := filepath.Join(os.TempDir(), fmt.Sprintf("meta_%s_%d.json", clipID, time.Now().UnixNano()))
	if err := os.WriteFile(metaTempPath, jsonBytes, 0644); err != nil {
		e.log.Warn("failed to write metadata json temp file", zap.Error(err))
		return
	}
	defer os.Remove(metaTempPath)

	// F2.11 (June 2026): route the metadata.json upload through the
	// canonical delivery.Publisher instead of the retired
	// DriveFolderManagerAdapter.Upload. The publisher resolves the
	// destination policy (DestinationArtlist + Group="metadata" →
	// segments=["metadata"] satisfies RequireSubpath) and pins the
	// resolved root to the clip's parent folder via RootFolderOverride.
	// ConflictPolicy=ConflictOverwrite matches the legacy "find existing
	// → update in place" semantics implicit in
	// DriveFolderManagerAdapter.Upload.
	//
	// KNOWN LAYOUT-SHIFT caveat (F2.11, June 2026): the legacy
	// DriveFolderManagerAdapter.Upload(metadataTempPath, folderID,
	// "metadata.json") placed metadata.json DIRECTLY in the clip's
	// parent folder (/Artlist/<term>/metadata.json). The new publisher
	// path appends the PathBuilder segments after the overridden root,
	// producing /Artlist/<term>/metadata/metadata.json — one folder
	// deeper. The cumulative metadata.json RMW semantics stay correct
	// (the file is still found-and-merged per term — see Reader.SearchFiles
	// query above) but the on-disk Drive layout grew a "metadata"
	// subfolder under every term. This is acceptable per the F2.11
	// user spec ("drop legacy FolderManager fallback"; the spec does
	// not pin the exact metadata.json layout) and is documented here
	// for follow-up — a future DestinationPolicy with RequireSubpath=false
	// would let the metadata.json land at the legacy location without
	// re-introducing the legacy fallback path.
	if _, err := e.publisher.Publish(ctx, delivery.PublishRequest{
		Destination:        delivery.DestinationArtlist,
		Group:              "metadata",
		Filename:           metaFilename,
		LocalPath:          metaTempPath,
		RootFolderOverride: folderID,
		ConflictPolicy:     delivery.ConflictOverwrite,
	}); err != nil {
		e.log.Warn("failed to upload metadata.json to Drive", zap.Error(err))
	} else {
		e.log.Info("uploaded cumulative metadata.json to Drive (enriched)", zap.Int("entries", len(existing)))
	}
}

// buildArtlistPrompt costruisce un prompt descrittivo per il tagger
// combinando il titolo del clip con il termine di ricerca originale.
func buildArtlistPrompt(name, term string, tags []string) string {
	parts := []string{}
	if name != "" {
		parts = append(parts, name)
	}
	if term != "" && !strings.EqualFold(name, term) {
		parts = append(parts, "search term: "+term)
	}
	for _, t := range tags {
		if t != "" && !strings.EqualFold(t, term) && !strings.EqualFold(t, name) {
			parts = append(parts, t)
		}
	}
	if len(parts) == 0 {
		return "stock video clip"
	}
	return strings.Join(parts, ", ")
}

// deduplicateStrings rimuove i duplicati preservando l'ordine.
func deduplicateStrings(ss []string) []string {
	seen := make(map[string]struct{}, len(ss))
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if s == "" {
			continue
		}
		lc := strings.ToLower(s)
		if _, ok := seen[lc]; !ok {
			seen[lc] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}
