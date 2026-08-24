package adminconsole

import "context"

// Adapter is a generic EntityReader/EntityMutator implementation that
// delegates to the functions supplied by the composition root.  Keeping
// the package free of concrete service imports lets the admin console
// live next to any application module without creating dependency
// cycles, and keeps the registry usable for entities whose backing
// service has not yet been wired.
type Adapter struct {
	ListFn   func(ctx context.Context, opts ListOptions) (ListResult, error)
	GetFn    func(ctx context.Context, id string) (map[string]any, error)
	PatchFn  func(ctx context.Context, id string, changes map[string]any, expectedVersion int) (map[string]any, error)
	ActionFn func(ctx context.Context, id, action string, payload map[string]any) (map[string]any, error)
}

// Compile-time checks.
var _ EntityReader = (*Adapter)(nil)
var _ EntityMutator = (*Adapter)(nil)

// List delegates to ListFn, or returns an empty result if nil.
func (a *Adapter) List(ctx context.Context, opts ListOptions) (ListResult, error) {
	if a.ListFn == nil {
		return ListResult{Items: []map[string]any{}}, nil
	}
	return a.ListFn(ctx, opts)
}

// Get delegates to GetFn, or returns an error if nil.
func (a *Adapter) Get(ctx context.Context, id string) (map[string]any, error) {
	if a.GetFn == nil {
		return nil, ErrNotSupported
	}
	return a.GetFn(ctx, id)
}

// Patch delegates to PatchFn, or returns an error if nil.
func (a *Adapter) Patch(ctx context.Context, id string, changes map[string]any, expectedVersion int) (map[string]any, error) {
	if a.PatchFn == nil {
		return nil, ErrNotEditable
	}
	return a.PatchFn(ctx, id, changes, expectedVersion)
}

// Action delegates to ActionFn, or returns an error if nil.
func (a *Adapter) Action(ctx context.Context, id, action string, payload map[string]any) (map[string]any, error) {
	if a.ActionFn == nil {
		return nil, ErrActionNotSupported
	}
	return a.ActionFn(ctx, id, action, payload)
}
