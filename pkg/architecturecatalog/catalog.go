// Package architecturecatalog owns the single typed schema used to produce
// architecture/current.yaml and architecture/issues.yaml.
package architecturecatalog

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const SchemaVersion = 1

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Catalog is the only hand-authored architecture work-state source.
type Catalog struct {
	SchemaVersion int            `yaml:"schema_version"`
	Current       []CurrentEntry `yaml:"current"`
	Issues        []IssueEntry   `yaml:"issues"`
}

type Count struct {
	BaselineInitial int `yaml:"baseline_initial"`
	Current         int `yaml:"current"`
	TargetZero      int `yaml:"target_zero"`
}

type FollowUpTicket struct {
	ID   string `yaml:"id"`
	Note string `yaml:"note"`
}

type CurrentEntry struct {
	ID              string            `yaml:"id"`
	Status          string            `yaml:"status"`
	Owner           string            `yaml:"owner"`
	Deadline        string            `yaml:"deadline"`
	Rationale       string            `yaml:"rationale"`
	Count           *Count            `yaml:"count,omitempty"`
	FollowUpTickets []FollowUpTicket  `yaml:"follow_up_tickets,omitempty"`
	CrossRefs       map[string]string `yaml:"cross_refs,omitempty"`
}

// Category is the doctrinal classification of an active issue's root cause.
// Per the Sprint 4.1 reconciliation: every active issue MUST carry exactly one
// of these values so the architecture catalog can be triaged by axis (Code
// defect / Operational environment missing / Live deployment stale /
// Credential not provisioned) instead of by severity alone. Severity
// preserves the within-axis priority.
const (
	CategoryCodeDefect                    = "code_defect"
	CategoryOperationalEnvironmentMissing = "operational_environment_missing"
	CategoryLiveDeploymentStale           = "live_deployment_stale"
	CategoryCredentialNotProvisioned      = "credential_not_provisioned"
)

var validCategories = map[string]struct{}{
	CategoryCodeDefect:                    {},
	CategoryOperationalEnvironmentMissing: {},
	CategoryLiveDeploymentStale:           {},
	CategoryCredentialNotProvisioned:      {},
}

type IssueEntry struct {
	ID               string   `yaml:"id"`
	Title            string   `yaml:"title"`
	Status           string   `yaml:"status"`
	Severity         string   `yaml:"severity"`
	Category         string   `yaml:"category"`
	OwnerCapability  string   `yaml:"owner_capability"`
	FollowUp         []string `yaml:"follow_up"`
	EvidenceFilename string   `yaml:"evidence_filename"`
	OpenedDate       string   `yaml:"opened_date"`
	TrackingIssue    string   `yaml:"tracking_issue"`
}

// Load decodes a catalog with unknown-field rejection and validates it.
func Load(path string) (*Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read architecture catalog %s: %w", path, err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var catalog Catalog
	if err := dec.Decode(&catalog); err != nil {
		return nil, fmt.Errorf("decode architecture catalog %s: %w", path, err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("decode architecture catalog %s: multiple YAML documents are forbidden", path)
		}
		return nil, fmt.Errorf("decode trailing architecture catalog data %s: %w", path, err)
	}
	if err := catalog.Validate(); err != nil {
		return nil, fmt.Errorf("validate architecture catalog %s: %w", path, err)
	}
	return &catalog, nil
}

