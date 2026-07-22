package artlist

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// Enrich arricchisce un clip Artlist con metadati semantici.
// Il tagger AI è facoltativo: quando è assente o disabilitato, l'enricher
// costruisce comunque il search_text deterministicamente tramite il
// SearchDocumentBuilder condiviso.
func (e *SemanticEnricher) Enrich(ctx context.Context, clip *asset.Asset, term string) error {
	if e.repo == nil {
		return fmt.Errorf("asset repository not configured")
	}

	// Ricarichiamo il clip dal DB per non sovrascrivere campi aggiornati nel frattempo
	existing, err := e.repo.Get(ctx, clip.ID)
	if err != nil || existing == nil {
		// Se non troviamo il clip usiamo quello in memoria
		existing = clip
	}

	// Tagger AI: facoltativo. Se presente, arricchisce i metadati ma NON
	// contamina più i tags canonici né genera direttamente il search_text.
	if e.metaWriter != nil {
		// Costruiamo un prompt ricco dal titolo + term di ricerca
		prompt := buildArtlistPrompt(existing.Name, term, existing.Tags)

		// Stile di default per stock footage
		style := "cinematic"

		e.log.Debug("enriching artlist clip semantically",
			zap.String("clip_id", existing.ID),
			zap.String("prompt_preview", textutil.Truncate(prompt, 80)),
		)

		// Usa MetadataWriter.GeneratePayload() invece di chiamare Tagger() direttamente
		// Questo garantisce che il metadata passi dal percorso centralizzato con fallback e override.
		payload, _, err := e.metaWriter.GeneratePayload(ctx, semantic.WriteRequest{
			AssetID:   existing.ID,
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

		// Preserva SearchTerms (i termini di ricerca originali) e aggiungi subjects
		if payload.Subjects != nil {
			existing.SearchTerms = deduplicateStrings(append(existing.SearchTerms, payload.Subjects...))
		}
	}

	// Ricostruisci i tags solo dalle fonti certificate (ProviderTags,
	// VLMTags, ManualTags, TranscriptTags). Non aggiungere più i tags
	// sintetici del tagger AI ai tags canonici.
	existing.RebuildTags()

	// Segna l'asset come arricchito indipendentemente dal tagger AI.
	if existing.Metadata == nil {
		existing.Metadata = make(map[string]any)
	}
	existing.Metadata["semantic_enriched"] = true

	// Costruisci il search_text deterministicamente dal documento di ricerca.
	if e.searchDocBuilder != nil {
		searchText, buildErr := e.searchDocBuilder.Build(ctx, *existing)
		if buildErr != nil {
			return fmt.Errorf("searchDocBuilder.Build: %w", buildErr)
		}
		existing.SearchText = searchText
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
