package adminconsole

import "context"

// PermissionCheck is a minimal port for authorising admin operations.
// In a future phase this can check roles/policies.
type PermissionCheck interface {
	CanRead(ctx context.Context, entity string) bool
	CanWrite(ctx context.Context, entity string) bool
}

// AllowAll always grants permission. Used as the default in the
// absence of a real permission implementation.
type AllowAll struct{}

// CanRead returns true for every entity.
func (AllowAll) CanRead(context.Context, string) bool { return true }

// CanWrite returns true for every entity.
func (AllowAll) CanWrite(context.Context, string) bool { return true }
