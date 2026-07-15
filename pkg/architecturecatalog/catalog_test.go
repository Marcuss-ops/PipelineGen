package architecturecatalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCatalog(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "catalog.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	path := writeCatalog(t, `schema_version: 1
current: []
issues: []
unknown_governance_key: true
`)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "field unknown_governance_key not found") {
		t.Fatalf("expected unknown-field error, got %v", err)
	}
}

func TestValidateRejectsConcludedStatuses(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"current resolved", `schema_version: 1
current:
  - id: DONE-CURRENT
    status: resolved
    owner: team
    deadline: "2026-09-15T00:00:00Z"
    rationale: done
issues: []
`},
		{"issue wontfix", `schema_version: 1
current: []
issues:
  - id: DONE-ISSUE
    title: done
    status: wontfix
    severity: p2
    owner_capability: pkg/x
    follow_up: [archive]
    opened_date: "2026-07-15"
    tracking_issue: git-history
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Load(writeCatalog(t, tc.body)); err == nil || !strings.Contains(err.Error(), "concluded/unknown status") {
				t.Fatalf("expected concluded-status error, got %v", err)
			}
		})
	}
}

func TestRenderIsDeterministicAndActiveOnly(t *testing.T) {
	path := writeCatalog(t, `schema_version: 1
current:
  - id: ACTIVE-WAVE
    status: in_progress
    owner: team
    deadline: "2026-09-15T00:00:00Z"
    rationale: "work in progress"
    cross_refs:
      z_ref: z
      a_ref: a
issues:
  - id: ACTIVE-ISSUE
    title: active
    status: open
    severity: p1
    owner_capability: pkg/x
    follow_up:
      - fix it
    opened_date: "2026-07-15"
    tracking_issue: plan
`)
	catalog, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	first, err := catalog.RenderCurrent()
	if err != nil {
		t.Fatal(err)
	}
	second, _ := catalog.RenderCurrent()
	if string(first) != string(second) {
		t.Fatal("current rendering is not deterministic")
	}
	if strings.Index(string(first), "a_ref") > strings.Index(string(first), "z_ref") {
		t.Fatal("cross_refs are not sorted")
	}
	issues, err := catalog.RenderIssues()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(first), "resolved") || strings.Contains(string(issues), "wontfix") {
		t.Fatal("concluded status leaked into generated active catalog")
	}
}
