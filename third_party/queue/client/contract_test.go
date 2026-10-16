package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestJobEnvelopeContractFile pins the checked-in envelope contract
// (contracts/renderinggen.job.v1.schema.json) to the wire constants this
// package owns and to the single accepted render_plan boundary.
//
// The contract used to accept BOTH the semantic overlay-plan.v1 and a concrete
// chronon.render-plan.v2 through an anyOf branch, while the runtime accepts
// only the semantic document. A producer following the schema (or the queue
// README that quoted it) was therefore rejected at claim time. This test makes
// the schema a checked projection of the boundary: the concrete branch may not
// come back, and the document identity must match JobSchemaV1/JobSchemaVersionV1.
func TestJobEnvelopeContractFile(t *testing.T) {
	// `go test` runs with the package directory as CWD: queue/client -> repo root.
	path := filepath.Join("..", "..", "contracts", "renderinggen.job.v1.schema.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read job envelope contract: %v", err)
	}

	var doc struct {
		Required   []string `json:"required"`
		Properties struct {
			Schema struct {
				Const string `json:"const"`
			} `json:"schema"`
			Version struct {
				Const int `json:"const"`
			} `json:"version"`
			RenderPlan struct {
				Ref   string            `json:"$ref"`
				AnyOf []json.RawMessage `json:"anyOf"`
			} `json:"render_plan"`
			PublicationPolicy struct {
				Enum []string `json:"enum"`
			} `json:"publication_policy"`
			DaemonAdmissionWaitMS struct {
				Minimum float64 `json:"minimum"`
				Maximum float64 `json:"maximum"`
			} `json:"daemon_admission_wait_ms"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode job envelope contract: %v", err)
	}

	if got := doc.Properties.Schema.Const; got != JobSchemaV1 {
		t.Errorf("contract schema.const = %q, want %q (queue/client owns the value)", got, JobSchemaV1)
	}
	if got := doc.Properties.Version.Const; got != JobSchemaVersionV1 {
		t.Errorf("contract version.const = %d, want %d (queue/client owns the value)", got, JobSchemaVersionV1)
	}
	if !containsString(doc.Properties.PublicationPolicy.Enum, PublicationObjectStoreOnly) || !containsString(doc.Properties.PublicationPolicy.Enum, PublicationObjectStoreAndDrive) {
		t.Errorf("publication_policy enum = %v, want both supported policy constants", doc.Properties.PublicationPolicy.Enum)
	}
	if doc.Properties.DaemonAdmissionWaitMS.Minimum != 0 || doc.Properties.DaemonAdmissionWaitMS.Maximum <= 0 {
		t.Errorf("daemon_admission_wait_ms bounds = [%v,%v], want finite non-negative range", doc.Properties.DaemonAdmissionWaitMS.Minimum, doc.Properties.DaemonAdmissionWaitMS.Maximum)
	}
	for _, field := range []string{"id", "schema", "version", "render_plan", "assets"} {
		if !containsString(doc.Required, field) {
			t.Errorf("contract must require %q", field)
		}
	}

	// The render_plan boundary is semantic-only: exactly one $ref, to
	// overlay-plan.v1. No anyOf, and no concrete Chronon plan anywhere.
	if len(doc.Properties.RenderPlan.AnyOf) != 0 {
		t.Errorf("render_plan must not accept multiple contracts (anyOf has %d branches)", len(doc.Properties.RenderPlan.AnyOf))
	}
	if !strings.HasSuffix(doc.Properties.RenderPlan.Ref, "/overlay-plan.v1.schema.json") {
		t.Errorf("render_plan $ref = %q, want it to end in /overlay-plan.v1.schema.json", doc.Properties.RenderPlan.Ref)
	}
	// No $ref anywhere in the document may point at a concrete Chronon plan;
	// prose may name it, a contract reference may not.
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("decode job envelope contract (generic): %v", err)
	}
	for _, ref := range collectRefs(generic) {
		if strings.Contains(ref, "chronon") {
			t.Errorf("job envelope contract must not $ref a concrete Chronon contract: %q", ref)
		}
	}
}

// collectRefs walks a decoded JSON document and returns every "$ref" value.
func collectRefs(v any) []string {
	var refs []string
	switch node := v.(type) {
	case map[string]any:
		for key, val := range node {
			if key == "$ref" {
				if s, ok := val.(string); ok {
					refs = append(refs, s)
				}
				continue
			}
			refs = append(refs, collectRefs(val)...)
		}
	case []any:
		for _, item := range node {
			refs = append(refs, collectRefs(item)...)
		}
	}
	return refs
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
