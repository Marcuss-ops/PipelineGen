// Package entities — semantic_item_store.go owns the durable SemanticItemStore
// port: the persistence surface of the script semantic index (the
// `script_semantic_items` table of the semantic-index pipeline).
//
// The semantic index is a DERIVED projection of a script's text: it is what
// the Script → Semantic Index stage produces, and it is the join key every
// downstream visual binding links back to on semantic_id. SaveScript is the
// single write path; ListScript is the deterministic read-back used to
// reconstruct "why did this visual appear" after the fact.
package entities

import (
	"context"
	"errors"
	"sort"
)

// SemanticItemStore is the durable semantic-index port. SaveItems upserts a
// script's semantic items (the latest canonical set wins); ListItems returns
// them in deterministic play order.
type SemanticItemStore interface {
	SaveItems(ctx context.Context, scriptID int64, items []SemanticItem) error
	ListItems(ctx context.Context, scriptID int64) ([]SemanticItem, error)
}

// InMemorySemanticItemStore is the pure, deterministic SemanticItemStore for
// tests and non-durable callers. Save replaces the script's whole set
// (upsert-by-semantic-id semantics), matching the SQLite adapter's behavior.
type InMemorySemanticItemStore struct {
	byScript map[int64]map[string]SemanticItem // scriptID → semantic_id → item
}

// NewInMemorySemanticItemStore returns an empty in-memory semantic item store.
func NewInMemorySemanticItemStore() *InMemorySemanticItemStore {
	return &InMemorySemanticItemStore{byScript: map[int64]map[string]SemanticItem{}}
}

// SaveItems validates then replaces the script's semantic items. An invalid
// item aborts the whole save (no partial state), matching the SQLite adapter's
// transactional behavior.
func (s *InMemorySemanticItemStore) SaveItems(_ context.Context, scriptID int64, items []SemanticItem) error {
	if s == nil {
		return errors.New("semantic item store: nil receiver")
	}
	rows := make(map[string]SemanticItem, len(items))
	for _, item := range items {
		if err := item.Validate(); err != nil {
			return err
		}
		rows[item.SemanticID] = item
	}
	s.byScript[scriptID] = rows
	return nil
}

// ListItems returns the script's semantic items in deterministic play order
// (start_us, then semantic_id) — never map order.
func (s *InMemorySemanticItemStore) ListItems(_ context.Context, scriptID int64) ([]SemanticItem, error) {
	if s == nil {
		return nil, errors.New("semantic item store: nil receiver")
	}
	rows := s.byScript[scriptID]
	out := make([]SemanticItem, 0, len(rows))
	for _, item := range rows {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartUS != out[j].StartUS {
			return out[i].StartUS < out[j].StartUS
		}
		return out[i].SemanticID < out[j].SemanticID
	})
	return out, nil
}
