package scriptdocs

import (
	"context"
	"fmt"
	"sync"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// CanonicalCategory rappresenta una "destinazione" visiva nel tuo archivio.
type CanonicalCategory struct {
	ID          string
	Description string // Descrizione ricca per l'embedding
	Embedding   []float32
}

// SemanticRegistry gestisce la mappatura "Open-World" tra frasi e cartelle.
type SemanticRegistry struct {
	categories []CanonicalCategory
	scorer     *SemanticScorer
	initialized bool
	mu         sync.RWMutex
}

// NewSemanticRegistry crea il registro con le categorie base del tuo archivio.
func NewSemanticRegistry(scorer *SemanticScorer) *SemanticRegistry {
	return &SemanticRegistry{
		scorer: scorer,
		categories: []CanonicalCategory{
			{ID: "gym", Description: "weightlifting, bodybuilding, training in a fitness club, gym equipment, athletes working out"},
			{ID: "boxing", Description: "professional boxing, punching bags, boxing ring, gloves, fighter sparring, knockout, combat sports"},
			{ID: "city", Description: "urban landscape, busy streets, skyscrapers, traffic, downtown area, metropolitan life"},
			{ID: "nature", Description: "mountains, forests, rivers, beautiful landscapes, outdoor wilderness, environment"},
			{ID: "technology", Description: "digital screens, computers, coding, futuristic tech, servers, electronics, artificial intelligence"},
			{ID: "rural", Description: "farmland, countryside, simple life, agriculture, villages, rustic scenery, horses and carriages"},
			{ID: "arena", Description: "large stadium, crowd cheering, concert venue, public event, audience atmosphere"},
			{ID: "media", Description: "press conference, microphones, reporters, journalism, news broadcast, interview"},
			{ID: "luxury", Description: "expensive cars, private jets, money, jewelry, wealth, high-end lifestyle"},
			{ID: "failure", Description: "sadness, depression, rainy window, defeat, loneliness, dark atmosphere"},
		},
	}
}

// Initialize genera gli embedding per le categorie. Va chiamato al bootstrap.
func (r *SemanticRegistry) Initialize(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.initialized {
		return nil
	}

	logger.Info("Initializing Semantic Registry embeddings...", zap.Int("categories", len(r.categories)))
	for i := range r.categories {
		embed, err := r.scorer.getEmbedding(ctx, r.categories[i].Description)
		if err != nil {
			return fmt.Errorf("failed to embed category %s: %w", r.categories[i].ID, err)
		}
		r.categories[i].Embedding = embed
	}

	r.initialized = true
	logger.Info("Semantic Registry initialized successfully")
	return nil
}

// DiscoverCategory trova la categoria più vicina a una frase.
func (r *SemanticRegistry) DiscoverCategory(ctx context.Context, phrase string) (string, float32) {
	r.mu.RLock()
	if !r.initialized {
		r.mu.RUnlock()
		return "", 0
	}
	r.mu.RUnlock()

	phraseEmbed, err := r.scorer.getEmbedding(ctx, phrase)
	if err != nil {
		return "", 0
	}

	var bestID string
	var maxSim float32 = -1.0

	for _, cat := range r.categories {
		sim := cosineSimilarity(phraseEmbed, cat.Embedding)
		if sim > maxSim {
			maxSim = sim
			bestID = cat.ID
		}
	}

	return bestID, maxSim
}
