package qdrantdr

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// StaticRemovalEvidence contains repository-local measurements used by the
// retirement gate. Runtime measurements (complete scans, pending cleanup,
// and reappearances) must come from the operational collector instead.
type StaticRemovalEvidence struct {
	QdrantAllowlistEntries int
	LegacyProductionTests  int
}

// MeasureStaticRemovalEvidence reads the duplicate-type allowlist and counts
// legacy test files under the supplied repository-relative roots. A Qdrant
// allowlist row is scoped explicitly to a path/name containing qdrant or
// qdrantdr; unrelated architecture exceptions do not block Qdrant retirement.
// Missing files or roots fail closed with an error.
func MeasureStaticRemovalEvidence(root, allowlistPath string, legacyTestRoots []string) (StaticRemovalEvidence, error) {
	if strings.TrimSpace(root) == "" {
		return StaticRemovalEvidence{}, errors.New("qdrantdr: repository root is required")
	}
	if strings.TrimSpace(allowlistPath) == "" {
		return StaticRemovalEvidence{}, errors.New("qdrantdr: duplicate-type allowlist path is required")
	}

	allowlist := allowlistPath
	if !filepath.IsAbs(allowlist) {
		allowlist = filepath.Join(root, allowlist)
	}
	f, err := os.Open(allowlist)
	if err != nil {
		return StaticRemovalEvidence{}, err
	}
	defer f.Close()

	var out StaticRemovalEvidence
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		token := strings.Fields(line)[0]
		lower := strings.ToLower(token)
		if strings.Contains(lower, "qdrant") || strings.Contains(lower, "qdrantdr") {
			out.QdrantAllowlistEntries++
		}
	}
	if err := scanner.Err(); err != nil {
		return StaticRemovalEvidence{}, err
	}

	for _, testRoot := range legacyTestRoots {
		path := testRoot
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		info, err := os.Stat(path)
		if err != nil {
			return StaticRemovalEvidence{}, err
		}
		if !info.IsDir() {
			return StaticRemovalEvidence{}, errors.New("qdrantdr: legacy test root is not a directory: " + path)
		}
		err = filepath.WalkDir(path, func(walkPath string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), "_test.go") {
				out.LegacyProductionTests++
			}
			return nil
		})
		if err != nil {
			return StaticRemovalEvidence{}, err
		}
	}
	return out, nil
}
