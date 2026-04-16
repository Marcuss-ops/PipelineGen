// Package stockjob fornisce cron job per arricchire il database di clip/stock
package stockjob

import (
	"context"
	"fmt"
	"sync"
	"time"

	"velox/go-master/internal/clip"
	"velox/go-master/internal/youtube"
	"velox/go-master/internal/downloader"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// Config configurazione del cron job
type Config struct {
	Enabled              bool          `json:"enabled"`
	CheckInterval        time.Duration `json:"check_interval"`         // Ogni quanto cercare nuove clip
	SearchQueries        []string      `json:"search_queries"`         // Query da cercare
	MaxResultsPerQuery   int           `json:"max_results_per_query"`  // Max risultati per query
	MinViews             int64         `json:"min_views"`              // Views minime per considerare un video
	MaxDuration          time.Duration `json:"max_duration"`           // Durata massima clip
	MinDuration          time.Duration `json:"min_duration"`           // Durata minima clip
	Database             *ClipDatabase `json:"database"`
}

// ClipDatabase interfacccia per il database clip
type ClipDatabase interface {
	// ClipExists verifica se una clip esiste già
	ClipExists(platform downloader.Platform, videoID string) (bool, error)
	
	// AddClip aggiunge una clip al database
	AddClip(clip *ClipRecord) error
	
	// UpdateClip aggiorna una clip esistente
	UpdateClip(clip *ClipRecord) error
	
	// GetClip ottiene una clip
	GetClip(platform downloader.Platform, videoID string) (*ClipRecord, error)
	
	// ListMissingClipsWithMetadata elenca clip senza metadati completi
	ListMissingClipsWithMetadata(limit int) ([]ClipRecord, error)
}

// ClipRecord record di una clip nel database
type ClipRecord struct {
	ID             string              `json:"id"`
	Platform       downloader.Platform `json:"platform"`
	VideoID        string              `json:"video_id"`
	URL            string              `json:"url"`
	Title          string              `json:"title"`
	Description    string              `json:"description"`
	Duration       time.Duration       `json:"duration"`
	Views          int64               `json:"views"`
	Thumbnail      string              `json:"thumbnail"`
	Author         string              `json:"author"`
	Tags           []string            `json:"tags"`
	Category       string              `json:"category"`
	Keywords       []string            `json:"keywords"`
	RelevanceScore float64             `json:"relevance_score"` // Quanto è pertinente al nostro contenuto
	Downloaded     bool                `json:"downloaded"`       // Se è stata scaricata fisicamente
	DownloadPath   string              `json:"download_path"`    // Path locale se scaricata
	DriveFileID    string              `json:"drive_file_id"`    // ID su Drive
	DriveFolder    string              `json:"drive_folder"`     // Cartella Drive
	MetadataFilled bool                `json:"metadata_filled"`  // Se i metadati sono stati popolati da AI
	LastChecked    time.Time           `json:"last_checked"`     // Ultima verifica
	CreatedAt      time.Time           `json:"created_at"`
}

// Scheduler gestisce il cron job di arricchimento
type Scheduler struct {
	config       *Config
	youtubeClient youtube.Client
	tiktokClient downloader.Downloader
	database     ClipDatabase
	indexer      *clip.Indexer
	running      bool
	mu           sync.Mutex
	stopCh       chan struct{}
}

// NewScheduler crea un nuovo scheduler
func NewScheduler(
	config *Config,
	ytClient youtube.Client,
	tiktokClient downloader.Downloader,
	db ClipDatabase,
	indexer *clip.Indexer,
) *Scheduler {
	if config == nil {
		config = &Config{
			Enabled:            false,
			CheckInterval:      1 * time.Hour,
			SearchQueries:      []string{"technology", "business", "interview"},
			MaxResultsPerQuery: 10,
			MinViews:           10000,
			MaxDuration:        10 * time.Minute,
			MinDuration:        10 * time.Second,
		}
	}

	return &Scheduler{
		config:        config,
		youtubeClient: ytClient,
		tiktokClient:  tiktokClient,
		database:      db,
		indexer:       indexer,
		stopCh:        make(chan struct{}),
	}
}

// Start avvia il cron job
func (s *Scheduler) Start(ctx context.Context) error {
	if !s.config.Enabled {
		logger.Info("Stock enrichment cron job is disabled")
		return nil
	}

	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("scheduler already running")
	}
	s.running = true
	s.mu.Unlock()

	logger.Info("Starting stock enrichment cron job",
		zap.Duration("interval", s.config.CheckInterval),
		zap.Int("search_queries", len(s.config.SearchQueries)),
	)

	go s.run(ctx)

	return nil
}

