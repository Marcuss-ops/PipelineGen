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

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
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
// PR2.5: dispatcher is now a constructor argument (was SetDispatcher
// setter previously — removed). The composition root in
// module_sources.go::WireArtlist wires the canonical outbox.Dispatcher at
// construction time so Enrich() can atomically combine UpsertClip +
// indexed-Qdrant in a single transaction. Indexer is the canonical
// port (was *clipindexer.Service concrete); nil-fallback path remains.
// PR2.7: driveUploader (*drive.Uploader concrete) is replaced by
// driveManager (DriveFolderManager port). The port hides the SDK so
// updateCumulativeMetadataJSON can no longer reach through the
// concrete to call raw Files.List/Trash/Download/Create methods.
type SemanticEnricher struct {
	repo         AssetStore
	indexer      Indexer
	metaWriter   *semantic.MetadataWriter
	driveManager DriveFolderManager
	log          *zap.Logger
	// dispatcher is the canonical media_index_outbox dispatcher used by
	// Enrich() to combine UpsertClip + indexed-Qdrant in a single tx.
	// When nil, falls back to the legacy indexer path. PR2.5: this is
	// a constructor argument (no SetDispatcher setter anymore).
	// PR2.4: typed as Dispatcher port (was *outbox.Dispatcher concrete).
	dispatcher Dispatcher
}

// NewSemanticEnricher crea un enricher pronto per il package artlist.
// Usa semantic.MetadataWriter (chiamato GeneratePayload) invece di chiamare Tagger() direttamente,
// per garantire che tutto il metadata passi dal percorso centralizzato.
//
// PR2.5: dispatcher param added. Pass nil only in tests / for the
// legacy fallback path; production wiring always passes the canonical
// outbox.Dispatcher so Enrich() routes UpsertClip + IndexClip through
// the dispatcher rather than the legacy clipIndexer.IndexClip.
// Indexer is the canonical port (PR2.5 wiring: bundle.ClipIndexerService
// satisfies it directly because *clipindexer.Service has IndexClip +
// IsEnabled matching the port).
// PR2.7: driveUploader param replaced by driveManager
// (DriveFolderManager port). Pass nil for tests; production wiring
// always passes the adapter constructed in module_sources.go::WireArtlist.
func NewSemanticEnricher(
	repo AssetStore,
	indexer Indexer,
	metaWriter *semantic.MetadataWriter,
	driveManager DriveFolderManager,
	dispatcher Dispatcher,
	log *zap.Logger,
) *SemanticEnricher {
	return &SemanticEnricher{
		repo:         repo,
		indexer:      indexer,
		metaWriter:   metaWriter,
		driveManager: driveManager,
		dispatcher:   dispatcher,
		log:          log,
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
		if e.driveManager != nil && folderID != "" {
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
// folder on Google Drive. PR2.7: this function no longer reaches
// through to raw Drive SDK calls. All 4 operations (List, Download,
// Trash, Upload) go through the DriveFolderManager port so the
// application layer stays decoupled from google.golang.org/api/drive/v3.
// Nil-tolerance: callers (test fixtures) can pass nil for driveManager
// to opt out of Drive sync entirely.
func (e *SemanticEnricher) updateCumulativeMetadataJSON(ctx context.Context, folderID, clipID string, newEntry map[string]any) {
	const metaFilename = "metadata.json"

	if e.driveManager == nil {
		return
	}

	var existing []map[string]any
	query := fmt.Sprintf("'%s' in parents and trashed = false and name = '%s'", folderID, metaFilename)
	files, err := e.driveManager.ListByQuery(ctx, query)
	if err != nil {
		e.log.Warn("failed to list metadata.json", zap.Error(err))
	} else if len(files) > 0 {
		existingFileID := files[0].ID
		body, _, dlErr := e.driveManager.Download(ctx, existingFileID)
		if dlErr == nil && body != nil {
			defer body.Close()
			var raw []map[string]any
			if decErr := json.NewDecoder(body).Decode(&raw); decErr == nil {
				existing = raw
			}
		}
		if trashErr := e.driveManager.Trash(ctx, existingFileID); trashErr != nil {
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

	if _, err := e.driveManager.Upload(ctx, metaTempPath, folderID, metaFilename); err != nil {
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
