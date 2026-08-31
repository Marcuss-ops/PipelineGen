package queue

import (
	"fmt"
	"sort"
	"strings"
)

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
		return fmt.Errorf("queue consumer validation: job type %q has no consumer", jobType)
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
	return fmt.Errorf("queue consumer validation: registered job type(s) have no consumer: %s", strings.Join(missing, ", "))
}
