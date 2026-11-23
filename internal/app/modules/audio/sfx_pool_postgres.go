package audio

// sfx_pool_postgres.go adapts the canonical sound-effect rows of the
// PostgreSQL media SSOT to the narrow script-generation selection port.
//
// The selector only needs identity + editorial taxonomy + certified duration;
// it never receives a path or a Drive token. The SQL itself belongs to the
// media read authority (media.SoundEffectCatalog), which owns the
// `source='sound_effect' AND category='file'` predicate; this adapter only
// maps catalog rows onto the capability DTO, so the MEDIA-SSOT read gate has
// exactly one place to inspect and the capability stays storage-agnostic.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	scriptaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/audio"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

// PostgresSoundEffectPool lists the indexed one-shot effects the clip-start
// selector may choose from.
type PostgresSoundEffectPool struct {
	catalog *pgmedia.SoundEffectCatalog
}

// NewPostgresSoundEffectPool binds the reader to the media SSOT handle. A nil
// handle returns nil so composition can fail open to the built-in aliases
// instead of pretending the library is empty.
func NewPostgresSoundEffectPool(db *sql.DB) *PostgresSoundEffectPool {
	catalog := pgmedia.NewSoundEffectCatalog(db)
	if catalog == nil {
		return nil
	}
	return &PostgresSoundEffectPool{catalog: catalog}
}

var _ scriptaudio.SoundEffectPoolSource = (*PostgresSoundEffectPool)(nil)

// ListSoundEffectCandidates returns every indexed one-shot effect. The
// family filter belongs to the selection policy, not to the query, so the
// same projection can serve other cue kinds later.
func (p *PostgresSoundEffectPool) ListSoundEffectCandidates(ctx context.Context) ([]scriptaudio.SoundEffectCandidate, error) {
	if p == nil || p.catalog == nil {
		return nil, fmt.Errorf("sound-effect pool: media reader unavailable")
	}
	rows, err := p.catalog.ListProjectionRows(ctx)
	if err != nil {
		return nil, fmt.Errorf("sound-effect pool: list: %w", err)
	}
	out := make([]scriptaudio.SoundEffectCandidate, 0, len(rows))
	for _, row := range rows {
		// Only the taxonomy subset crosses into the capability: the Drive
		// pointers and folder path stay in this package.
		out = append(out, scriptaudio.SoundEffectCandidate{
			AssetID:    row.AssetID,
			Family:     row.Family,
			Subtype:    row.Subtype,
			Mood:       row.Mood,
			Energy:     row.Energy,
			DurationUS: row.DurationMS * 1000,
			Tags:       splitSoundEffectTags(row.Tags),
		})
	}
	return out, nil
}

func splitSoundEffectTags(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(strings.ToLower(part))
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
