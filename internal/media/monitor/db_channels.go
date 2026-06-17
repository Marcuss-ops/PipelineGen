package monitor

import (
	"context"
	"encoding/json"
	"time"
)

func (m *ChannelMonitor) loadDBChannels(ctx context.Context) ([]ChannelConfig, error) {
	if m.db == nil {
		return nil, nil
	}

	rows, err := m.db.QueryContext(ctx, `
		SELECT channel_url, category, COALESCE(keywords, '[]'), min_views, max_clip_duration,
			COALESCE(drive_folder_id, ''),
			COALESCE(semantic_keywords, '[]'),
			COALESCE(min_semantic_score, 0),
			COALESCE(playlist_end, -1),
			COALESCE(check_interval, '24h'),
			COALESCE(max_videos_per_run, 0),
			COALESCE(priority, 2),
			COALESCE(lookback_days, 0),
			COALESCE(max_segments, 0),
			COALESCE(segment_prompt, '')
		FROM category_channels
		ORDER BY category ASC, channel_url ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var configs []ChannelConfig
	for rows.Next() {
		var url, category, keywordsJSON, driveFolderID, semanticKeywordsJSON,
			checkIntervalStr, segmentPrompt string
		var minViews, maxClipDur, minSemanticScore, playlistEnd, maxVideosPerRun, priority, lookbackDays, maxSegments int
		if err := rows.Scan(&url, &category, &keywordsJSON, &minViews, &maxClipDur,
			&driveFolderID, &semanticKeywordsJSON, &minSemanticScore,
			&playlistEnd, &checkIntervalStr, &maxVideosPerRun, &priority, &lookbackDays, &maxSegments, &segmentPrompt); err != nil {
			continue
		}

		// Parse check_interval string into time.Duration
		var checkInterval time.Duration
		if checkIntervalStr != "" && checkIntervalStr != "24h" {
			if parsed, err := parseCheckInterval(checkIntervalStr); err == nil {
				checkInterval = parsed
			}
		}

		cfg := ChannelConfig{
			URL:              url,
			Category:         category,
			MaxClipDuration:  maxClipDur,
			DriveFolderID:    driveFolderID,
			MinSemanticScore: minSemanticScore,
			PlaylistEnd:      playlistEnd,
			CheckInterval:    checkInterval,
			MaxVideosPerRun:  maxVideosPerRun,
			Priority:         priority,
			LookbackDays:     lookbackDays,
			MaxSegments:      maxSegments,
			SegmentPrompt:    segmentPrompt,
		}
		if minViews > 0 {
			cfg.MinViews = minViews
		}
		if keywordsJSON != "" && keywordsJSON != "[]" {
			var keywords []string
			if err := json.Unmarshal([]byte(keywordsJSON), &keywords); err == nil {
				cfg.Keywords = keywords
			}
		}
		if semanticKeywordsJSON != "" && semanticKeywordsJSON != "[]" {
			var sk []string
			if err := json.Unmarshal([]byte(semanticKeywordsJSON), &sk); err == nil {
				cfg.SemanticKeywords = sk
			}
		}
		configs = append(configs, cfg)
	}

	return configs, rows.Err()
}

// Stop stops the channel monitor
