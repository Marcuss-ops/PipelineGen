// Package semanticplan persists the script semantic plan (script_semantic_items
// + script_visual_bindings, migration 220) in SQLite. It is the single durable
// store for the semantic-index pipeline's two derived artifacts, keyed by
// script_id.
package semanticplan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// ErrNotWired is returned when the adapter is constructed without a database.
var ErrNotWired = errors.New("semantic plan sqlite adapter: not wired")

// Store implements both entities.SemanticItemStore and
// overlays.VisualBindingsStore over one *sql.DB, plus an atomic SavePlan that
// writes both tables in a single transaction.
type Store struct{ db *sql.DB }

// New constructs the adapter. Fail-closed: a nil database is a construction
// error, never a deferred no-op.
func New(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, ErrNotWired
	}
	return &Store{db: db}, nil
}

var (
	_ entities.SemanticItemStore   = (*Store)(nil)
	_ overlays.VisualBindingsStore = (*Store)(nil)
)

// SaveItems validates and replaces the script's semantic items (the latest
// canonical set wins), atomically.
func (s *Store) SaveItems(ctx context.Context, scriptID int64, items []entities.SemanticItem) error {
	if s == nil {
		return ErrNotWired
	}
	for _, item := range items {
		if err := item.Validate(); err != nil {
			return fmt.Errorf("semantic plan: save items: %w", err)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("semantic plan: begin: %w", err)
	}
	if err := saveItemsTx(ctx, tx, scriptID, items); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// ListItems returns the script's semantic items in deterministic play order
// (start_us, then semantic_id).
func (s *Store) ListItems(ctx context.Context, scriptID int64) ([]entities.SemanticItem, error) {
	if s == nil {
		return nil, ErrNotWired
	}
	if scriptID < 0 {
		return nil, fmt.Errorf("semantic plan: list items: script_id must be non-negative")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT semantic_id, scene_id, type, text, normalized_text, canonical_entity_id, start_char, end_char, start_us, end_us, confidence
		FROM script_semantic_items WHERE script_id = ?
		ORDER BY start_us, semantic_id`, scriptID)
	if err != nil {
		return nil, fmt.Errorf("semantic plan: list items: %w", err)
	}
	defer rows.Close()

	var out []entities.SemanticItem
	for rows.Next() {
		var item entities.SemanticItem
		if err := rows.Scan(&item.SemanticID, &item.SceneID, &item.Type, &item.Text, &item.NormalizedText,
			&item.CanonicalEntityID, &item.StartChar, &item.EndChar, &item.StartUS, &item.EndUS, &item.Confidence); err != nil {
			return nil, fmt.Errorf("semantic plan: scan item: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// SaveBindings validates and replaces the script's visual bindings (the latest
// canonical set wins), atomically.
func (s *Store) SaveBindings(ctx context.Context, scriptID int64, bindings []overlays.VisualBinding) error {
	if s == nil {
		return ErrNotWired
	}
	for _, b := range bindings {
		if err := b.Validate(); err != nil {
			return fmt.Errorf("semantic plan: save bindings: %w", err)
		}
		if b.ScriptID != scriptID {
			return fmt.Errorf("semantic plan: save bindings: binding script_id %d does not match %d", b.ScriptID, scriptID)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("semantic plan: begin: %w", err)
	}
	if err := saveBindingsTx(ctx, tx, scriptID, bindings); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// ListBindings returns the script's visual bindings in deterministic play order
// (start_us, then visual_event_id).
func (s *Store) ListBindings(ctx context.Context, scriptID int64) ([]overlays.VisualBinding, error) {
	if s == nil {
		return nil, ErrNotWired
	}
	if scriptID < 0 {
		return nil, fmt.Errorf("semantic plan: list bindings: script_id must be non-negative")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT semantic_id, visual_event_id, preset_family, preset_id, asset_id, animation_in, animation_idle, animation_out, start_us, duration_us, resolver_version, sampler_version
		FROM script_visual_bindings WHERE script_id = ?
		ORDER BY start_us, visual_event_id`, scriptID)
	if err != nil {
		return nil, fmt.Errorf("semantic plan: list bindings: %w", err)
	}
	defer rows.Close()

	var out []overlays.VisualBinding
	for rows.Next() {
		var b overlays.VisualBinding
		if err := rows.Scan(&b.SemanticID, &b.VisualEventID, &b.PresetFamily, &b.PresetID, &b.AssetID,
			&b.AnimationIn, &b.AnimationIdle, &b.AnimationOut, &b.StartUS, &b.DurationUS,
			&b.ResolverVersion, &b.SamplerVersion); err != nil {
			return nil, fmt.Errorf("semantic plan: scan binding: %w", err)
		}
		b.ScriptID = scriptID
		out = append(out, b)
	}
	return out, rows.Err()
}

// SavePlan writes the script's semantic items and visual bindings together in
// one transaction, so a script never exposes a partially-updated semantic plan.
func (s *Store) SavePlan(ctx context.Context, scriptID int64, items []entities.SemanticItem, bindings []overlays.VisualBinding) error {
	if s == nil {
		return ErrNotWired
	}
	for _, item := range items {
		if err := item.Validate(); err != nil {
			return fmt.Errorf("semantic plan: save plan: %w", err)
		}
	}
	for _, b := range bindings {
		if err := b.Validate(); err != nil {
			return fmt.Errorf("semantic plan: save plan: %w", err)
		}
		if b.ScriptID != scriptID {
			return fmt.Errorf("semantic plan: save plan: binding script_id %d does not match %d", b.ScriptID, scriptID)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("semantic plan: begin: %w", err)
	}
	if err := saveItemsTx(ctx, tx, scriptID, items); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := saveBindingsTx(ctx, tx, scriptID, bindings); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func saveItemsTx(ctx context.Context, tx *sql.Tx, scriptID int64, items []entities.SemanticItem) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM script_semantic_items WHERE script_id = ?`, scriptID); err != nil {
		return fmt.Errorf("semantic plan: clear items: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO script_semantic_items
		(script_id, semantic_id, scene_id, type, subtype, text, normalized_text, canonical_entity_id, start_char, end_char, start_us, end_us, confidence, metadata_json)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return fmt.Errorf("semantic plan: prepare items: %w", err)
	}
	defer stmt.Close()
	for _, item := range items {
		if _, err := stmt.ExecContext(ctx, scriptID, item.SemanticID, item.SceneID, string(item.Type),
			"", item.Text, item.NormalizedText, item.CanonicalEntityID,
			item.StartChar, item.EndChar, item.StartUS, item.EndUS, item.Confidence, "{}"); err != nil {
			return fmt.Errorf("semantic plan: insert item %s: %w", item.SemanticID, err)
		}
	}
	return nil
}

func saveBindingsTx(ctx context.Context, tx *sql.Tx, scriptID int64, bindings []overlays.VisualBinding) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM script_visual_bindings WHERE script_id = ?`, scriptID); err != nil {
		return fmt.Errorf("semantic plan: clear bindings: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO script_visual_bindings
		(script_id, semantic_id, visual_event_id, preset_family, preset_id, asset_id, animation_in, animation_idle, animation_out, start_us, duration_us, resolver_version, sampler_version)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return fmt.Errorf("semantic plan: prepare bindings: %w", err)
	}
	defer stmt.Close()
	for _, b := range bindings {
		if _, err := stmt.ExecContext(ctx, scriptID, b.SemanticID, b.VisualEventID, b.PresetFamily,
			b.PresetID, b.AssetID, b.AnimationIn, b.AnimationIdle, b.AnimationOut,
			b.StartUS, b.DurationUS, b.ResolverVersion, b.SamplerVersion); err != nil {
			return fmt.Errorf("semantic plan: insert binding %s: %w", b.VisualEventID, err)
		}
	}
	return nil
}
