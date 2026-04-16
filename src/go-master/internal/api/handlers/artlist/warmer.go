package artlistpipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"velox/go-master/internal/artlistdb"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// KeywordPool manages the seed keywords for pre-warming.
type KeywordPool struct {
	data *KeywordPoolData
	path string
	mu   sync.RWMutex
}

// KeywordPoolData holds the full keyword pool.
type KeywordPoolData struct {
	Seeds    []KeywordSeed   `json:"seeds"`
	Clusters []KeywordCluster `json:"clusters"`
	Expanded map[string][]string `json:"expanded"` // parent → variants (cached LLM expansions)
	LastWarm string          `json:"last_warm"`
}

// KeywordSeed is a single seed keyword.
type KeywordSeed struct {
	Keyword    string    `json:"keyword"`
	Cluster    string    `json:"cluster"`
	Language   string    `json:"language"` // "en" or "it"
	Priority   int       `json:"priority"` // 1-10
	LastWarmed string    `json:"last_warmed"`
	WarmCount  int       `json:"warm_count"` // how many times warmed
}

// KeywordCluster groups related keywords.
type KeywordCluster struct {
	Name      string   `json:"name"`
	Keywords  []string `json:"keywords"`
}

// NewKeywordPool creates or loads the keyword pool.
func NewKeywordPool(path string) (*KeywordPool, error) {
	kp := &KeywordPool{
		path: path,
		data: &KeywordPoolData{
			Seeds:    []KeywordSeed{},
			Clusters: []KeywordCluster{},
			Expanded: make(map[string][]string),
		},
	}

	if _, err := os.Stat(path); err == nil {
		if err := kp.load(); err != nil {
			logger.Warn("Failed to load keyword pool, starting fresh", zap.Error(err))
		}
	}

	// Populate default seeds if empty
	if len(kp.data.Seeds) == 0 {
		kp.populateDefaults()
		kp.save()
	}

	return kp, nil
}

// load reads the pool from disk.
func (kp *KeywordPool) load() error {
	data, err := os.ReadFile(kp.path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, kp.data)
}

