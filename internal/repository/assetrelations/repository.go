// Package assetrelations provides the read/write layer for the
// asset_relations table, which tracks relationships between media assets.
// Examples: derived_from, part_of, used_in, replaces, source_of.
//
// Canonical relation types are defined as constants below. All code that
// creates relations MUST use these constants to ensure consistency.
package assetrelations

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// ── Canonical relation types ───────────────────────────────────────────
// These are the only valid values for the relation_type column.
// Add new constants here as new relationship types are needed.
const (
	// DerivedFrom: asset was derived from another (e.g. rendered video from script)
	RelationDerivedFrom = "derived_from"
	// PartOf: asset is part of a compilation or collection
	RelationPartOf = "part_of"
	// UsedIn: asset was used as input in creating another asset
	RelationUsedIn = "used_in"
	// Replaces: asset replaces a previous version/asset
	RelationReplaces = "replaces"
	// SourceOf: asset is the source for a derived asset (e.g. video → transcript)
	RelationSourceOf = "source_of"
)

// ValidRelationTypes returns all valid relation type constants.
func ValidRelationTypes() []string {
	return []string{
		RelationDerivedFrom,
		RelationPartOf,
		RelationUsedIn,
		RelationReplaces,
		RelationSourceOf,
	}
}

// IsValidRelationType returns true if the given type is a canonical constant.
func IsValidRelationType(relationType string) bool {
	switch relationType {
	case RelationDerivedFrom, RelationPartOf, RelationUsedIn, RelationReplaces, RelationSourceOf:
		return true
	default:
		return false
	}
}

// ── Types ─────────────────────────────────────────────────────────────

// Relation represents a single asset_relations row.
type Relation struct {
	SourceAssetID string
	TargetAssetID string
	RelationType  string
	MetadataJSON  string
	CreatedAt     time.Time
}

// Repository wraps SQL access to the asset_relations table.
type Repository struct {
	db *sql.DB
}

// NewRepository creates a Repository backed by db.
func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// Create inserts a new relation. Returns error if:
//   - source == target (self-relation)
//   - the relation_type is not a canonical constant
//   - metadata_json is not valid JSON (when non-empty)
//   - the tuple already exists (unique constraint)
func (r *Repository) Create(ctx context.Context, rel Relation) error {
	if rel.SourceAssetID == rel.TargetAssetID {
		return fmt.Errorf("assetrelations.Create: self-relation not allowed (%s → %s)",
			rel.SourceAssetID, rel.TargetAssetID)
	}
	if !IsValidRelationType(rel.RelationType) {
		return fmt.Errorf("assetrelations.Create: invalid relation type %q (must be one of: derived_from, part_of, used_in, replaces, source_of)",
			rel.RelationType)
	}
	if rel.MetadataJSON != "" && rel.MetadataJSON != "{}" && !json.Valid([]byte(rel.MetadataJSON)) {
		return fmt.Errorf("assetrelations.Create(%s→%s, %s): metadata_json is not valid JSON",
			rel.SourceAssetID, rel.TargetAssetID, rel.RelationType)
	}
	now := timeutil.FormatRFC3339(time.Now())
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO asset_relations (source_asset_id, target_asset_id, relation_type, metadata_json, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, rel.SourceAssetID, rel.TargetAssetID, rel.RelationType, rel.MetadataJSON, now)
	if err != nil {
		return fmt.Errorf("assetrelations.Create(%s→%s, %s): %w",
			rel.SourceAssetID, rel.TargetAssetID, rel.RelationType, err)
	}
	return nil
}

// Delete removes a single relation.
func (r *Repository) Delete(ctx context.Context, sourceAssetID, targetAssetID, relationType string) error {
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM asset_relations
		WHERE source_asset_id = ? AND target_asset_id = ? AND relation_type = ?
	`, sourceAssetID, targetAssetID, relationType)
	if err != nil {
		return fmt.Errorf("assetrelations.Delete(%s→%s, %s): %w",
			sourceAssetID, targetAssetID, relationType, err)
	}
	return nil
}

// GetBySource returns all relations where the given asset is the source.
func (r *Repository) GetBySource(ctx context.Context, sourceAssetID string) ([]Relation, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT source_asset_id, target_asset_id, relation_type, metadata_json, created_at
		FROM asset_relations
		WHERE source_asset_id = ?
		ORDER BY relation_type, target_asset_id
	`, sourceAssetID)
	if err != nil {
		return nil, fmt.Errorf("assetrelations.GetBySource(%s): %w", sourceAssetID, err)
	}
	defer rows.Close()
	return scanRelations(rows)
}

// GetByTarget returns all relations where the given asset is the target.
func (r *Repository) GetByTarget(ctx context.Context, targetAssetID string) ([]Relation, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT source_asset_id, target_asset_id, relation_type, metadata_json, created_at
		FROM asset_relations
		WHERE target_asset_id = ?
		ORDER BY relation_type, source_asset_id
	`, targetAssetID)
	if err != nil {
		return nil, fmt.Errorf("assetrelations.GetByTarget(%s): %w", targetAssetID, err)
	}
	defer rows.Close()
	return scanRelations(rows)
}

// GetByType returns all relations of a given type.
func (r *Repository) GetByType(ctx context.Context, relationType string) ([]Relation, error) {
	if !IsValidRelationType(relationType) {
		return nil, fmt.Errorf("assetrelations.GetByType: invalid relation type %q", relationType)
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT source_asset_id, target_asset_id, relation_type, metadata_json, created_at
		FROM asset_relations
		WHERE relation_type = ?
		ORDER BY source_asset_id, target_asset_id
	`, relationType)
	if err != nil {
		return nil, fmt.Errorf("assetrelations.GetByType(%s): %w", relationType, err)
	}
	defer rows.Close()
	return scanRelations(rows)
}

// DeleteAllForSource removes all relations where the given asset is the source.
func (r *Repository) DeleteAllForSource(ctx context.Context, sourceAssetID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM asset_relations WHERE source_asset_id = ?`, sourceAssetID)
	if err != nil {
		return fmt.Errorf("assetrelations.DeleteAllForSource(%s): %w", sourceAssetID, err)
	}
	return nil
}

// DeleteAllForTarget removes all relations where the given asset is the target.
func (r *Repository) DeleteAllForTarget(ctx context.Context, targetAssetID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM asset_relations WHERE target_asset_id = ?`, targetAssetID)
	if err != nil {
		return fmt.Errorf("assetrelations.DeleteAllForTarget(%s): %w", targetAssetID, err)
	}
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────

func scanRelation(s interface{ Scan(dest ...any) error }) (*Relation, error) {
	r := &Relation{}
	var createdAtStr string
	err := s.Scan(&r.SourceAssetID, &r.TargetAssetID, &r.RelationType, &r.MetadataJSON, &createdAtStr)
	if err != nil {
		return nil, err
	}
	if t := timeutil.ParseRFC3339(createdAtStr); !t.IsZero() {
		r.CreatedAt = t
	}
	return r, nil
}

func scanRelations(rows *sql.Rows) ([]Relation, error) {
	var out []Relation
	for rows.Next() {
		r, err := scanRelation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}
