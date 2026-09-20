package artifacts

import (
	"context"
	"errors"
)

// ── Shared Registry helpers ─────────────────────────────────────────────

// ErrContentHashLookupNotWired is the fail-closed sentinel returned by a
// Registry whose content-identity lookup was never wired. Reporting "these
// bytes are new" for an unreadable content index would be a successful no-op
// that silently duplicates storage, so the lookup refuses instead.
var ErrContentHashLookupNotWired = errors.New("artifacts: FindByContentHash is not wired for this registry")

// NoopFindByPHash returns ("", nil) — use when pHash lookup is not
// applicable to a media type (e.g. voiceovers are audio, pHash is visual).
func NoopFindByPHash(_ context.Context, _ string) (string, error) {
	return "", nil
}

// NoopFindByContentHash returns (nil, nil) — use only for a registry whose
// records have no byte identity at all (pure metadata registries).
//
// It is deliberately NOT the default for SimpleRegistry: "no content index"
// and "the bytes are not stored" are different facts, and conflating them is
// what makes content dedup quietly stop working. A registry that cannot answer
// must leave ContentHashFn nil and fail closed with
// ErrContentHashLookupNotWired instead.

// GetAllWithDriveFileID is a generic helper for the common registry pattern:
// list all records from a repository, convert-and-filter each to a
// *MediaRecord via a single callback. The callback returns (nil, false) to
// skip an item (e.g. empty DriveFileID).
func GetAllWithDriveFileID[T any](
	ctx context.Context,
	listAll func(context.Context) ([]T, error),
	convert func(T) (*MediaRecord, bool),
) ([]*MediaRecord, error) {
	items, err := listAll(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]*MediaRecord, 0, len(items))
	for _, item := range items {
		if rec, ok := convert(item); ok {
			result = append(result, rec)
		}
	}
	return result, nil
}

// ── SimpleRegistry ─────────────────────────────────────────────────────

// SimpleRegistry implements Registry entirely via callback functions.
// Each method of the Registry interface delegates to the corresponding
// function field. Intended for thin CRUD wrappers around a single
// repository — the constructor wires the repo-specific callbacks while
// the conversion logic stays with the adapter package.
//
// For adapters that need custom logic on a per-method basis (e.g. ClipsRegistry
// with its 5 dependency fields, raw SQL, and multi-table writes), implement
// Registry directly instead of using SimpleRegistry.
type SimpleRegistry struct {
	UpsertFn func(context.Context, *MediaRecord) error
	GetFn    func(context.Context, string) (*MediaRecord, error)
	DeleteFn func(context.Context, string) error
	ListFn   func(context.Context) ([]*MediaRecord, error)
	PHashFn  func(context.Context, string) (string, error)
	// ContentHashFn resolves the record owning a content identity (SHA-256 of
	// the bytes). Nil is a fail-closed signal, NOT a no-op: see
	// NoopFindByContentHash for why the two must stay distinguishable.
	ContentHashFn func(context.Context, string) (*MediaRecord, error)
}

func (r *SimpleRegistry) UpsertMedia(ctx context.Context, rec *MediaRecord) error {
	return r.UpsertFn(ctx, rec)
}

func (r *SimpleRegistry) GetMedia(ctx context.Context, id string) (*MediaRecord, error) {
	return r.GetFn(ctx, id)
}

func (r *SimpleRegistry) DeleteMedia(ctx context.Context, id string) error {
	return r.DeleteFn(ctx, id)
}

func (r *SimpleRegistry) GetAllWithDriveFileID(ctx context.Context) ([]*MediaRecord, error) {
	return r.ListFn(ctx)
}

func (r *SimpleRegistry) FindByPHash(ctx context.Context, phash string) (string, error) {
	if r.PHashFn == nil {
		return "", nil
	}
	return r.PHashFn(ctx, phash)
}

func (r *SimpleRegistry) FindByContentHash(ctx context.Context, sha256 string) (*MediaRecord, error) {
	if r.ContentHashFn == nil {
		return nil, ErrContentHashLookupNotWired
	}
	return r.ContentHashFn(ctx, sha256)
}

// compile-time guard: SimpleRegistry satisfies Registry
var _ Registry = (*SimpleRegistry)(nil)
