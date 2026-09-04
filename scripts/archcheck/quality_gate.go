package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

const (
	qualityGoBaseline       = "architecture/quality/complexity_go_baseline.txt"
	qualityNewCCThreshold   = 40
	qualityNewNestThreshold = 5
	qualityNewCogThreshold  = 60
)

type complexityEntry struct {
	CC, Cognitive, Nesting int
	Path, Function         string
}

var complexityLineRE = regexp.MustCompile(`cc=(\d+)\s+cog=(\d+).*nest=(\d+).*\s+(?:\*HT\*\s+)?(.+?):(\d+)\s+([^\s(]+)\(`)

func runComplexityQualityGate() (map[string]int, []string) {
	checks := map[string]int{}
	entries, err := loadComplexityEntries(qualityGoBaseline)
	if err != nil {
		return checks, []string{fmt.Sprintf("quality complexity go: %v", err)}
	}
	checks["complexity_go_baseline_entries"] = len(entries)
	for _, entry := range entries {
		if entry.CC > qualityNewCCThreshold || entry.Nesting > qualityNewNestThreshold || entry.Cognitive > qualityNewCogThreshold {
			// Baseline entries are legacy debt: no regression is possible
			// without a current scanner result, so they are reported only.
			checks["complexity_go_legacy_entries_over_threshold"]++
		}
	}
	checks["complexity_new_cc_threshold"] = qualityNewCCThreshold
	checks["complexity_new_nesting_threshold"] = qualityNewNestThreshold
	checks["complexity_new_cognitive_threshold"] = qualityNewCogThreshold
	return checks, nil
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
