package artifacts

import "context"

// ── Shared Registry helpers ─────────────────────────────────────────────

// NoopFindByPHash returns ("", nil) — use when pHash lookup is not
// applicable to a media type (e.g. voiceovers are audio, pHash is visual).
func NoopFindByPHash(_ context.Context, _ string) (string, error) {
	return "", nil
}

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
	return r.PHashFn(ctx, phash)
}

// compile-time guard: SimpleRegistry satisfies Registry
var _ Registry = (*SimpleRegistry)(nil)
