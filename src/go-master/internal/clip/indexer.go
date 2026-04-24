package clip

import (
	"sync"
	"time"
	"strings"

	"velox/go-master/internal/upload/drive"
)

type Indexer struct {
	driveClient  *drive.Client
	rootFolderID string
	scanFolderIDs []string
	mu           sync.RWMutex
	index        *ClipIndex
	lastSync     time.Time
	cache        *SuggestionCache
	artlistSrc   *ArtlistSource
}

func NewIndexer(driveClient *drive.Client, rootFolderID string) *Indexer {
	return &Indexer{
		driveClient:  driveClient,
		rootFolderID: rootFolderID,
		cache:        NewSuggestionCache(500, 10*time.Minute),
		index: &ClipIndex{
			Version:      "1.0",
			RootFolderID: rootFolderID,
			Clips:        []IndexedClip{},
			Folders:      []IndexedFolder{},
			Stats: IndexStats{
				ClipsByGroup:     make(map[string]int),
				ClipsByMediaType: make(map[string]int),
			},
		},
	}
}

func (idx *Indexer) SetScanFolderIDs(folderIDs []string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	seen := make(map[string]bool)
	out := make([]string, 0, len(folderIDs))
	for _, id := range folderIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	idx.scanFolderIDs = out
}

func (idx *Indexer) getScanFolderIDs() []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if len(idx.scanFolderIDs) == 0 {
		return nil
	}
	out := make([]string, len(idx.scanFolderIDs))
	copy(out, idx.scanFolderIDs)
	return out
}

func NewTestIndexer(clips []IndexedClip) *Indexer {
	index := &ClipIndex{
		Version:      "test",
		LastSync:     time.Now(),
		RootFolderID: "test",
		Clips:        clips,
		Folders:      []IndexedFolder{},
		Stats: IndexStats{
			TotalClips:       len(clips),
			TotalFolders:     0,
			ClipsByGroup:     make(map[string]int),
			ClipsByMediaType: make(map[string]int),
		},
	}

	for _, clip := range clips {
		if clip.Group != "" {
			index.Stats.ClipsByGroup[clip.Group]++
		}
		if clip.MediaType != "" {
			index.Stats.ClipsByMediaType[clip.MediaType]++
		}
	}

	return &Indexer{
		driveClient:  nil,
		rootFolderID: "test",
		cache:        NewSuggestionCache(500, 10*time.Minute),
		index:        index,
	}
}

func (idx *Indexer) GetIndex() *ClipIndex {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if idx.index == nil {
		return &ClipIndex{
			Version:      "1.0",
			RootFolderID: idx.rootFolderID,
			Stats: IndexStats{
				ClipsByGroup:     make(map[string]int),
				ClipsByMediaType: make(map[string]int),
			},
		}
	}

	indexCopy := *idx.index
	indexCopy.Stats.ClipsByGroup = make(map[string]int)
	for k, v := range idx.index.Stats.ClipsByGroup {
		indexCopy.Stats.ClipsByGroup[k] = v
	}

	return &indexCopy
}

func (idx *Indexer) SetIndex(index *ClipIndex) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	idx.index = index
	idx.lastSync = index.LastSync
}

func (idx *Indexer) GetLastSync() time.Time {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.lastSync
}

func (idx *Indexer) GetStats() IndexStats {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if idx.index == nil {
		return IndexStats{
			ClipsByGroup:     make(map[string]int),
			ClipsByMediaType: make(map[string]int),
		}
	}

	return idx.index.Stats
}

func (idx *Indexer) NeedsSync(maxAge time.Duration) bool {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if idx.index == nil {
		return true
	}

	return time.Since(idx.lastSync) > maxAge
}

func (idx *Indexer) GetCache() *SuggestionCache {
	return idx.cache
}

func (idx *Indexer) SetArtlistSource(src *ArtlistSource) {
	idx.artlistSrc = src
}
