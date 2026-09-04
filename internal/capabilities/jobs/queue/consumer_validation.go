package queue

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrNoConsumer is the canonical queue-admission sentinel for a job type that
// has no bound handler. Root jobs re-exports it for compatibility.
var ErrNoConsumer = errors.New("queue: no consumer registered for job type")

// ConsumerCatalog exposes the registered job types that must have a consumer.
type ConsumerCatalog interface {
	AllTypes() []string
}

// ConsumerBindings exposes the consumer-presence query needed by validation.
type ConsumerBindings interface {
	HasHandler(jobType string) bool
}

// RequireConsumer checks that one job type has a bound consumer.
func RequireConsumer(jobType string, bindings ConsumerBindings) error {
	if bindings == nil || jobType == "" || !bindings.HasHandler(jobType) {
		return fmt.Errorf("%w: %q", ErrNoConsumer, jobType)
	}
	return nil
}

// ValidateConsumers checks that every registered, consumable job type has a
// bound consumer. It returns a deterministic diagnostic naming the first gap.
func ValidateConsumers(catalog ConsumerCatalog, bindings ConsumerBindings) error {
	if catalog == nil || bindings == nil {
		return nil
	}
	missing := make([]string, 0)
	for _, jobType := range catalog.AllTypes() {
		if !bindings.HasHandler(jobType) {
			missing = append(missing, jobType)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("%w: %s", ErrNoConsumer, strings.Join(missing, ", "))
}
