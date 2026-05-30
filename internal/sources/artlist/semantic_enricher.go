package artlist

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	"velox/go-master/internal/media/clipindexer"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/media/semantic"
	"velox/go-master/internal/repository/clips"
)

// SemanticEnricher arricchisce un clip Artlist con metadati semantici.
// Usa il semantic_tagger.py per generare search_text, concept_tags, subjects, mood,
// e un embedding compatto (concept_tags serializzati come JSON) per la ricerca ibrida.
//
// L'enrichment viene eseguito in background dopo il salvataggio iniziale del clip,
// quindi non blocca mai la pipeline principale di download.
type SemanticEnricher struct {
	repo        *clips.Repository
	clipIndexer *clipindexer.Service
	scriptsDir  string
	ollamaURL   string
	ollamaModel string
	log         *zap.Logger
}

// NewSemanticEnricher crea un enricher pronto per il package artlist.
func NewSemanticEnricher(repo *clips.Repository, clipIndexer *clipindexer.Service, scriptsDir, ollamaURL, ollamaModel string, log *zap.Logger) *SemanticEnricher {
	return &SemanticEnricher{
		repo:        repo,
		clipIndexer: clipIndexer,
		scriptsDir:  scriptsDir,
		ollamaURL:   ollamaURL,
		ollamaModel: ollamaModel,
		log:         log,
	}
}

// EnrichAsync avvia l'enrichment in background (fire-and-forget).
// Usa un contesto fresco derivato da Background perché il contesto HTTP può essere già scaduto
// al momento dell'esecuzione del tagger (che ci può mettere qualche secondo).
func (e *SemanticEnricher) EnrichAsync(clip *models.MediaAsset, term string) {
	if clip == nil || clip.ID == "" {
		return
	}
	clipCopy := *clip // copia per sicurezza nella goroutine
	go func() {
		ctx := context.Background()
		if err := e.Enrich(ctx, &clipCopy, term); err != nil {
			e.log.Warn("artlist semantic enrichment failed",
				zap.String("clip_id", clipCopy.ID),
				zap.String("term", term),
				zap.Error(err),
			)
		}
	}()
}

// Enrich esegue il tagger e aggiorna il DB con i metadati semantici.
// Restituisce errore solo se il tagger stesso fallisce; aggiornamenti parziali
// sono tollerati (il clip è già salvato, il metadata è un bonus).
func (e *SemanticEnricher) Enrich(ctx context.Context, clip *models.MediaAsset, term string) error {
	if e.scriptsDir == "" {
		return fmt.Errorf("scripts dir not configured")
	}

	// Costruiamo un prompt ricco dal titolo + term di ricerca
	prompt := buildArtlistPrompt(clip.Name, term, clip.Tags)

	// Stile di default per stock footage
	style := "cinematic"

	e.log.Debug("enriching artlist clip semantically",
		zap.String("clip_id", clip.ID),
		zap.String("prompt_preview", truncate(prompt, 80)),
	)

	payload, err := semantic.Tagger(
		ctx,
		filepath.Clean(e.scriptsDir),
		prompt,
		style,
		"video", // media_type
		"artlist_scraper",
		e.ollamaURL,
		e.ollamaModel,
	)
	if err != nil {
		return fmt.Errorf("semantic.Tagger: %w", err)
	}

	// Ricarichiamo il clip dal DB per non sovrascrivere campi aggiornati nel frattempo
	existing, err := e.repo.GetClip(ctx, clip.ID)
	if err != nil || existing == nil {
		// Se non troviamo il clip usiamo quello in memoria
		existing = clip
	}

	// Patch dei campi semantici
	if payload.SearchTextExpanded != "" {
		existing.SearchText = payload.SearchTextExpanded
	} else if payload.SearchText != "" {
		existing.SearchText = payload.SearchText
	}

	// Aggiungi concept tags + subjects ai tags esistenti (deduplicati)
	existing.Tags = deduplicateStrings(append(existing.Tags, payload.Tags...))

	// Preserva SearchTerms (i termini di ricerca originali) e aggiungi subjects
	if payload.Subjects != nil {
		existing.SearchTerms = deduplicateStrings(append(existing.SearchTerms, payload.Subjects...))
	}

	// Embedding compatto: serializza concept_tags come JSON array.
	// Quando arriveremo a Qdrant/vettori reali, questo campo verrà sostituito
	// con vettori float. Per ora serve come fallback per FTS e per il campo
	// embedding_json già presente nel DB schema.
	if len(payload.ConceptTags) > 0 {
		embJSON, jsonErr := json.Marshal(payload.ConceptTags)
		if jsonErr == nil {
			existing.EmbeddingJSON = string(embJSON)
		}
	}

	// Metadati aggiuntivi nel metadata_json del clip
	if existing.Metadata == nil {
		existing.Metadata = make(map[string]any)
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
	existing.Metadata["semantic_confidence"] = payload.Confidence

	// Aggiorna il DB
	if err := e.repo.UpsertClip(ctx, existing); err != nil {
		return fmt.Errorf("upsert after enrichment: %w", err)
	}

	// Aggiorna anche il vector store (Qdrant) in tempo reale se configurato
	if e.clipIndexer != nil {
		e.clipIndexer.UpsertVectorStore(ctx, existing.ID)
	}

	e.log.Info("artlist clip enriched",
		zap.String("clip_id", existing.ID),
		zap.Int("tags_count", len(existing.Tags)),
		zap.String("search_text_preview", truncate(existing.SearchText, 60)),
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

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
