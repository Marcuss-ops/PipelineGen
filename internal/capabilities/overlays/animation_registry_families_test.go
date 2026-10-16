package overlays

import (
	"sort"
	"testing"
)

// TestRegistryFamiliesHaveActiveMembers fails when a family referenced by the
// registry has only retired entries, which would leave its selection pool
// empty without any error. Add the family to the registry with an active
// entry, or drop its last reference.
func TestRegistryFamiliesHaveActiveMembers(t *testing.T) {
	active := map[string]int{}
	declared := map[string]bool{}
	for _, entry := range canonicalAnimationRegistry.Entries() {
		for _, family := range entry.Families {
			declared[family] = true
			if entry.Status == "active" {
				active[family]++
			}
		}
	}

	var empty []string
	for family := range declared {
		if active[family] == 0 {
			empty = append(empty, family)
		}
	}
	sort.Strings(empty)
	for _, family := range empty {
		t.Errorf("registry family %q has no active entries; its selection pool would be empty", family)
	}
}
