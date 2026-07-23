package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Debt is scored in half-open units to avoid floating point arithmetic:
// open=2 units, in_progress=2 units. The policy's max_pre_existing_open is
// multiplied by two, so in_progress consumes the same capacity as open
// and cannot be used to bypass the gate.
const (
	debtOpenUnits       = 2
	debtInProgressUnits = 2
)

type debtEntry struct {
	ID                string
	Status            string
	OwnerCapability   string
	EvidenceFilename  string
	TrackingIssue     string
	ImplementationRef string
}

var commitRefRE = regexp.MustCompile(`\b[0-9a-fA-F]{7,40}\b`)

func validateDebtBudget(root, policyPath string) error {
	capOpen, err := readDebtCap(filepath.Join(root, policyPath))
	if err != nil {
		return err
	}
	catalogPath := filepath.Join(root, "architecture", "issues.yaml")
	currentData, err := os.ReadFile(catalogPath)
	if err != nil {
		return fmt.Errorf("debt budget: read catalog: %w", err)
	}
	entries := parseDebtEntries(string(currentData))

	parentStatuses := map[string]string{}
	if parent, err := exec.Command("git", "-C", root, "show", "HEAD^:architecture/issues.yaml").Output(); err == nil {
		for _, entry := range parseDebtEntries(string(parent)) {
			parentStatuses[entry.ID] = entry.Status
		}
	}
	changedConcrete := hasConcreteChange(root)

	units := 0
	var offenders []string
	for _, entry := range entries {
		if !strings.HasPrefix(entry.ID, "PRE-EXISTING-") {
			continue
		}
		switch entry.Status {
		case "open":
			units += debtOpenUnits
			offenders = append(offenders, entry.ID+"(open=1.0)")
		case "in_progress":
			units += debtInProgressUnits
			offenders = append(offenders, entry.ID+"(in_progress=1.0)")
			if entry.OwnerCapability == "" || entry.EvidenceFilename == "" {
				return fmt.Errorf("debt budget: %s is in_progress but missing owner_capability or evidence_filename", entry.ID)
			}
			if !hasConcreteImplementation(root, entry) {
				return fmt.Errorf("debt budget: %s is in_progress without a resolvable implementation commit/evidence reference", entry.ID)
			}
			if parentStatuses[entry.ID] == "open" && !changedConcrete {
				return fmt.Errorf("debt budget: %s changed open -> in_progress without any non-governance code or operational modification in this commit", entry.ID)
			}
		}
	}

	maxUnits := capOpen * debtOpenUnits
	if units > maxUnits {
		return fmt.Errorf("debt budget: weighted PRE-EXISTING score %.1f exceeds cap %.1f; entries: %s",
			float64(units)/2, float64(maxUnits)/2, strings.Join(offenders, ", "))
	}
	fmt.Fprintf(os.Stderr, "debt budget: weighted score %.1f / %.1f (open=1.0, in_progress=1.0)\n",
		float64(units)/2, float64(maxUnits)/2)
	return nil
}

func readDebtCap(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("debt budget: read policy: %w", err)
	}
	defer file.Close()
	inDebt := false
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "debt_budget:" {
			inDebt = true
			continue
		}
		if inDebt && len(line) > 0 && line[0] != ' ' && !strings.HasPrefix(trimmed, "#") {
			break
		}
		if inDebt && strings.HasPrefix(trimmed, "max_pre_existing_open:") {
			value := strings.TrimSpace(strings.TrimPrefix(trimmed, "max_pre_existing_open:"))
			capOpen, err := strconv.Atoi(value)
			if err != nil || capOpen < 0 {
				return 0, fmt.Errorf("debt budget: invalid max_pre_existing_open %q", value)
			}
			return capOpen, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return 0, fmt.Errorf("debt budget: policy has no debt_budget.max_pre_existing_open")
}

func parseDebtEntries(data string) []debtEntry {
	var entries []debtEntry
	var current *debtEntry
	flush := func() {
		if current != nil && current.ID != "" {
			entries = append(entries, *current)
		}
	}
	for _, line := range strings.Split(data, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- id:") {
			flush()
			current = &debtEntry{ID: yamlScalar(strings.TrimSpace(strings.TrimPrefix(trimmed, "- id:")))}
			continue
		}
		if current == nil {
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		value = yamlScalar(strings.TrimSpace(value))
		switch key {
		case "status":
			current.Status = value
		case "owner_capability":
			current.OwnerCapability = value
		case "evidence_filename":
			current.EvidenceFilename = value
		case "tracking_issue":
			current.TrackingIssue = value
		case "implementation_ref":
			current.ImplementationRef = value
		}
	}
	flush()
	return entries
}

func yamlScalar(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
		return value[1 : len(value)-1]
	}
	return value
}

func hasConcreteImplementation(root string, entry debtEntry) bool {
	for _, candidate := range []string{entry.ImplementationRef, entry.TrackingIssue, entry.EvidenceFilename} {
		for _, sha := range commitRefRE.FindAllString(candidate, -1) {
			if exec.Command("git", "-C", root, "cat-file", "-e", sha+"^{commit}").Run() == nil {
				return true
			}
		}
	}
	if entry.EvidenceFilename != "" {
		path := filepath.Join(root, filepath.FromSlash(entry.EvidenceFilename))
		if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Size() > 0 {
			return true
		}
	}
	return false
}

func hasConcreteChange(root string) bool {
	out, err := exec.Command("git", "-C", root, "diff", "--name-only", "HEAD^", "HEAD").Output()
	if err != nil {
		return false
	}
	for _, raw := range strings.Split(string(out), "\n") {
		path := strings.TrimSpace(raw)
		if path == "" {
			continue
		}
		if path == "architecture/issues.yaml" || path == "architecture/current.yaml" || path == "architecture/policy.yaml" || strings.HasPrefix(path, "docs/operations/debt-budget") {
			continue
		}
		return true
	}
	return false
}
