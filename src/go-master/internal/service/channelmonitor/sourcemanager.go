package channelmonitor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

type SourceType string

const (
	SourceChannel   SourceType = "channel"
	SourceSearch    SourceType = "search_query"
	SourceKeyword   SourceType = "keyword"
)

type UnifiedSource struct {
	ID        string      `json:"id"`
	Type      SourceType `json:"type"`
	Enabled   bool        `json:"enabled"`
	Priority  int         `json:"priority"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`

	URL      string `json:"url,omitempty"`
	Query    string `json:"query,omitempty"`
	Category string `json:"category,omitempty"`

	Filters  FilterConfig  `json:"filters"`
	Output   OutputConfig  `json:"output"`
}

type FilterConfig struct {
	Keywords        []string `json:"keywords"`
	ExcludeKeywords []string `json:"exclude_keywords"`
	MinViews        int64    `json:"min_views"`
	MinDuration     int      `json:"min_duration"`
	MaxDuration     int      `json:"max_duration"`
	Timeframe       string   `json:"timeframe"`
}

type OutputConfig struct {
	Mode          string `json:"mode"` // "clips" | "full"
	MaxClips      int    `json:"max_clips"`
	ClipDuration  int    `json:"clip_duration"`
	DriveFolder   string `json:"drive_folder"`
	SkipTranscript bool  `json:"skip_transcript"`
}

type SourceStore struct {
	mu         sync.RWMutex
	sources    map[string]*UnifiedSource
	filePath   string
	dedupDB    *DedupDB
}

type DedupDB struct {
	mu       sync.RWMutex
	seen     map[string]time.Time
	filePath string
}

func NewDedupDB(path string) *DedupDB {
	return &DedupDB{
		seen:     make(map[string]time.Time),
		filePath: path,
	}
}

func (d *DedupDB) ShouldProcess(videoID string, maxAge time.Duration) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if t, ok := d.seen[videoID]; ok {
		return time.Since(t) > maxAge
	}
	return true
}

func (d *DedupDB) MarkProcessed(videoID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seen[videoID] = time.Now()
	d.saveAsync()
}

func (d *DedupDB) saveAsync() {
	go func() {
		d.mu.RLock()
		data, _ := json.MarshalIndent(d.seen, "", "  ")
		d.mu.RUnlock()
		os.WriteFile(d.filePath+".tmp", data, 0644)
		os.Rename(d.filePath+".tmp", d.filePath)
	}()
}

func NewSourceStore(path string) (*SourceStore, error) {
	store := &SourceStore{
		sources:  make(map[string]*UnifiedSource),
		filePath: path,
		dedupDB:  NewDedupDB(filepath.Join(filepath.Dir(path), "source_dedup.json")),
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}

	if data, err := os.ReadFile(path); err == nil {
		var sources []UnifiedSource
		if err := json.Unmarshal(data, &sources); err == nil {
			for i := range sources {
				store.sources[sources[i].ID] = &sources[i]
			}
		}
	}

	return store, nil
}

func (s *SourceStore) GetAll() []*UnifiedSource {
	s.mu.RLock()
	defer s.mu.RUnlock()

	list := make([]*UnifiedSource, 0, len(s.sources))
	for _, src := range s.sources {
		list = append(list, src)
	}

	sort.Slice(list, func(i, j int) bool {
		if list[i].Enabled != list[j].Enabled {
			return list[i].Enabled
		}
		return list[i].Priority < list[j].Priority
	})

	return list
}

func (s *SourceStore) GetEnabled() []*UnifiedSource {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var list []*UnifiedSource
	for _, src := range s.sources {
		if src.Enabled {
			list = append(list, src)
		}
	}

	sort.Slice(list, func(i, j int) bool {
		return list[i].Priority < list[j].Priority
	})

	return list
}

func (s *SourceStore) Get(id string) (*UnifiedSource, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	src, ok := s.sources[id]
	return src, ok
}

func (s *SourceStore) Add(src UnifiedSource) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if src.ID == "" {
		src.ID = generateSourceID(src)
	}
	src.CreatedAt = time.Now()
	src.UpdatedAt = time.Now()

	if src.Output.MaxClips == 0 {
		src.Output.MaxClips = 5
	}
	if src.Output.ClipDuration == 0 {
		src.Output.ClipDuration = 60
	}

	s.sources[src.ID] = &src
	return s.save()
}

func (s *SourceStore) Update(src *UnifiedSource) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	src.UpdatedAt = time.Now()
	s.sources[src.ID] = src
	return s.save()
}

func (s *SourceStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sources, id)
	return s.save()
}

func (s *SourceStore) Toggle(id string) (*UnifiedSource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if src, ok := s.sources[id]; ok {
		src.Enabled = !src.Enabled
		src.UpdatedAt = time.Now()
		s.save()
		return src, nil
	}
	return nil, nil
}

func (s *SourceStore) save() error {
	list := make([]UnifiedSource, 0, len(s.sources))
	for _, src := range s.sources {
		list = append(list, *src)
	}

	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(s.filePath+".tmp", data, 0644); err != nil {
		return err
	}
	return os.Rename(s.filePath+".tmp", s.filePath)
}

func (s *SourceStore) FilterVideo(video VideoInfo) *UnifiedSource {
	for _, src := range s.GetEnabled() {
		if matchesSource(video, src) {
			return src
		}
	}
	return nil
}

func matchesSource(video VideoInfo, src *UnifiedSource) bool {
	fe := NewFilterEngine()
	result := fe.Match(video, FilterCriteria{
		Keywords:        src.Filters.Keywords,
		ExcludeKeywords: src.Filters.ExcludeKeywords,
		MinViews:        src.Filters.MinViews,
		MinDuration:     src.Filters.MinDuration,
		MaxDuration:     src.Filters.MaxDuration,
		Timeframe:       src.Filters.Timeframe,
	})
	return result.Matched
}

func generateSourceID(src UnifiedSource) string {
	prefix := ""
	switch src.Type {
	case SourceChannel:
		prefix = "ch"
	case SourceSearch:
		prefix = "sq"
	case SourceKeyword:
		prefix = "kw"
	}
	return prefix + "_" + time.Now().Format("20060102150405")
}

func ImportFromChannelConfig(channels []ChannelConfig, store *SourceStore) error {
	for _, ch := range channels {
		src := UnifiedSource{
			Type:     SourceChannel,
			Enabled:  true,
			Priority: 0,
			URL:      ch.URL,
			Category: ch.Category,
			Filters: FilterConfig{
				Keywords:  ch.Keywords,
				MinViews:  ch.MinViews,
				Timeframe: "month",
			},
			Output: OutputConfig{
				Mode:         "clips",
				MaxClips:     ch.MaxClips,
				ClipDuration: ch.MaxClipDuration,
			},
		}

		if existing, ok := store.GetByURL(ch.URL); ok {
			src.ID = existing.ID
			src.Priority = existing.Priority
			src.CreatedAt = existing.CreatedAt
		}

		if err := store.Add(src); err != nil {
			logger.Warn("Failed to import channel",
				zap.String("url", ch.URL),
				zap.Error(err),
			)
		}
	}
	return nil
}

func (s *SourceStore) GetByURL(url string) (*UnifiedSource, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, src := range s.sources {
		if src.URL == url {
			return src, true
		}
	}
	return nil, false
}

func (s *SourceStore) GetByQuery(query string) (*UnifiedSource, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, src := range s.sources {
		if src.Query == query {
			return src, true
		}
	}
	return nil, false
}

func (s *SourceStore) DedupDB() *DedupDB {
	return s.dedupDB
}