// save writes the pool to disk.
func (kp *KeywordPool) save() error {
	kp.mu.Lock()
	defer kp.mu.Unlock()

	data, err := json.MarshalIndent(kp.data, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(kp.path)
	os.MkdirAll(dir, 0755)
	return os.WriteFile(kp.path, data, 0644)
}

// populateDefaults fills the pool with 500 English seed keywords.
func (kp *KeywordPool) populateDefaults() {
	clusters := map[string][]string{
		"boxing_fitness": {
			"boxing", "punching bag", "boxing ring", "boxing gloves", "boxing training",
			"gym workout", "weightlifting", "crossfit", "running", "sweat",
			"mma training", "muay thai", "shadow boxing", "speed bag", "heavy bag",
			"boxing coach", "sparring", "rope jump", "pushups", "pullups",
			"deadlift", "bench press", "squat", "kettlebell", "dumbbell",
			"treadmill", "battle ropes", "boxing footwork", "boxing stance", "fitness motivation",
		},
		"business_money": {
			"money counting", "dollar bills", "crypto trading", "stock market", "bitcoin",
			"office meeting", "business handshake", "laptop working", "presentation", "contract signing",
			"luxury office", "entrepreneur", "startup pitch", "finance charts", "trading desk",
			"gold bars", "credit card", "bank vault", "cash register", "payment terminal",
			"business card exchange", "boardroom", "ceo office", "wall street", "money transfer",
			"ethereum", "nft digital", "blockchain", "forex trading", "investment portfolio",
			"millionaire lifestyle", "wealth", "success", "business growth", "profit",
		},
		"luxury_lifestyle": {
			"ferrari driving", "lamborghini", "luxury yacht", "private jet", "luxury villa",
			"swimming pool", "luxury watch", "champagne toast", "dubai skyline", "miami beach",
			"porsche", "rolex watch", "diamond jewelry", "designer fashion", "luxury hotel",
			"penthouse view", "sports car", "helicopter flight", "first class", "luxury restaurant",
			"supercar", "golf course", "country club", "luxury shopping", "designer bags",
			"casino chips", "roulette wheel", "high roller", "exclusive party", "vip lounge",
			"manhattan skyline", "sunset yacht", "beach resort", "spa luxury", "premium wine",
		},
		"motivation_discipline": {
			"waking up early", "reading book", "studying", "discipline", "success motivation",
			"failure recovery", "victory celebration", "night training", "meditation", "focus",
			"goal setting", "journal writing", "morning routine", "cold shower", "disciplined life",
			"hard work", "perseverance", "determination", "self improvement", "mindset",
			"vision board", "affirmations", "gratitude", "positive thinking", "mental strength",
			"overcoming obstacles", "champion mindset", "never give up", "push limits", "breakthrough",
			"alarm clock", "5am morning", "productive day", "time management", "efficiency",
		},
		"people_emotions": {
			"business man", "elegant woman", "team collaboration", "family bonding", "father son",
			"couple walking", "friends laughing", "anger expression", "joy celebration", "crying",
			"hug", "handshake", "confident walking", "people arguing", "people kissing",
			"crowd cheering", "audience clapping", "people talking", "group discussion", "leader speaking",
			"serious expression", "smiling person", "nervous person", "excited person", "thoughtful person",
			"man in suit", "woman in dress", "casual people", "athletic people", "elderly wise person",
			"young entrepreneur", "professional woman", "street people", "urban lifestyle", "city crowd",
		},
		"technology_digital": {
			"smartphone usage", "computer coding", "social media", "video call", "typing keyboard",
			"server room", "data center", "artificial intelligence", "robot", "drone flying",
			"virtual reality", "augmented reality", "3d printing", "electric car", "tesla charging",
			"blockchain network", "cybersecurity", "hacker typing", "digital interface", "hologram",
			"cloud computing", "network cables", "wifi signal", "digital transformation", "tech startup",
			"app development", "website design", "mobile gaming", "streaming video", "podcast recording",
		},
		"nature_environment": {
			"mountain landscape", "ocean waves", "forest aerial", "sunset timelapse", "city timelapse",
			"rain drops", "snow falling", "thunderstorm", "river flowing", "waterfall",
			"desert landscape", "beach aerial", "lake reflection", "northern lights", "stars timelapse",
			"flower blooming", "tree growing", "autumn leaves", "spring flowers", "winter snow",
			"cloud movement", "sunrise", "golden hour", "blue hour", "moon rise",
			"nature macro", "butterfly", "bird flying", "fish swimming", "wildlife",
		},
		"food_cooking": {
			"chef cooking", "italian pasta", "pizza making", "kitchen cooking", "food plating",
			"knife cutting", "pan frying", "oven baking", "grilling meat", "chopping vegetables",
			"wine pouring", "coffee making", "espresso", "bread baking", "cake decorating",
			"sushi making", "bbq smoking", "flambé cooking", "restaurant kitchen", "food preparation",
			"farmer market", "fresh ingredients", "spice mixing", "sauce pouring", "garnish plating",
			"tasting food", "chef tasting", "food closeup", "steam rising", "sizzling pan",
		},
	}

	for clusterName, keywords := range clusters {
		kp.data.Clusters = append(kp.data.Clusters, KeywordCluster{
			Name:     clusterName,
			Keywords: keywords,
		})

		for _, kw := range keywords {
			kp.data.Seeds = append(kp.data.Seeds, KeywordSeed{
				Keyword:  kw,
				Cluster:  clusterName,
				Language: "en",
				Priority: 5,
			})
		}
	}

	logger.Info("Keyword pool populated", zap.Int("seeds", len(kp.data.Seeds)))
}

// GetTopKeywords returns the most-used keywords from clip cache.
func (kp *KeywordPool) GetTopKeywords(n int) []string {
	kp.mu.RLock()
	defer kp.mu.RUnlock()

	// Count usage from expanded map (simulated usage tracking)
	usageCount := make(map[string]int)
	for parent := range kp.data.Expanded {
		usageCount[parent]++
	}

	// Also count seeds that have been warmed
	for _, seed := range kp.data.Seeds {
		usageCount[seed.Keyword] += seed.WarmCount
	}

	// Sort by count
	type kwCount struct {
		keyword string
		count   int
	}
	var counts []kwCount
	for kw, count := range usageCount {
		counts = append(counts, kwCount{kw, count})
	}
	sort.Slice(counts, func(i, j int) bool {
		return counts[i].count > counts[j].count
	})

	var result []string
	for i := 0; i < n && i < len(counts); i++ {
		result = append(result, counts[i].keyword)
	}

	return result
}

// GetRandomKeywords returns random keywords that haven't been warmed recently.
func (kp *KeywordPool) GetRandomKeywords(n int) []string {
	kp.mu.RLock()
	defer kp.mu.RUnlock()

	cutoff := time.Now().Add(-7 * 24 * time.Hour)

	var candidates []KeywordSeed
	for _, seed := range kp.data.Seeds {
		if seed.LastWarmed == "" {
			candidates = append(candidates, seed)
			continue
		}
		if lastWarmed, err := time.Parse(time.RFC3339, seed.LastWarmed); err == nil {
			if lastWarmed.Before(cutoff) {
				candidates = append(candidates, seed)
			}
		}
	}

	// Shuffle and pick n
	rand.Shuffle(len(candidates), func(i, j int) {
		candidates[i], candidates[j] = candidates[j], candidates[i]
	})

	n = minInt(n, len(candidates))
	var result []string
	for i := 0; i < n; i++ {
		result = append(result, candidates[i].Keyword)
	}

	return result
}

// MarkKeywordWarmed marks a keyword as warmed.
func (kp *KeywordPool) MarkKeywordWarmed(keyword string) {
	kp.mu.Lock()
	defer kp.mu.Unlock()

	for i := range kp.data.Seeds {
		if kp.data.Seeds[i].Keyword == keyword {
			kp.data.Seeds[i].LastWarmed = time.Now().Format(time.RFC3339)
			kp.data.Seeds[i].WarmCount++
			kp.save()
			return
		}
	}
}

// GetExpansionVariants returns variants for a keyword (cached or generates new ones).
func (kp *KeywordPool) GetExpansionVariants(keyword string) []string {
	kp.mu.RLock()
	defer kp.mu.RUnlock()

	if variants, ok := kp.data.Expanded[strings.ToLower(keyword)]; ok {
		return variants
	}

	// Return default variants (English synonyms/related terms)
	return defaultVariants(keyword)
}

// SaveExpansionVariants caches expansion variants for a keyword.
func (kp *KeywordPool) SaveExpansionVariants(keyword string, variants []string) {
	kp.mu.Lock()
	defer kp.mu.Unlock()

	kp.data.Expanded[strings.ToLower(keyword)] = variants
	kp.save()
}

// defaultVariants returns basic English variants for a keyword.
func defaultVariants(keyword string) []string {
	kwLower := strings.ToLower(keyword)

	// Pre-built variant map for common keywords
	variantMap := map[string][]string{
		"boxing":       {"boxing training", "boxing ring", "boxing gloves", "punching bag", "sparring"},
		"gym workout":  {"gym training", "weightlifting", "fitness", "exercise", "workout"},
		"money":        {"dollar bills", "cash", "currency", "finance", "wealth"},
		"luxury":       {"luxury lifestyle", "premium", "elegant", "expensive", "high-end"},
		"business":     {"office meeting", "business man", "corporate", "entrepreneur", "startup"},
		"technology":   {"smartphone", "computer", "digital", "tech", "innovation"},
		"nature":       {"landscape", "outdoor", "natural", "scenic", "wilderness"},
		"food":         {"cooking", "chef", "kitchen", "restaurant", "cuisine"},
		"motivation":   {"success", "discipline", "determination", "perseverance", "mindset"},
		"fitness":      {"exercise", "training", "workout", "athletic", "sport"},
	}

	if variants, ok := variantMap[kwLower]; ok {
		return variants
	}

	// Generic fallback
	return []string{
		kwLower,
		kwLower + " training",
		kwLower + " professional",
		kwLower + " slow motion",
		kwLower + " closeup",
	}
}

// RunPreWarmV2 executes the v2 pre-warm: 50 exploitation + 50 exploration keywords.
func (h *Handler) RunPreWarmV2(ctx context.Context) error {
	if h.keywordPool == nil {
		return fmt.Errorf("keyword pool not initialized")
	}

	startTime := time.Now()
	logger.Info("Pre-warm v2 started")

	// 1. EXPLOITATION: top 50 most-used keywords
	topKeywords := h.keywordPool.GetTopKeywords(50)

	// 2. EXPLORATION: 50 random keywords not warmed recently
	randomKeywords := h.keywordPool.GetRandomKeywords(50)

	// Combine and dedup
	seen := make(map[string]bool)
	var allKeywords []string
	for _, kw := range topKeywords {
		if !seen[kw] {
			seen[kw] = true
			allKeywords = append(allKeywords, kw)
		}
	}
	for _, kw := range randomKeywords {
		if !seen[kw] {
			seen[kw] = true
			allKeywords = append(allKeywords, kw)
		}
	}

	logger.Info("Pre-warm v2 keywords",
		zap.Int("exploitation", len(topKeywords)),
		zap.Int("exploration", len(randomKeywords)),
		zap.Int("total_unique", len(allKeywords)))

	var (
		totalDownloaded int
		totalSearched   int
		mu              sync.Mutex
		wg              sync.WaitGroup
		sem             = make(chan struct{}, 2) // Max 2 parallel downloads
	)

	for _, kw := range allKeywords {
		// Get expansion variants (3-5 variants per keyword)
		variants := h.keywordPool.GetExpansionVariants(kw)
		if len(variants) > 5 {
			variants = variants[:5]
		}

		for _, variant := range variants {
			wg.Add(1)
			sem <- struct{}{}

			go func(parentKw, variant string) {
				defer wg.Done()
				defer func() { <-sem }()

				// Check how many clips we already have for this variant
				existingClips, _ := h.artlistDB.GetClipsForTerm(variant)
				downloadedCount := 0
				for _, c := range existingClips {
					if c.Downloaded {
						downloadedCount++
					}
				}

				// Only download if we have < 20 clips for this variant
				targetCount := 20
				need := targetCount - downloadedCount
				if need <= 0 {
					mu.Lock()
					totalSearched++
					mu.Unlock()
					return
				}

				// Search Artlist for this variant
				if h.artlistSrc == nil {
					return
				}

				searchResults, err := h.artlistSrc.SearchClips(variant, need*3)
				if err != nil || len(searchResults) == 0 {
					logger.Debug("No Artlist results for variant",
						zap.String("variant", variant))
					mu.Lock()
					totalSearched++
					mu.Unlock()
					return
				}

				// Convert to ArtlistClip and save to DB
				var artlistClips []artlistdb.ArtlistClip
				for _, sr := range searchResults {
					// Skip if already in DB
					if _, alreadyDL := h.artlistDB.IsClipAlreadyDownloaded(sr.ID, sr.DownloadLink); alreadyDL {
						continue
					}

					artlistClips = append(artlistClips, artlistdb.ArtlistClip{
						ID:          sr.ID,
						VideoID:     sr.Filename,
						Title:       sr.Name,
						OriginalURL: sr.DownloadLink,
						URL:         sr.DownloadLink,
						Duration:    int(sr.Duration),
						Width:       sr.Width,
						Height:      sr.Height,
						Category:    sr.FolderPath,
						Tags:        sr.Tags,
					})
				}

				if len(artlistClips) == 0 {
					mu.Lock()
					totalSearched++
					mu.Unlock()
					return
				}

				h.artlistDB.AddSearchResults(variant, artlistClips)

				// Mark keyword as warmed
				h.keywordPool.MarkKeywordWarmed(parentKw)

				mu.Lock()
				totalDownloaded += len(artlistClips)
				totalSearched++
				mu.Unlock()

				logger.Debug("Pre-warm variant downloaded",
					zap.String("parent", parentKw),
					zap.String("variant", variant),
					zap.Int("clips", len(artlistClips)))

			}(kw, variant)
		}

		// Rate limit: 1.5s between keywords
		time.Sleep(1500 * time.Millisecond)
	}

	wg.Wait()

	// Save DB
	h.artlistDB.Save()

	duration := time.Since(startTime)
	logger.Info("Pre-warm v2 completed",
		zap.Int("keywords_processed", len(allKeywords)),
		zap.Int("total_clips_added", totalDownloaded),
		zap.Int("variants_searched", totalSearched),
		zap.Duration("duration", duration))

	// Update last warm timestamp
	h.keywordPool.data.LastWarm = time.Now().Format(time.RFC3339)
	h.keywordPool.save()

	return nil
}
