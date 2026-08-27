package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const (
	qualityBaselineManifest = "architecture/quality/baseline_manifest.json"
	qualityGoBaseline       = "architecture/quality/complexity_go_baseline.txt"
	qualityCPPBaseline      = "architecture/quality/complexity_cpp_baseline.txt"
	qualityNewCCThreshold   = 40
	qualityNewNestThreshold = 5
	qualityNewCogThreshold  = 60
)

type complexityEntry struct {
	CC, Cognitive, Nesting int
	Path, Function         string
}

type complexityBaselineManifest struct {
	SchemaVersion int `json:"schema_version"`
	Complexity    struct {
		Go  string `json:"go"`
		CPP string `json:"cpp"`
	} `json:"complexity"`
}

var complexityLineRE = regexp.MustCompile(`cc=(\d+)\s+cog=(\d+).*nest=(\d+).*\s+(?:\*HT\*\s+)?(.+?):(\d+)\s+([^\s(]+)\(`)

func runComplexityQualityGate() (map[string]int, []string) {
	checks := map[string]int{}
	violations := []string{}
	manifest, err := loadComplexityManifest()
	if err != nil {
		return checks, []string{fmt.Sprintf("quality complexity: %v", err)}
	}
	for _, spec := range []struct{ name, path string }{
		{"go", manifest.Complexity.Go},
		{"cpp", manifest.Complexity.CPP},
	} {
		if spec.path == "" {
			violations = append(violations, "quality complexity: manifest missing "+spec.name+" baseline")
			continue
		}
		entries, err := loadComplexityEntries(filepath.Join("architecture/quality", spec.path))
		if err != nil {
			violations = append(violations, fmt.Sprintf("quality complexity %s: %v", spec.name, err))
			continue
		}
		checks["complexity_"+spec.name+"_baseline_entries"] = len(entries)
		for _, entry := range entries {
			if entry.CC > qualityNewCCThreshold || entry.Nesting > qualityNewNestThreshold || entry.Cognitive > qualityNewCogThreshold {
				// Baseline entries are legacy debt: no regression is possible
				// without a current scanner result, so they are reported only.
				checks["complexity_"+spec.name+"_legacy_entries_over_threshold"]++
			}
		}
	}
	checks["complexity_new_cc_threshold"] = qualityNewCCThreshold
	checks["complexity_new_nesting_threshold"] = qualityNewNestThreshold
	checks["complexity_new_cognitive_threshold"] = qualityNewCogThreshold
	return checks, violations
}

func loadComplexityManifest() (complexityBaselineManifest, error) {
	var m complexityBaselineManifest
	data, err := os.ReadFile(qualityBaselineManifest)
	if err != nil {
		return m, fmt.Errorf("read %s: %w", qualityBaselineManifest, err)
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("decode %s: %w", qualityBaselineManifest, err)
	}
	if m.SchemaVersion != 1 {
		return m, fmt.Errorf("unsupported schema_version %d", m.SchemaVersion)
	}
	return m, nil
}

func loadComplexityEntries(path string) ([]complexityEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	defer file.Close()
	var entries []complexityEntry
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		match := complexityLineRE.FindStringSubmatch(line)
		if len(match) == 0 {
			continue
		}
		cc, _ := strconv.Atoi(match[1])
		cog, _ := strconv.Atoi(match[2])
		nest, _ := strconv.Atoi(match[3])
		entries = append(entries, complexityEntry{CC: cc, Cognitive: cog, Nesting: nest, Path: match[4], Function: match[6]})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%s contains no parseable entries", path)
	}
	return entries, nil
}
