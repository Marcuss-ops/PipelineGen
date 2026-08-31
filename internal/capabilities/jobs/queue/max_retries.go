package queue

import "fmt"

var (
	// ErrRegistryRequired reports that retry resolution was requested without
	// a registry-backed resolver.
	ErrRegistryRequired = fmt.Errorf("queue: retry policy registry is required")
	// ErrMaxRetriesUnknown reports that no retry policy exists for a job type.
	ErrMaxRetriesUnknown = fmt.Errorf("queue: max retries policy is unknown")
	errRetryResolverNil  = ErrRegistryRequired
)

// MaxRetriesResolver is the narrow registry contract needed to resolve the
// default retry budget for a job type. The queue package does not depend on
// the root jobs registry implementation.
type MaxRetriesResolver interface {
	GetMaxRetries(jobType string) (int, error)
}

// ResolveMaxRetries applies the enqueue retry contract:
//   - negative values explicitly mean zero retries;
//   - positive values are preserved verbatim;
//   - zero values are resolved through the supplied registry.
func ResolveMaxRetries(resolver MaxRetriesResolver, jobType string, current int) (int, error) {
	if current < 0 {
		return 0, nil
	}
	if current > 0 {
		return current, nil
	}
	if resolver == nil {
		return 0, fmt.Errorf("%w: nil resolver", errRetryResolverNil)
	}
	return resolver.GetMaxRetries(jobType)
}
