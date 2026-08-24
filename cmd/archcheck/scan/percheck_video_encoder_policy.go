// Package scan — forward-prevention gate for centralized video encoder policy.
//
// Video encoder selection is an infrastructure policy decision. FFmpeg argv
// builders must receive the resolved codec from the canonical resolver instead
// of embedding a concrete encoder at a call site. This gate scans active
// production Go code with the Go AST, so assignments, comparisons, and
// multiline argv builders are covered uniformly while comments and tests
// remain non-fatal. The contract is intentionally conservative: a concrete
// encoder literal anywhere in governed production code is a violation unless
// it belongs to an explicitly registered policy owner.
package scan

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

const (
	videoEncoderPolicyRule        = "percheck_video_encoder_policy"
	videoEncoderPolicyMatchedRule = "hardcoded_video_encoder_outside_resolver"
)

var concreteVideoEncoders = map[string]bool{
	"libx264":           true,
	"libx265":           true,
	"libvpx-vp9":        true,
	"mpeg4":             true,
	"h264_nvenc":        true,
	"hevc_nvenc":        true,
	"av1_nvenc":         true,
	"h264_vaapi":        true,
	"h264_qsv":          true,
	"h264_amf":          true,
	"h264_videotoolbox": true,
}

var videoEncoderPolicySkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	"node-scraper": true,
	"examples":     true,
	"scripts":      true,
	"out":          true,
}

// The resolver is the sole owner of runtime encoder selection. The scanner
// itself contains probe literals and is exempt. Configuration and the stock
// pipeline's default profile are policy inputs, not FFmpeg command builders;
// they remain explicit allowlisted owners of default policy values.
var videoEncoderPolicyAllowPrefixes = []string{
	// Retained for the scanner's synthetic canonical-resolver fixtures;
	// the production package was removed after migration to rustexec.
	"internal/infrastructure/media/ffmpeg/encoder_resolver.go",
	"internal/platform/config/video.go",
	"internal/capabilities/assets/providers/stock/stockpipeline/service_types.go",
	"cmd/archcheck/scan",
}

// Scan production Go trees rather than only today's FFmpeg packages. This
// makes the rule forward-preventive: a new cmd/admin, application, or
// infrastructure command builder cannot bypass the policy simply by landing
// outside the original media directories. The narrow allowlist above keeps
// declarative policy inputs and the resolver itself free of false positives.
var videoEncoderPolicyScopes = []string{
	"internal/",
	"cmd/",
	"pkg/",
}

const videoEncoderPolicyNote = "hardcoded video encoder in an active FFmpeg command-builder path outside the canonical resolver; pass the resolved codec through the central encoder policy and RunWithEncoderPolicy"

// ScanVideoEncoderPolicy reports concrete video encoder literals used in
// governed production code outside the canonical resolver and registered
// policy-input owners.
func ScanVideoEncoderPolicy(root string, _ *policy.Policy, r *report.Report) {
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if videoEncoderPolicySkipDirs[entry.Name()] {
				return filepath.SkipDir
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr == nil && videoEncoderPolicyAllowed(filepath.ToSlash(rel)) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if videoEncoderPolicyAllowed(relSlash) || !videoEncoderPolicyInScope(relSlash) {
			return nil
		}
		scanVideoEncoderPolicyFile(path, relSlash, r)
		return nil
	})
}

func videoEncoderPolicyAllowed(relPath string) bool {
	for _, prefix := range videoEncoderPolicyAllowPrefixes {
		if relPath == prefix || strings.HasPrefix(relPath, prefix+"/") {
			return true
		}
	}
	return false
}

func videoEncoderPolicyInScope(relPath string) bool {
	for _, scope := range videoEncoderPolicyScopes {
		if strings.HasPrefix(relPath, scope) {
			return true
		}
	}
	return false
}

func scanVideoEncoderPolicyFile(path, relPath string, r *report.Report) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return
	}
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err != nil || !concreteVideoEncoders[value] {
			return true
		}
		position := fset.Position(literal.Pos())
		r.Violations = append(r.Violations, report.Violation{
			File:        relPath,
			Line:        position.Line,
			Rule:        videoEncoderPolicyRule,
			Severity:    string(report.SeverityError),
			MatchedRule: videoEncoderPolicyMatchedRule,
			Note:        videoEncoderPolicyNote + "; encoder=" + value,
		})
		return true
	})
}