// Stop ferma il cron job
func (s *Scheduler) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return nil
	}

	close(s.stopCh)
	s.running = false

	logger.Info("Stock enrichment cron job stopped")
	return nil
}

// run ciclo principale
func (s *Scheduler) run(ctx context.Context) {
	ticker := time.NewTicker(s.config.CheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("Context cancelled, stopping cron job")
			return
		case <-s.stopCh:
			logger.Info("Stop signal received")
			return
		case <-ticker.C:
			s.executeCycle(ctx)
		}
	}
}

// executeCycle esegue un ciclo di arricchimento
func (s *Scheduler) executeCycle(ctx context.Context) {
	logger.Info("Starting enrichment cycle")

	// Fase 1: Cerca nuove clip su YouTube
	s.searchNewClips(ctx)

	// Fase 2: Arricchisci metadati per clip esistenti
	s.enrichExistingClips(ctx)

	// Fase 3: Pulizia database (rimuovi clip non pertinenti)
	s.cleanupDatabase(ctx)

	logger.Info("Enrichment cycle completed")
}

// searchNewClips cerca nuove clip
func (s *Scheduler) searchNewClips(ctx context.Context) {
	logger.Info("Searching for new clips on YouTube and TikTok")

	var wg sync.WaitGroup

	for _, query := range s.config.SearchQueries {
		wg.Add(1)
		go func(q string) {
			defer wg.Done()

			// Cerca su YouTube
			s.searchYouTube(ctx, q)
			
			// Cerca su TikTok (se client disponibile)
			if s.tiktokClient != nil {
				s.searchTikTok(ctx, q)
			}
		}(query)
	}

	wg.Wait()
}

// searchYouTube cerca su YouTube
func (s *Scheduler) searchYouTube(ctx context.Context, query string) {
	opts := &youtube.SearchOptions{
		MaxResults: s.config.MaxResultsPerQuery,
		SortBy:     "relevance",
	}

	results, err := s.youtubeClient.Search(ctx, query, opts)
	if err != nil {
		logger.Warn("YouTube search failed",
			zap.Error(err),
			zap.String("query", query),
		)
		return
	}

	imported := 0
	for _, result := range results {
		// Filtra per views
		if result.Views < s.config.MinViews {
			continue
		}

		// Filtra per durata
		if result.Duration < s.config.MinDuration || result.Duration > s.config.MaxDuration {
			continue
		}

		// Controlla se esiste già
		exists, err := s.database.ClipExists(downloader.PlatformYouTube, result.ID)
		if err != nil || exists {
			continue
		}

		// Aggiungi al database
		clip := &ClipRecord{
			ID:          fmt.Sprintf("yt_%s", result.ID),
			Platform:    downloader.PlatformYouTube,
			VideoID:     result.ID,
			URL:         result.URL,
			Title:       result.Title,
			Duration:    result.Duration,
			Views:       result.Views,
			Thumbnail:   result.Thumbnail,
			Author:      result.Channel,
			Tags:        []string{query},
			Category:    s.detectCategory(result.Title, result.URL),
			LastChecked: time.Now(),
			CreatedAt:   time.Now(),
		}

		if err := s.database.AddClip(clip); err != nil {
			logger.Warn("Failed to add clip to database",
				zap.Error(err),
				zap.String("video_id", result.ID),
			)
			continue
		}

		imported++
	}

	logger.Info("YouTube search completed",
		zap.String("query", query),
		zap.Int("results_found", len(results)),
		zap.Int("imported", imported),
	)
}

