package catalogdb

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func scanClip(scanner interface{ Scan(dest ...interface{}) error }) (Clip, error) {
	var clip Clip
	var tagsJSON string
	var createdAt, modifiedAt, lastSyncedAt sql.NullTime
	var isActive int
	if err := scanner.Scan(
		&clip.ID, &clip.Source, &clip.SourceID, &clip.Provider, &clip.Title,
		&clip.Description, &clip.Filename, &clip.Category, &clip.FolderID,
		&clip.FolderPath, &clip.DriveFileID, &clip.DriveURL, &clip.ExternalPath,
		&clip.LocalPath, &tagsJSON, &clip.DurationSec, &clip.Width, &clip.Height,
		&clip.MimeType, &clip.FileExt, &clip.FileSizeBytes, &createdAt,
		&modifiedAt, &lastSyncedAt, &isActive, &clip.MetadataJSON,
	); err != nil {
		return Clip{}, fmt.Errorf("scan catalog clip: %w", err)
	}
	if createdAt.Valid { clip.CreatedAt = createdAt.Time }
	if modifiedAt.Valid { clip.ModifiedAt = modifiedAt.Time }
	if lastSyncedAt.Valid { clip.LastSyncedAt = lastSyncedAt.Time }
	clip.IsActive = isActive == 1
	_ = json.Unmarshal([]byte(tagsJSON), &clip.Tags)
	return clip, nil
}

func scanClipWithRank(scanner interface{ Scan(dest ...interface{}) error }) (Clip, float64, error) {
	var clip Clip
	var tagsJSON string
	var createdAt, modifiedAt, lastSyncedAt sql.NullTime
	var isActive int
	var rank float64
	if err := scanner.Scan(
		&clip.ID, &clip.Source, &clip.SourceID, &clip.Provider, &clip.Title,
		&clip.Description, &clip.Filename, &clip.Category, &clip.FolderID,
		&clip.FolderPath, &clip.DriveFileID, &clip.DriveURL, &clip.ExternalPath,
		&clip.LocalPath, &tagsJSON, &clip.DurationSec, &clip.Width, &clip.Height,
		&clip.MimeType, &clip.FileExt, &clip.FileSizeBytes, &createdAt,
		&modifiedAt, &lastSyncedAt, &isActive, &clip.MetadataJSON, &rank,
	); err != nil {
		return Clip{}, 0, fmt.Errorf("scan catalog clip with rank: %w", err)
	}
	if createdAt.Valid { clip.CreatedAt = createdAt.Time }
	if modifiedAt.Valid { clip.ModifiedAt = modifiedAt.Time }
	if lastSyncedAt.Valid { clip.LastSyncedAt = lastSyncedAt.Time }
	clip.IsActive = isActive == 1
	_ = json.Unmarshal([]byte(tagsJSON), &clip.Tags)
	return clip, rank, nil
}

func nullableTime(t time.Time) interface{} {
	if t.IsZero() { return nil }
	return t.UTC()
}

func boolToInt(v bool) int {
	if v { return 1 }
	return 0
}

func normalizeTags(tags []string) []string {
	seen := make(map[string]struct{}, len(tags))
	result := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(strings.ToLower(tag))
		if _, ok := seen[tag]; tag == "" || ok {
			continue
		}
		seen[tag] = struct{}{}
		result = append(result, tag)
	}
	return result
}

func scoreClip(clip Clip, queryTerms []string) float64 {
	if len(queryTerms) == 0 { return sourcePriority(clip.Source) }
	text := strings.ToLower(strings.Join([]string{clip.Title, clip.Description, clip.Filename, strings.Join(clip.Tags, " "), clip.FolderPath, clip.MetadataJSON}, " "))
	var score float64
	for _, term := range queryTerms {
		if strings.Contains(text, term) { score += 1 }
		for _, tag := range clip.Tags {
			if term == tag { score += 0.35 }
		}
	}
	if clip.DurationSec >= 3 && clip.DurationSec <= 20 { score += 0.2 }
	score += sourcePriority(clip.Source)
	return score
}

func sourcePriority(source string) float64 {
	switch source {
	case SourceClipDrive: return 0.25
	case SourceArtlist: return 0.18
	case SourceStockDrive: return 0.12
	default: return 0
	}
}

func normalizeFTSScore(rank float64) float64 {
	if rank >= 0 { return 0.1 }
	return -rank
}

func trimResults(results []SearchResult, limit int) []SearchResult {
	if len(results) <= limit { return results }
	return results[:limit]
}
