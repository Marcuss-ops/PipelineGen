package media

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// DriveLocationSize returns the byte size this registry already recorded for a
// Drive location. Ingest writes it to asset_locations.file_size_bytes from the
// VerifiedArtifact it uploaded, so a handoff can use that fact instead of
// asking the provider for metadata the commit already knew.
//
// A missing row or an unmeasured legacy row returns (0, nil): "unknown" stays
// representable, and the caller decides whether the provider fallback is
// justified.
func (s *MediaSearcher) DriveLocationSize(ctx context.Context, fileID string) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("postgres media: media read repository not wired")
	}
	id := strings.TrimSpace(fileID)
	if id == "" {
		return 0, nil
	}
	var size int64
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(file_size_bytes, 0)
		FROM asset_locations
		WHERE location_kind = 'drive'
		  AND (external_id = $1 OR uri = 'drive://' || $1)
		ORDER BY is_primary DESC
		LIMIT 1`, id).Scan(&size)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("postgres media: drive location size %q: %w", id, err)
	}
	return size, nil
}
