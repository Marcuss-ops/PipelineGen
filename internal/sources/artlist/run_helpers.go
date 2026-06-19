package artlist

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
)

// RunDefaults holds default values for request normalization.
type RunDefaults struct {
	DefaultRootFolderID string
	DefaultLimit        int
	MaxLimit            int
}

// maxSearchWords is the maximum number of words kept by normalizeSearchTerm.
const maxSearchWords = 4

// normalizeSearchTerm trims the term and keeps at most the first [maxSearchWords] words.
func normalizeSearchTerm(term string) string {
	term = strings.TrimSpace(term)
	if term == "" {
		return ""
	}

	parts := strings.Fields(term)
	if len(parts) > maxSearchWords {
		parts = parts[:maxSearchWords]
	}
	return strings.Join(parts, " ")
}

// normalizeSearchTermLower is like normalizeSearchTerm but also lowercases the result.
// Use this for cache keys and index lookups to guarantee case-insensitive matching.
func normalizeSearchTermLower(term string) string {
	return strings.ToLower(normalizeSearchTerm(term))
}

// NormalizeRunTagRequest normalizes a RunTagRequest using the provided defaults.
// This is the SINGLE normalization function that should be used everywhere:
// - Before dedup key generation
// - Before job enqueue
// - Before job execution
// - At the start of pipeline RunTag
func NormalizeRunTagRequest(req RunTagRequest, defaults RunDefaults) RunTagRequest {
	// Normalize term
	req.Term = normalizeSearchTerm(req.Term)

	// Normalize limit
	if req.Limit <= 0 {
		if defaults.DefaultLimit > 0 {
			req.Limit = defaults.DefaultLimit
		} else {
			req.Limit = 1
		}
	}
	if defaults.MaxLimit > 0 && req.Limit > defaults.MaxLimit {
		req.Limit = defaults.MaxLimit
	}

	// Normalize root folder ID
	req.RootFolderID = strings.TrimSpace(req.RootFolderID)
	if req.RootFolderID == "" && defaults.DefaultRootFolderID != "" {
		req.RootFolderID = defaults.DefaultRootFolderID
	}

	// Normalize strategy
	req.Strategy = string(models.NormalizeStrategy(req.Strategy, false))

	// Normalize concurrency
	if req.Concurrency <= 0 {
		req.Concurrency = 3
	} else if req.Concurrency > 10 {
		req.Concurrency = 10
	}

	return req
}

func runDedupKey(term, rootFolderID, strategy string, dryRun bool) string {
	// Build canonical request for deduplication
	canonical := map[string]any{
		"term":           strings.ToLower(strings.TrimSpace(term)),
		"root_folder_id": strings.TrimSpace(rootFolderID),
		"strategy":       strings.ToLower(strings.TrimSpace(strategy)),
		"dry_run":        dryRun,
	}
	raw, err := json.Marshal(canonical)
	if err != nil {
		// Fallback to simple key if JSON fails
		return fmt.Sprintf("%s|%s|%s|%v", strings.ToLower(strings.TrimSpace(term)), strings.TrimSpace(rootFolderID), strings.ToLower(strings.TrimSpace(strategy)), dryRun)
	}
	hash := sha256.Sum256(raw)
	return fmt.Sprintf("%x", hash)
}

// ResolveRootFolderID determines the canonical root folder for Artlist jobs.
// Delegates to cfg.Drive.ArtlistFolder() which resolves MediaRootFolder > ArtlistRootFolder > "".
func ResolveRootFolderID(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	return cfg.Drive.ArtlistFolder()
}