// Validate enforces active-only status sets and required fields.
func (c *Catalog) Validate() error {
	if c.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schema_version=%d, want %d", c.SchemaVersion, SchemaVersion)
	}
	seen := map[string]string{}
	for i := range c.Current {
		e := &c.Current[i]
		where := fmt.Sprintf("current[%d]", i)
		if err := validateID(e.ID, where, seen); err != nil {
			return err
		}
		if e.Status != "pending" && e.Status != "in_progress" {
			return fmt.Errorf("%s %q has concluded/unknown status %q; active current entries allow only pending or in_progress", where, e.ID, e.Status)
		}
		if strings.TrimSpace(e.Owner) == "" || strings.TrimSpace(e.Rationale) == "" {
			return fmt.Errorf("%s %q requires owner and rationale", where, e.ID)
		}
		if _, err := time.Parse(time.RFC3339, e.Deadline); err != nil {
			return fmt.Errorf("%s %q deadline must be RFC3339: %w", where, e.ID, err)
		}
		if e.Count != nil {
			if e.Count.BaselineInitial < 0 || e.Count.Current < 0 || e.Count.TargetZero < 0 {
				return fmt.Errorf("%s %q count values cannot be negative", where, e.ID)
			}
		}
		for j, ticket := range e.FollowUpTickets {
			if strings.TrimSpace(ticket.ID) == "" || strings.TrimSpace(ticket.Note) == "" {
				return fmt.Errorf("%s %q follow_up_tickets[%d] requires id and note", where, e.ID, j)
			}
		}
	}
	for i := range c.Issues {
		e := &c.Issues[i]
		where := fmt.Sprintf("issues[%d]", i)
		if err := validateID(e.ID, where, seen); err != nil {
			return err
		}
		if e.Status != "open" && e.Status != "in_progress" {
			return fmt.Errorf("%s %q has concluded/unknown status %q; active issues allow only open or in_progress", where, e.ID, e.Status)
		}
		switch e.Severity {
		case "p0", "p1", "p2", "p3":
		default:
			return fmt.Errorf("%s %q has invalid severity %q", where, e.ID, e.Severity)
		}
		if strings.TrimSpace(e.Category) == "" {
			return fmt.Errorf("%s %q requires category (one of: code_defect, operational_environment_missing, live_deployment_stale, credential_not_provisioned)", where, e.ID)
		}
		if _, ok := validCategories[e.Category]; !ok {
			return fmt.Errorf("%s %q has invalid category %q (allowed: code_defect, operational_environment_missing, live_deployment_stale, credential_not_provisioned)", where, e.ID, e.Category)
		}
		if strings.TrimSpace(e.Title) == "" || strings.TrimSpace(e.OwnerCapability) == "" || len(e.FollowUp) == 0 || strings.TrimSpace(e.TrackingIssue) == "" {
			return fmt.Errorf("%s %q requires title, owner_capability, follow_up and tracking_issue", where, e.ID)
		}
		if _, err := time.Parse("2006-01-02", e.OpenedDate); err != nil {
			return fmt.Errorf("%s %q opened_date must be YYYY-MM-DD: %w", where, e.ID, err)
		}
	}
	return nil
}

func validateID(id, where string, seen map[string]string) error {
	if !idPattern.MatchString(id) {
		return fmt.Errorf("%s has invalid id %q", where, id)
	}
	if prior, ok := seen[id]; ok {
		return fmt.Errorf("duplicate id %q in %s and %s", id, prior, where)
	}
	seen[id] = where
	return nil
}

// RenderCurrent produces the active current.yaml compatibility surface.
func (c *Catalog) RenderCurrent() ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString("# Code generated from architecture/catalog.yaml by cmd/admin/regen-current-yaml. DO NOT EDIT.\n")
	b.WriteString("# Active statuses only: pending | in_progress. Completed work belongs in Git history.\n")
	for _, e := range c.Current {
		fmt.Fprintf(&b, "- id: %s\n", yamlString(e.ID))
		fmt.Fprintf(&b, "  status: %s\n", yamlString(e.Status))
		fmt.Fprintf(&b, "  owner: %s\n", yamlString(e.Owner))
		fmt.Fprintf(&b, "  deadline: %s\n", yamlString(e.Deadline))
		fmt.Fprintf(&b, "  rationale: %s\n", yamlString(e.Rationale))
		if e.Count != nil {
			b.WriteString("  count:\n")
			fmt.Fprintf(&b, "    baseline_initial: %d\n", e.Count.BaselineInitial)
			fmt.Fprintf(&b, "    current: %d\n", e.Count.Current)
			fmt.Fprintf(&b, "    target_zero: %d\n", e.Count.TargetZero)
		}
		if len(e.FollowUpTickets) > 0 {
			b.WriteString("  follow_up_tickets:\n")
			for _, ticket := range e.FollowUpTickets {
				fmt.Fprintf(&b, "    - id: %s\n", yamlString(ticket.ID))
				fmt.Fprintf(&b, "      note: %s\n", yamlString(ticket.Note))
			}
		}
		writeStringMap(&b, "  cross_refs", e.CrossRefs)
	}
	return []byte(b.String()), nil
}

// RenderIssues produces the active issues.yaml compatibility surface.
func (c *Catalog) RenderIssues() ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString("# Code generated from architecture/catalog.yaml by cmd/admin/regen-current-yaml. DO NOT EDIT.\n")
	b.WriteString("# Active statuses only: open | in_progress. Completed issues belong in Git history.\n")
	b.WriteString("issues:\n")
	for _, e := range c.Issues {
		fmt.Fprintf(&b, "  - id: %s\n", yamlString(e.ID))
		fmt.Fprintf(&b, "    title: %s\n", yamlString(e.Title))
		fmt.Fprintf(&b, "    status: %s\n", yamlString(e.Status))
		fmt.Fprintf(&b, "    severity: %s\n", yamlString(e.Severity))
		owner := strings.TrimSpace(strings.SplitN(e.OwnerCapability, " (", 2)[0])
		fmt.Fprintf(&b, "    owner: %s\n", yamlString(owner))
	}
	return []byte(b.String()), nil
}

func writeStringMap(b *strings.Builder, key string, values map[string]string) {
	if len(values) == 0 {
		return
	}
	b.WriteString(key + ":\n")
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(b, "    %s: %s\n", k, yamlString(values[k]))
	}
}

func yamlString(s string) string { return strconv.Quote(s) }
