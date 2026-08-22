package catalogsync

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// SyncAll synchronizes every configured target.
func (s *Service) SyncAll(ctx context.Context) (*Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	summary := &Summary{
		OK:        true,
		StartedAt: time.Now().UTC(),
		Roots:     make([]RootSummary, 0, len(s.targets)),
	}

	if s.reader == nil {
		summary.OK = false
		summary.Error = "drive reader not configured"
		return summary, fmt.Errorf("drive reader not configured")
	}

	for _, target := range s.targets {
		if strings.TrimSpace(target.RootFolderID) == "" {
			continue
		}
		if target.Repo == nil || target.Indexer == nil {
			err := fmt.Errorf("%w: source=%q", ErrCatalogSyncInvalidTarget, target.Source)
			summary.OK = false
			summary.Error = err.Error()
			return summary, err
		}

		rootSummary, err := s.syncTarget(ctx, target)
		if err != nil {
			rootSummary.Error = err.Error()
			summary.OK = false
			summary.Error = err.Error()
		}
		summary.Roots = append(summary.Roots, rootSummary)
		summary.Synced += rootSummary.Synced
		summary.Failed += rootSummary.Failed
	}

	summary.EndedAt = time.Now().UTC()
	return summary, nil
}

// SyncSource synchronizes a specific source target.
func (s *Service) SyncSource(ctx context.Context, source string) (*RootSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, target := range s.targets {
		if strings.EqualFold(target.Source, source) {
			if err := validateTarget(target); err != nil {
				return nil, err
			}
			summary, err := s.syncTarget(ctx, target)
			return &summary, err
		}
	}

	return nil, fmt.Errorf("source not found: %s", source)
}

// SyncFolderID synchronizes a single Drive folder (not pre-configured as a target)
// into the database recursively. This is the ad-hoc equivalent of a catalog sync
// that accepts an arbitrary folder ID, source, and repo.
//
// Usage:
//
//	POST /api/media/sync
//	{ "drive_folder_id": "1ll2RlTa...", "source": "youtube", "name": "MyFolder" }
func (s *Service) SyncFolderID(ctx context.Context, folderID, source, name, mediaType string, repo CatalogRepository, indexer AssetIndexer) (*RootSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if strings.TrimSpace(folderID) == "" {
		return nil, fmt.Errorf("drive_folder_id is required")
	}
	if repo == nil {
		return nil, fmt.Errorf("repository is required")
	}
	if indexer == nil {
		return nil, fmt.Errorf("asset indexer is required")
	}
	if source == "" {
		source = "drive"
	}
	if name == "" {
		name = folderID
	}
	if mediaType == "" {
		mediaType = "video"
	}

	target := Target{
		Name:         name,
		RootFolderID: folderID,
		Source:       source,
		MediaType:    mediaType,
		Repo:         repo,
		Indexer:      indexer,
	}

	summary, err := s.syncTarget(ctx, target)
	return &summary, err
}
