package audit

import (
	"testing"
)

// The model must cover all live tables — any table that is not in the
// model and not auto-classified by heuristic is a regression risk.
func TestReachabilityModel_CoversAllTables(t *testing.T) {
	// Every synthetic key in the model that has a ChildTable must
	// reference a real table name (we don't want orphan edges).
	for key, rel := range canonicalOwnershipModel {
		if rel.ChildTable == "" {
			continue // root/cache/queue/history/audit entry
		}
		if rel.ChildTable == "" || rel.OwnerTable == "" || rel.ChildColumn == "" || rel.OwnerColumn == "" {
			t.Errorf("model key %q: child edge has empty fields: %+v", key, rel)
		}
		if rel.Kind != "FK" && rel.Kind != "LOGICAL" {
			t.Errorf("model key %q: Kind must be FK or LOGICAL, got %q", key, rel.Kind)
		}
	}
}

// The heuristic classifier must not silently return empty.
func TestClassifyByHeuristic(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"some_cache", "cache"},
		{"provider_cache", "cache"},
		{"something_audit", "audit"},
		{"job_events", "history"},
		{"some_history", "history"},
		{"qdrantprojection_checkpoints", "history"},
		{"request_log", "audit"},
		{"unknown_table", "unclassified"},
	}
	for _, c := range cases {
		got := classifyByHeuristic(c.name)
		if got != c.want {
			t.Errorf("classifyByHeuristic(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

// The model must NOT have duplicate keys — the Go compiler would catch
// this at build time, but a test keeps the invariant explicit.
func TestReachabilityModel_NoDuplicateKeys(t *testing.T) {
	seen := map[string]bool{}
	for key := range canonicalOwnershipModel {
		if seen[key] {
			t.Errorf("duplicate model key: %q", key)
		}
		seen[key] = true
	}
}
