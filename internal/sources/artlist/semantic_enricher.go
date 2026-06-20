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

	"github.com/Marcuss-ops/PipelineGen/internal/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/media/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/media/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/upload/drive"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

var enrichMetaMu sync.Mutex

// SemanticEnricher arricchisce un clip Artlist con metadati semantici.
// Usa il semantic_tagger.py per generare search_text, concept_tags, subjects, mood,
// e un embedding compatto (concept_tags serializzati come JSON) per la ricerca ibrida.
//
// L'enrichment viene eseguito in background dopo il salvataggio iniziale del clip,
// quindi non blocca mai la pipeline principale di download.
type SemanticEnricher struct {
	repo          *sqlite.ClipsRepository
	clipIndexer   *clipindexer.Service
	metaWriter    *semantic.MetadataWriter
	driveUploader *drive.Uploader
	log           *zap.Logger
	// dispatcher is the canonical media_index_outbox dispatcher used by
	// Enrich() to combine UpsertClip + indexed-Qdrant in a single tx.
	// When nil, falls back to the legacy clipIndexer.IndexClip path.
	dispatcher *outbox.Dispatcher
}

// NewSemanticEnricher crea un enricher pronto per il package artlist.
// Usa semantic.MetadataWriter (chiamato GeneratePayload) invece di chiamare Tagger() direttamente,
// per garantire che tutto il metadata passi dal percorso centralizzato.
func NewSemanticEnricher(repo *sqlite.ClipsRepository, clipIndexer *clipindexer.Service, metaWriter *semantic.MetadataWriter, driveUploader *drive.Uploader, log *zap.Logger) *SemanticEnricher {
	return &SemanticEnricher{
		repo:          repo,
		clipIndexer:   clipIndexer,
		metaWriter:    metaWriter,
		driveUploader: driveUploader,
		log:           log,
	}
}

// EnrichAsync avvia l'enrichment in background (fire-and-forget).
// Usa context.WithoutCancel per preservare il tracing anche dopo che
// il contesto HTTP è scaduto, ma con un timeout proprio per evitare leak.
func (e *SemanticEnricher) EnrichAsync(parentCtx context.Context, clip *assets.Asset, term string) {
	if clip == nil || clip.ID == "" {
		return
	}
	clipCopy := *clip // copia per sicurezza nella goroutine
	concurrent.SafeGo("artlist-enrich", func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(parentCtx), 30*time.Second)
		defer cancel()
		if err := e.Enrich(ctx, &clipCopy, term); err != nil {
			e.log.Warn("artlist semantic enrichment failed",
				zap.String("clip_id", clipCopy.ID),
				zap.String("term", term),
				zap.Duration("timeout", 30*time.Second),
				zap.Error(err),
			)
		}
	})
}

// SetDispatcher injects the canonical media_index_outbox dispatcher.
// Falls back to legacy clipIndexer.IndexClip path when nil. Mirrors
// the Service.SetDispatcher contract.
func (e *SemanticEnricher) SetDispatcher(d *outbox.Dispatcher) {
	e.dispatcher = d
}

// dispatchOrIndexAndUpsert performs UpsertClip + IndexClip atomically via
// the canonical media_index_outbox dispatcher when wired, otherwise falls
// back to the legacy (UpsertClip + clipIndexer.IndexClip) pair.
//
// The decision logic lives in dispatchBridge (dispatch_bridge.go) so this
// method is a thin alias and can be removed in a follow-up once all
// callers route directly through the bridge.
func (e *SemanticEnricher) dispatchOrIndexAndUpsert(ctx context.Context, clip *assets.Asset, hash string) {
	e.newDispatchBridge().EnqueueOrFallback(ctx, clip, hash)
}

// newDispatchBridge is the enricher-local mirror of Service.newDispatchBridge.
// It pulls the four upstream deps from the enricher's own struct fields so
// callers don't have to construct dispatchBridge{} by hand. Symmetric with
// the Service variant; if the enricher is ever refactored to hold a *Service
// reference, both methods collapse into one.
func (e *SemanticEnricher) newDispatchBridge() *dispatchBridge {
	return &dispatchBridge{
		dispatcher:  e.dispatcher,
		clipsRepo:   e.repo,
		clipIndexer: e.clipIndexer,
		log:         e.log,
	}
}

// Enrich esegue il tagger e aggiorna il DB con i metadati semantici.
// Restituisce errore solo se il tagger stesso fallisce; aggiornamenti parziali
// sono tollerati (il clip è già salvato, il metadata è un bonus).
func (e *SemanticEnricher) Enrich(ctx context.Context, clip *assets.Asset, term string) error {
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
		if e.driveUploader != nil && folderID != "" {
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

// updateCumulativeMetadataJSON maintains a single metadata.json per folder on Google Drive.
func (e *SemanticEnricher) updateCumulativeMetadataJSON(ctx context.Context, folderID, clipID string, newEntry map[string]any) {
	const metaFilename = "metadata.json"

	if e.driveUploader == nil || e.driveUploader.Service == nil {
		return
	}

	var existing []map[string]any
	query := fmt.Sprintf("'%s' in parents and trashed = false and name = '%s'", folderID, metaFilename)
	list, err := e.driveUploader.Service.Files.List().Q(query).Fields("files(id, name)").Context(ctx).Do()
	if err != nil {
		e.log.Warn("failed to list metadata.json", zap.Error(err))
	} else if len(list.Files) > 0 {
		existingFileID := list.Files[0].Id
		body, _, dlErr := e.driveUploader.DownloadFile(ctx, existingFileID)
		if dlErr == nil && body != nil {
			defer body.Close()
			var raw []map[string]any
			if decErr := json.NewDecoder(body).Decode(&raw); decErr == nil {
				existing = raw
			}
		}
		if err := e.driveUploader.TrashFile(ctx, existingFileID); err != nil {
			e.log.Warn("failed to trash old metadata.json", zap.Error(err))
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

	if _, err := e.driveUploader.UploadFile(ctx, metaTempPath, folderID, metaFilename); err != nil {
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
