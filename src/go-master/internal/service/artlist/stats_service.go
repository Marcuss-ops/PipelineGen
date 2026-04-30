package artlist

import (
	"context"
	"database/sql"
	"strings"
)

func (s *Service) GetStats(ctx context.Context) (*Stats, error) {
	stats := &Stats{OK: true}

	if s.artlistDB == nil {
		return stats, nil
	}

	row := s.artlistDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM artlist_clips")
	artlistTotal := 0
	if err := row.Scan(&artlistTotal); err == nil {
		stats.ArtlistClipsTotal = artlistTotal
	}

	totalClips, err := s.clipsRepo.CountClips(ctx)
	if err == nil {
		stats.ClipsTotal = totalClips
	}

	row = s.artlistDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM search_terms")
	termsTotal := 0
	if err := row.Scan(&termsTotal); err == nil {
		stats.SearchTermsTotal = termsTotal
	}

	row = s.artlistDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM search_terms WHERE scraped = 1")
	termsScraped := 0
	if err := row.Scan(&termsScraped); err == nil {
		stats.SearchTermsScraped = termsScraped
	}

	if stats.SearchTermsTotal > 0 {
		stats.CoveragePct = float64(stats.SearchTermsScraped) / float64(stats.SearchTermsTotal) * 100
	}

	row = s.artlistDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM search_terms WHERE scraped = 0 OR scraped IS NULL")
	staleTerms := 0
	if err := row.Scan(&staleTerms); err == nil {
		stats.StaleTerms = staleTerms
	}

	row = s.artlistDB.QueryRowContext(ctx, "SELECT last_sync_at FROM sync_status LIMIT 1")
	var lastSync string
	if err := row.Scan(&lastSync); err == nil && lastSync != "" {
		stats.LastSyncAt = &lastSync
	}

	return stats, nil
}

func (s *Service) Diagnostics(ctx context.Context, term string) (*DiagnosticsResponse, error) {
	resp := &DiagnosticsResponse{
		OK:             true,
		RootFolderID:   s.driveFolderID,
		DriveFolderID:  s.driveFolderID,
		NodeScraperDir: s.nodeScraperDir,
		HasDriveClient: s.driveClient != nil,
		HasArtlistDB:   s.artlistDB != nil,
		MainDBReady:    s.mainDB != nil,
	}

	if s.mainDB != nil {
		if total, err := s.clipsRepo.CountClips(ctx); err == nil {
			resp.ClipsTotal = total
		}
	}

	if s.artlistDB != nil {
		if row := s.artlistDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM artlist_clips"); row != nil {
			_ = row.Scan(&resp.ArtlistClipsTotal)
		}
	}

	term = strings.TrimSpace(term)
	if term != "" {
		resp.SearchTerm = term
		if matches, err := s.clipsRepo.SearchClips(ctx, term); err == nil {
			resp.MatchingClips = len(matches)
			resp.EstimatedSize = len(matches)
		}
	}

	if lastProcessedAt, err := s.lastProcessedAtForTerm(ctx, term); err == nil {
		resp.LastProcessedAt = lastProcessedAt
	}

	return resp, nil
}

func (s *Service) StaleTerms(ctx context.Context) ([]TermInfo, error) {
	if s.artlistDB == nil {
		return nil, nil
	}

	rows, err := s.artlistDB.QueryContext(ctx, `
		SELECT term, scraped, last_scraped, (SELECT COUNT(*) FROM artlist_clips WHERE search_term_id = st.id) as video_count, created_at
		FROM search_terms st
		WHERE scraped = 0 OR scraped IS NULL
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var terms []TermInfo
	for rows.Next() {
		var t TermInfo
		var lastScraped sql.NullString
		if err := rows.Scan(&t.Term, &t.Scraped, &lastScraped, &t.VideoCount, &t.CreatedAt); err != nil {
			continue
		}
		if lastScraped.Valid {
			t.LastScraped = &lastScraped.String
		}
		terms = append(terms, t)
	}

	return terms, nil
}

func (s *Service) lastProcessedAtForTerm(ctx context.Context, term string) (*string, error) {
	if s.mainDB == nil {
		return nil, nil
	}

	var lastProcessed sql.NullString
	row := s.mainDB.QueryRowContext(ctx, `
		SELECT MAX(updated_at)
		FROM clips
		WHERE source = 'artlist' AND tags LIKE ?
	`, "%"+strings.TrimSpace(term)+"%")
	if err := row.Scan(&lastProcessed); err != nil {
		return nil, err
	}
	if !lastProcessed.Valid || strings.TrimSpace(lastProcessed.String) == "" {
		return nil, nil
	}
	return &lastProcessed.String, nil
}
