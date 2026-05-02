package mediaimport

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"velox/go-master/internal/core/media"
)

type LegacyStockImporter struct {
	oldDB      *sql.DB
	mediaRepo  media.Repository
}

func NewLegacyStockImporter(oldDB *sql.DB, mediaRepo media.Repository) *LegacyStockImporter {
	return &LegacyStockImporter{
		oldDB:     oldDB,
		mediaRepo: mediaRepo,
	}
}

type StockClipRow struct {
	ClipID     string
	Filename   string
	TopicSlug  string
	Tags       string
	Duration   int
}

func (i *LegacyStockImporter) Import(ctx context.Context, workspaceID, projectID string) error {
	rows, err := i.oldDB.QueryContext(ctx, `
		SELECT clip_id, filename, topic_slug, tags, duration
		FROM stock_clips
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var row StockClipRow
		if err := rows.Scan(&row.ClipID, &row.Filename, &row.TopicSlug, &row.Tags, &row.Duration); err != nil {
			return err
		}

		now := time.Now()
		item := &media.Item{
			ID:           row.ClipID,
			WorkspaceID:  workspaceID,
			ProjectID:    projectID,
			SourceKind:   media.SourceKindStock,
			MediaType:    media.MediaTypeStockClip,
			Status:       media.MediaStatusReady,
			Title:        row.Filename,
			Description:  "",
			DurationSecs: row.Duration,
			MetadataJSON: `{"topic_slug":"` + row.TopicSlug + `","legacy":true}`,
			CreatedAt:    now,
			UpdatedAt:    now,
		}

		if err := i.mediaRepo.CreateItem(ctx, item); err != nil {
			return err
		}

		if row.Tags != "" {
			tags := parseTags(row.Tags)
			if len(tags) > 0 {
				_ = i.mediaRepo.AddTags(ctx, item.ID, tags)
			}
		}
	}

	return rows.Err()
}

func parseTags(tagsStr string) []string {
	if tagsStr == "" {
		return nil
	}
	parts := strings.Split(tagsStr, ",")
	var tags []string
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			tags = append(tags, trimmed)
		}
	}
	return tags
}
