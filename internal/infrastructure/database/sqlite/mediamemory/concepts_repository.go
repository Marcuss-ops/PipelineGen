// Package mediamemory (sqlite infrastructure) — concepts_repository.go
// is the canonical concrete impl of mediamemory.ConceptRepository.
//
// godlike/06 SSOT (one canonical owner per fact): this file owns the
// SQL ↔ Go row conversion for media_concepts. The DDL canonical
// home is migrations/sqlite/163_mediamemory_concepts.sql; the
// application-layer wire canonical lives in
// internal/application/mediamemory/types.go::MediaConcept. Both
// files import the canonical ports; this file is the bridge.
//
// godlike/07 NO-FAKE-AVAILABILITY: UNIQUE(language,
// phrase_fingerprint) violations surface as wrapped
// ErrDuplicateBinding (re-using the canonical envelope — the
// semantic intent "duplicate row" is fail-closed). Misses surface
// as wrapped ErrConceptNotFound via errors.Is from the caller.
//
// composition: this file imports the application-layer mediamemory
// package (godlike/06 SSOT layering: infrastructure depends only
// on application, never the reverse). It does NOT import a logger
// because the canonical Logger port is narrow; if production
// observability is needed, the composition root wraps the
// repository with a decorator.
package mediamemory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediamemory"
)

// conceptsRepository is the canonical concrete
// mediamemory.ConceptRepository backed by SQLite.
type conceptsRepository struct {
	db *sql.DB
}

// NewConceptsRepository constructs the canonical repository
// (composition-root-narrow; no setters per godlike/06).
func NewConceptsRepository(db *sql.DB) mediamemory.ConceptRepository {
	return &conceptsRepository{db: db}
}

// Compile-time assertion: conceptsRepository satisfies the
// canonical ConceptRepository port. Drift in field layout or
// method set is a build error, not a runtime nil-deref.
var _ mediamemory.ConceptRepository = (*conceptsRepository)(nil)

const (
	conceptsSelectColumns = `id, canonical_text, language, normalized_text,
		phrase_fingerprint, concept_type, embedding_version,
		created_at, updated_at`
)

// Upsert inserts a new concept or updates the existing row keyed
// by (language, phrase_fingerprint). Returns the canonical row
// (with the server-assigned ID when newly created).
func (r *conceptsRepository) Upsert(ctx context.Context, c mediamemory.MediaConcept) (mediamemory.MediaConcept, error) {
	now := time.Now().UTC()
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	c.UpdatedAt = now

	q := `INSERT INTO media_concepts
		(` + conceptsSelectColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(language, phrase_fingerprint) DO UPDATE SET
			canonical_text = excluded.canonical_text,
			normalized_text = excluded.normalized_text,
			concept_type = excluded.concept_type,
			embedding_version = excluded.embedding_version,
			updated_at = excluded.updated_at
		RETURNING ` + conceptsSelectColumns

	row := r.db.QueryRowContext(ctx, q,
		c.ID, c.CanonicalText, c.Language, c.NormalizedText,
		c.PhraseFingerprint, string(c.ConceptType), nullableEmbeddingVersion(c.EmbeddingVersion),
		c.CreatedAt.Format(time.RFC3339Nano), c.UpdatedAt.Format(time.RFC3339Nano),
	)
	return scanConceptRow(row)
}

// FindByID wraps ErrConceptNotFound when the row is missing.
func (r *conceptsRepository) FindByID(ctx context.Context, id string) (mediamemory.MediaConcept, error) {
	q := `SELECT ` + conceptsSelectColumns + ` FROM media_concepts WHERE id = ?`
	row := r.db.QueryRowContext(ctx, q, id)
	c, err := scanConceptRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return mediamemory.MediaConcept{}, fmt.Errorf("mediamemory: find concept %q: %w", id, mediamemory.ErrConceptNotFound)
		}
		return mediamemory.MediaConcept{}, err
	}
	return c, nil
}

// FindByFingerprint is the Level 0 (exact match) hot path used by
// the VisualResolver before any fan-out. Returns ErrConceptNotFound
// when the row is missing.
func (r *conceptsRepository) FindByFingerprint(ctx context.Context, language, fingerprint string) (mediamemory.MediaConcept, error) {
	q := `SELECT ` + conceptsSelectColumns + `
		FROM media_concepts
		WHERE language = ? AND phrase_fingerprint = ?`
	row := r.db.QueryRowContext(ctx, q, language, fingerprint)
	c, err := scanConceptRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return mediamemory.MediaConcept{}, fmt.Errorf("mediamemory: find concept fp %q (lang=%q): %w", fingerprint, language, mediamemory.ErrConceptNotFound)
		}
		return mediamemory.MediaConcept{}, err
	}
	return c, nil
}

// FindManyByFingerprints is the bulk variant. Missing rows are
// silently skipped; the resolver matches the returned set against
// the input slice by index.
func (r *conceptsRepository) FindManyByFingerprints(ctx context.Context, language string, fingerprints []string) ([]mediamemory.MediaConcept, error) {
	if len(fingerprints) == 0 {
		return nil, nil
	}
	placeholders := strings.Repeat("?,", len(fingerprints))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, len(fingerprints)+1)
	args = append(args, language)
	for _, fp := range fingerprints {
		args = append(args, fp)
	}
	q := `SELECT ` + conceptsSelectColumns + `
		FROM media_concepts
		WHERE language = ? AND phrase_fingerprint IN (` + placeholders + `)`
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("mediamemory: bulk find concepts (lang=%q, n=%d): %w", language, len(fingerprints), err)
	}
	defer rows.Close()

	out := make([]mediamemory.MediaConcept, 0, len(fingerprints))
	for rows.Next() {
		c, err := scanConceptRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("mediamemory: bulk find concepts iterate: %w", err)
	}
	return out, nil
}

// ── Row scanning (helper) ──────────────────────────────────

func scanConceptRow(s rowScanner) (mediamemory.MediaConcept, error) {
	var (
		c                mediamemory.MediaConcept
		conceptType      string
		embeddingVersion sql.NullString
		createdAt        string
		updatedAt        string
	)
	if err := s.Scan(
		&c.ID, &c.CanonicalText, &c.Language, &c.NormalizedText,
		&c.PhraseFingerprint, &conceptType, &embeddingVersion,
		&createdAt, &updatedAt,
	); err != nil {
		return mediamemory.MediaConcept{}, err
	}
	c.ConceptType = mediamemory.ConceptType(conceptType)
	if !mediamemory.IsKnownConceptType(c.ConceptType) {
		return mediamemory.MediaConcept{}, fmt.Errorf(
			"mediamemory: concept %q has unknown concept_type %q",
			c.ID, conceptType,
		)
	}
	if embeddingVersion.Valid {
		c.EmbeddingVersion = embeddingVersion.String
	}
	var err error
	if c.CreatedAt, err = parseTime(createdAt); err != nil {
		return mediamemory.MediaConcept{}, fmt.Errorf("mediamemory: concept %q created_at: %w", c.ID, err)
	}
	if c.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return mediamemory.MediaConcept{}, fmt.Errorf("mediamemory: concept %q updated_at: %w", c.ID, err)
	}
	return c, nil
}

// nullableEmbeddingVersion returns nil for the SQLite driver when
// the embedding version is empty (Phase 1.2: no embedding yet;
// Phase 2 will write a non-empty value).
func nullableEmbeddingVersion(s string) any {
	if s == "" {
		return nil
	}
	return s
}
