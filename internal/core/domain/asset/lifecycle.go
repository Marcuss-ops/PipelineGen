package asset

// LifecycleState is the canonical lifecycle status of an asset.
type LifecycleState string

const (
	StateReady   LifecycleState = "ready"
	StatePending LifecycleState = "pending"
	StateDeleted LifecycleState = "deleted"
)

// Valid returns true if s is a known lifecycle state.
func (s LifecycleState) Valid() bool {
	switch s {
	case StateReady, StatePending, StateDeleted:
		return true
	}
	return false
}
