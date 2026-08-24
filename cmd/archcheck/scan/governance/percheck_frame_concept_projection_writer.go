// Package scan — percheck_frame_concept_projection_writer.go: forward-prevention
// gate that bans point writes to the media_frames / media_concepts projections
// outside their respective projection writers (PR-HASH-SEMANTICS item 8/16,
// August 2026).
//
// godlike/06 SSOT (projection separation): a frame is not an asset and a
// concept is not an asset. Each has its own collection and its own canonical
// writer:
//
//	media_frames   → internal/platform/qdrant/qdrantmm/qdrant_frame_indexer.go
//	media_concepts → internal/platform/qdrant/qdrantmm/qdrant_indexer.go
//
// Point writes (UpsertProjection/DeleteProjection via the generic
// ProjectionWriter, or UpsertPoints/DeletePoints via the raw transport client)
// that target FrameCollectionName/ConceptCollectionName must originate ONLY in
// the respective writer file. Any other production site — including a NEW file
// in the qdrantmm package, or a frame writer writing the concept collection —
// fails the build. This is the frame/concept complement of
// percheck_upsert_points_sole_owner (which already bans raw UpsertPoints /
// DeletePoints outside indexing/ + qdrantmm/); this gate adds the
// ProjectionWriter methods and the collection-target precision.
//
// Scope (deliberately narrow):
//   - Skip *_test.go, vendor/example dirs, and the scanner's own package.
//   - Read paths (e.g. HybridSearchPoints) and collection-lifecycle calls
//     (e.g. CollectionManager.CreateCollection) are NOT point writes and are
//     therefore not matched.
//   - The respective writer may write ONLY its own collection: a frame writer
//     writing ConceptCollectionName (or vice-versa) is a violation.
//
// matched rule_id: `percheck_frame_concept_projection_writer`.
package governance

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// frameConceptWriterSkipDirs is the standard skip-dir set (mirrors the
// sibling percheck scanners).
var frameConceptWriterSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	"node-scraper": true,
	"examples":     true,
	"archivist":    true,
	"docs":         true,
	"data":         true,
}

// frameConceptWriterSkipPathPrefixes is the scan's own package exemption.
var frameConceptWriterSkipPathPrefixes = []string{
	"cmd/archcheck/scan",
}

// frameConceptAuthorizedWriters maps each authorized projection-writer FILE to
// the ONLY collection identifier it may write. The frame writer owns
// FrameCollectionName; the concept writer owns ConceptCollectionName. Writing
// the other collection — even from within qdrantmm — is a cross-projection
// violation.
var frameConceptAuthorizedWriters = map[string]string{
	"internal/platform/qdrant/qdrantmm/qdrant_frame_indexer.go": "FrameCollectionName",
	"internal/platform/qdrant/qdrantmm/qdrant_indexer.go":       "ConceptCollectionName",
}

// frameConceptWriterScanScope is the prefix the gate applies to.
const frameConceptWriterScanScope = "internal/"

// frameConceptWriterRule is the rule-family id the scanner emits.
const frameConceptWriterRule = "percheck_frame_concept_projection_writer"

// frameConceptWriteMethodRE matches a point-write call site: the generic
// ProjectionWriter methods (UpsertProjection/DeleteProjection) and the raw
// transport methods (UpsertPoints/DeletePoints). The dot-receiver requirement
// naturally exempts the method declarations (`func (c *Client) UpsertPoints(...)`
// and interface method declarations have no dot receiver).
var frameConceptWriteMethodRE = regexp.MustCompile(`\.\s*(?:UpsertProjection|DeleteProjection|UpsertPoints|DeletePoints)\s*\(`)

// frameConceptCollectionRE matches the canonical collection identifiers. It is
// independent of the write-method matcher so a write targeting a frame/concept
// collection is detected regardless of argument order on the line.
var frameConceptCollectionRE = regexp.MustCompile(`\b(FrameCollectionName|ConceptCollectionName)\b`)

// frameConceptWriterNote is the violation Note for a non-canonical
// frame/concept point write.
const frameConceptWriterNote = "forbidden point write to a frame/concept projection outside its respective projection writer (PR-HASH-SEMANTICS item 8/16, August 2026); godlike/06 SSOT requires media_frames writes to originate ONLY in internal/platform/qdrant/qdrantmm/qdrant_frame_indexer.go and media_concepts writes ONLY in internal/platform/qdrant/qdrantmm/qdrant_indexer.go, through the generic ProjectionWriter. A write from any other site (including a frame writer targeting the concept collection, or vice-versa) bypasses the fail-closed envelope and risks corrupting the projection boundary."

// ScanFrameConceptProjectionWriter walks every .go file under internal/** and
// emits a violation for any point-write call site that targets the frame or
// concept collection outside the respective projection writer file.
func ScanFrameConceptProjectionWriter(root string, pol *policy.Policy, r *report.Report, _ bool) {
	_ = pol // reserved for future SeverityOverride plumbing.

	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if frameConceptWriterSkipDirs[base] {
				return filepath.SkipDir
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr == nil {
				if hasAnyPathPrefix(filepath.ToSlash(rel), frameConceptWriterSkipPathPrefixes) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if !strings.HasPrefix(relSlash, frameConceptWriterScanScope) {
			return nil
		}
		scanFrameConceptWriterFile(path, relSlash, r)
		return nil
	})
}

// scanFrameConceptWriterFile opens a single .go file and emits
// percheck_frame_concept_projection_writer violations for any point-write call
// site targeting a frame/concept collection that is not the file's authorized
// (respective) collection.
func scanFrameConceptWriterFile(path, relPath string, r *report.Report) {
	allowedCollection, isAuthorized := frameConceptAuthorizedWriters[relPath]

	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
			continue
		}
		if !frameConceptWriteMethodRE.MatchString(line) {
			continue
		}
		m := frameConceptCollectionRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		collection := m[1]
		// The respective writer may write its OWN collection only. Any other
		// site — non-writer file, or the other projection's writer — is a
		// violation.
		if isAuthorized && collection == allowedCollection {
			continue
		}
		r.Violations = append(r.Violations, report.Violation{
			Package:     filepath.ToSlash(filepath.Dir(relPath)),
			File:        relPath,
			Line:        lineNo,
			Rule:        frameConceptWriterRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "non_canonical_frame_concept_write",
			Note:        frameConceptWriterNote + " | collection: " + collection + " | snippet: " + truncateFrameConceptWriter(line),
		})
	}
}

// truncateFrameConceptWriter bounds the snippet surface at 120 chars to keep
// report JSON size stable (mirrors the sibling percheck scanners).
func truncateFrameConceptWriter(s string) string {
	const maxLen = 120
	const marker = " <<<"
	if len(s) > maxLen {
		return s[:maxLen] + marker
	}
	return s
}