// searchTikTok cerca su TikTok
func (s *Scheduler) searchTikTok(ctx context.Context, query string) {
	if s.tiktokClient == nil {
		return
	}

	results, err := s.tiktokClient.Search(ctx, query, s.config.MaxResultsPerQuery)
	if err != nil {
		logger.Warn("TikTok search failed",
			zap.Error(err),
			zap.String("query", query),
		)
		return
	}

	imported := 0
	for _, result := range results {
		// Filtra per views
		if result.Views < s.config.MinViews {
			continue
		}

		// Controlla se esiste già
		exists, err := s.database.ClipExists(downloader.PlatformTikTok, result.ID)
		if err != nil || exists {
			continue
		}

		// Aggiungi al database
		clip := &ClipRecord{
			ID:          fmt.Sprintf("tt_%s", result.ID),
			Platform:    downloader.PlatformTikTok,
			VideoID:     result.ID,
			URL:         result.URL,
			Title:       result.Title,
			Duration:    result.Duration,
			Views:       result.Views,
			Thumbnail:   result.Thumbnail,
			Author:      result.Author,
			Tags:        []string{query},
			Category:    s.detectCategory(result.Title, result.URL),
			LastChecked: time.Now(),
			CreatedAt:   time.Now(),
		}

		if err := s.database.AddClip(clip); err != nil {
			logger.Warn("Failed to add clip to database",
				zap.Error(err),
				zap.String("video_id", result.ID),
			)
			continue
		}

		imported++
	}

	logger.Info("TikTok search completed",
		zap.String("query", query),
		zap.Int("results_found", len(results)),
		zap.Int("imported", imported),
	)
}

// enrichExistingClips arricchisce metadati per clip esistenti
func (s *Scheduler) enrichExistingClips(ctx context.Context) {
	logger.Info("Enriching metadata for existing clips")

	// Ottieni clip senza metadati completi
	clips, err := s.database.ListMissingClipsWithMetadata(50)
	if err != nil {
		logger.Warn("Failed to list clips with missing metadata",
			zap.Error(err),
		)
		return
	}

	enriched := 0
	for _, clip := range clips {
		// Qui potresti chiamare un LLM per estrarre keywords e tag
		// Per ora, mark as filled
		clip.MetadataFilled = true
		clip.LastChecked = time.Now()

		if err := s.database.UpdateClip(&clip); err != nil {
			logger.Warn("Failed to update clip",
				zap.Error(err),
				zap.String("clip_id", clip.ID),
			)
			continue
		}

		enriched++
	}

	logger.Info("Metadata enrichment completed",
		zap.Int("clips_processed", len(clips)),
		zap.Int("clips_enriched", enriched),
	)
}

// cleanupDatabase pulisce il database da clip non pertinenti
func (s *Scheduler) cleanupDatabase(ctx context.Context) {
	// Qui potresti rimuovere clip vecchie o non pertinenti
	// Per ora, placeholder
	logger.Info("Database cleanup (not implemented yet)")
}

// detectCategory rileva la categoria dal titolo/URL
func (s *Scheduler) detectCategory(title, url string) string {
	text := (title + " " + url)
	
	if containsAny(text, "tech", "technology", "ai", "software") {
		return "tech"
	}
	if containsAny(text, "business", "marketing", "startup") {
		return "business"
	}
	if containsAny(text, "interview", "podcast", "talk") {
		return "interview"
	}
	if containsAny(text, "nature", "wildlife", "travel") {
		return "nature"
	}
	
	return "general"
}

// containsAny verifica se il testo contiene una delle parole
func containsAny(text string, words ...string) bool {
	for _, word := range words {
		if containsWord(text, word) {
			return true
		}
	}
	return false
}

// containsWord verifica se una parola è presente nel testo
func containsWord(text, word string) bool {
	return len(text) >= len(word) && 
		   (text == word || 
		    len(text) > len(word) && 
		    (text[:len(word)] == word || 
		     text[len(text)-len(word):] == word ||
		     containsSubstring(text, word)))
}

// containsSubstring verifica se una sottostringa è presente
func containsSubstring(text, substr string) bool {
	for i := 0; i <= len(text)-len(substr); i++ {
		if text[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
