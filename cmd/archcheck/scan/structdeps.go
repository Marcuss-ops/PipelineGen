// Package scan — struct-deps scanner.
//
// scan/structdeps.go owns the "struct_deps" rule family that counts
// fields in Dependencies / Deps / Options structs. Mega-structs that
// bundle >8 mandatory ports circumvent the constructor-parameter gate;
// this scanner catches them at the type level.
package scan

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

var structDepsNames = []string{"Dependencies", "Deps", "Options"}

var optionalFieldTypes = map[string]bool{
	"Logger":          true,
	"int":             true,
	"string":          true,
	"bool":            true,
	"float64":         true,
	"Duration":        true,
	"OperatorOptions": true,
}

var typeDeclRe = regexp.MustCompile(`^\s*type\s+(\w+)\s+struct\s*\{`)

const clipIngestPipelineDepsRelPath = "internal/application/assets/ingest/clip_ingest_pipeline.go"

// ScanStructDeps walks non-test Go source files under <root>/internal/ and
// reports dependency structs whose mandatory field count exceeds the policy
// threshold. ClipIngestPipelineDeps has one explicit per-struct policy knob;
// every other dependency struct uses MaxStructDeps.
func ScanStructDeps(root string, pol *policy.Policy, r *report.Report) {
	if pol.MaxStructDeps <= 0 {
		return
	}

	skipDirs := map[string]bool{
		".git": true, "vendor": true, "node_modules": true,
		"node-scraper": true, "examples": true, "scripts": true,
	}
	internalDir := filepath.Join(root, "internal")

	_ = filepath.WalkDir(internalDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[filepath.Base(path)] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		rel, _ := filepath.Rel(root, path)
		relPath := filepath.ToSlash(rel)

		sc := bufio.NewScanner(f)
		lineNum := 0
		for sc.Scan() {
			lineNum++
			line := sc.Text()
			m := typeDeclRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			typeName := m[1]
			if !isDepsStructName(typeName) {
				continue
			}

			openBrace := strings.Index(line, "{")
			body := line[openBrace:]
			depth := depthOf(body, '{', '}')
			startLine := lineNum
			for depth > 0 && sc.Scan() {
				lineNum++
				next := sc.Text()
				body += "\n" + next
				depth = depthOf(body, '{', '}')
			}

			fieldCount := countMandatoryFields(body)
			threshold := structDepsThreshold(pol, relPath, typeName)
			if fieldCount > threshold {
				r.Violations = append(r.Violations, report.Violation{
					File:         relPath,
					Line:         startLine,
					ActualCount:  fieldCount,
					AllowedCount: threshold,
					MatchedRule:  "max_struct_deps",
					Rule:         "struct_deps",
					Severity:     "warn",
					Note: fmt.Sprintf(
						"struct %s has %d mandatory-port fields (max %d); split into smaller port bundles (e.g. DeliveryPorts + MediaProcessingPorts). Optional fields (*zap.Logger, primitive config) are excluded.",
						typeName, fieldCount, threshold,
					),
				})
			}
		}
		return nil
	})
}

func structDepsThreshold(pol *policy.Policy, relPath, typeName string) int {
	if relPath == clipIngestPipelineDepsRelPath && typeName == "ClipIngestPipelineDeps" && pol.MaxClipIngestPipelineFields > 0 {
		return pol.MaxClipIngestPipelineFields
	}
	return pol.MaxStructDeps
}

func isDepsStructName(name string) bool {
	for _, suffix := range structDepsNames {
		if name == suffix || strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

func depthOf(s string, open, close byte) int {
	d := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case open:
			d++
		case close:
			d--
		}
	}
	return d
}

func countMandatoryFields(body string) int {
	lines := strings.Split(body, "\n")
	depth := 0
	count := 0
	inRawString := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		for i := 0; i < len(line); i++ {
			if line[i] == '`' && !inRawString {
				inRawString = true
			} else if line[i] == '`' && inRawString {
				inRawString = false
			}
		}
		for _, c := range line {
			switch c {
			case '{':
				depth++
			case '}':
				depth--
			}
		}
		if inRawString || depth != 1 {
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || trimmed == "{" || trimmed == "}" {
			continue
		}
		fieldLine := trimmed
		if strings.HasPrefix(fieldLine, "{") {
			fieldLine = strings.TrimSpace(strings.TrimPrefix(fieldLine, "{"))
		}
		if fieldLine == "" || isOptionalField(fieldLine) {
			continue
		}
		count++
	}
	return count
}

func isOptionalField(line string) bool {
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return true
	}
	typeWord := strings.TrimPrefix(parts[len(parts)-1], "*")
	if idx := strings.LastIndex(typeWord, "."); idx >= 0 {
		typeWord = typeWord[idx+1:]
	}
	return optionalFieldTypes[typeWord]
}
