package boundaries

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

const platformPortBoundaryRule = "percheck_platform_port_boundary"

// platformPortInventoryStaleRule is the second half of the same gate: an
// inventory entry that no longer imports the forbidden package is stale and
// must be deleted in the same change that migrates the file. Without this half
// the inventory would silently keep authorising a file that no longer needs it,
// so the ratchet could never tighten.
const platformPortInventoryStaleRule = "percheck_platform_port_inventory_stale"

// platformForbiddenCapabilityPackages is the set of capabilities packages an
// adapter under internal/platform must not import.
//
// Why these packages and not "any capabilities package": a platform adapter
// implementing a PORT owes a dependency in one direction only — it imports the
// port. That is normal hexagonal wiring and it is why this gate does not ban
// platform → capabilities wholesale (378 files legitimately import a capability
// port today). What it bans is the inverse coupling that creates a module-level
// cycle: an adapter importing a capability's BUSINESS package (domain types,
// policy, service layer) instead of a thin port.
//
// `capabilities/scripts` is the god business package (395 files: the whole
// script-generation domain, its planner, its runtime and its model). Before
// 2026-11-18 the RenderingGen adapter imported it for the render-queue wire
// contract, which meant platform/renderinggen could not be built, tested or
// replaced without the business package — while the business package imports
// platform/renderinggen for the transport. The contract now lives in
// internal/kernel/script/renderqueue (neutral owner, imported by both sides);
// this gate is what keeps the adapter off the business package permanently.
var platformForbiddenCapabilityPackages = map[string]string{
	"internal/capabilities/scripts":          "the scripts business package owns the script-generation domain; its wire contracts belong in internal/kernel/script/renderqueue",
	"internal/capabilities/scripts/adapters": "the scripts adapter family is business implementation (planners, processors), not a port",
}

// platformAllowedCapabilitySubpackages are the SUBpackages of a forbidden
// package that stay importable: they are thin port packages (interfaces and
// wire DTOs only, no policy and no service layer), which is exactly the
// dependency direction an adapter owes. `scripts/ports` is today's only such
// package; adding an entry here is a deliberate architectural decision, not a
// convenience.
var platformAllowedCapabilitySubpackages = []string{
	"internal/capabilities/scripts/ports",
}

// platformCapabilityScriptsInventory is the exact-file inventory of adapters
// that already imported a forbidden package on 2026-11-18, when the gate was
// introduced. Every entry is real debt with an owner and a removal trigger; the
// list ratchets DOWN only (a migrating file must delete its entry, and the
// stale half of the gate enforces that).
//
// These are durable-store and Rust-executor adapters whose ports are declared
// inside capabilities/scripts itself (run repositories, render-metrics
// registry, semantic engine contracts). Extracting those ports to the kernel is
// a separate programme; until then the coupling is recorded rather than
// hidden.
var platformCapabilityScriptsInventory = map[string]string{
	"internal/platform/media/rustexec/semantic_adapters.go":               "owner=media-rust; removal_trigger=semantic ports move to kernel/script",
	"internal/platform/media/rustexec/semantic_engine.go":                 "owner=media-rust; removal_trigger=semantic ports move to kernel/script",
	"internal/platform/media/rustexec/semantic_engine_consolidation.go":   "owner=media-rust; removal_trigger=semantic ports move to kernel/script",
	"internal/platform/media/rustexec/semantic_engine_contract.go":        "owner=media-rust; removal_trigger=semantic ports move to kernel/script",
	"internal/platform/media/rustexec/semantic_engine_editorial.go":       "owner=media-rust; removal_trigger=semantic ports move to kernel/script",
	"internal/platform/media/rustexec/semantic_engine_entity_linking.go":  "owner=media-rust; removal_trigger=semantic ports move to kernel/script",
	"internal/platform/media/rustexec/semantic_engine_identity.go":        "owner=media-rust; removal_trigger=semantic ports move to kernel/script",
	"internal/platform/media/rustexec/semantic_engine_phrases.go":         "owner=media-rust; removal_trigger=semantic ports move to kernel/script",
	"internal/platform/media/rustexec/semantic_engine_projection.go":      "owner=media-rust; removal_trigger=semantic ports move to kernel/script",
	"internal/platform/media/rustexec/semantic_engine_search_feedback.go": "owner=media-rust; removal_trigger=semantic ports move to kernel/script", "internal/platform/media/rustexec/semantic_engine_story.go": "owner=media-rust; removal_trigger=semantic ports move to kernel/script",
	"internal/platform/media/rustexec/semantic_engine_quantities.go":     "owner=media-rust; removal_trigger=semantic ports move to kernel/script",
	"internal/platform/media/rustexec/video_processor.go":                "owner=media-rust; removal_trigger=semantic ports move to kernel/script",
	"internal/platform/ollama/adapters/metadata_generator.go":            "owner=ollama-adapters; removal_trigger=script metadata generator port moves to kernel",
	"internal/platform/ollama/adapters/person_image_scorer.go":           "owner=ollama-adapters; removal_trigger=person image scorer port moves to kernel",
	"internal/platform/sqlite/history/reader.go":                         "owner=sqlite-history; removal_trigger=history row types move to kernel",
	"internal/platform/sqlite/jobregistry/execution_recorder.go":         "owner=sqlite-jobregistry; removal_trigger=run/step types move to kernel",
	"internal/platform/sqlite/overlayrendercache/cache.go":               "owner=sqlite-overlaycache; removal_trigger=cache key/artifact types move to kernel",
	"internal/platform/sqlite/performance/source.go":                     "owner=sqlite-performance; removal_trigger=metrics types move to kernel",
	"internal/platform/sqlite/rendermetrics/registry.go":                 "owner=sqlite-rendermetrics; removal_trigger=metrics types move to kernel",
	"internal/platform/sqlite/scripts/artifact_checkpoint_repository.go": "owner=sqlite-scripts; removal_trigger=script run/checkpoint types move to kernel",
	"internal/platform/sqlite/scripts/run_reconciliation.go":             "owner=sqlite-scripts; removal_trigger=script run types move to kernel",
	"internal/platform/sqlite/scripts/run_repository.go":                 "owner=sqlite-scripts; removal_trigger=script run types move to kernel",
	"internal/platform/sqlite/scripts/semantic_plan_cache_adapter.go":    "owner=sqlite-scripts; removal_trigger=cache types move to kernel",
}

// ScanPlatformPortBoundary enforces that no adapter under internal/platform
// imports a capability's business package (see
// platformForbiddenCapabilityPackages), and that the recorded inventory stays
// exact in both directions.
//
// Test files are excluded: a test double may legitimately construct a
// capability type to exercise an adapter, and test code never ships. The
// dependency this gate protects is the one in the compiled adapter.
func ScanPlatformPortBoundary(root string, _ *policy.Policy, r *report.Report) {
	platformRoot := filepath.Join(root, "internal", "platform")
	seen := map[string]bool{}
	_ = filepath.WalkDir(platformRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		scanPlatformAdapterImports(root, path, seen, r)
		return nil
	})
	reportStalePlatformInventory(seen, r)
}

func scanPlatformAdapterImports(root, path string, seen map[string]bool, r *report.Report) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
	rel, _ := filepath.Rel(root, path)
	rel = filepath.ToSlash(rel)
	if err != nil {
		r.Violations = append(r.Violations, report.Violation{
			File: rel, Rule: platformPortBoundaryRule, MatchedRule: "platform_port_parse_error",
			Severity: string(report.SeverityError), Note: "platform adapter cannot be parsed: " + err.Error(),
		})
		return
	}
	const module = "github.com/Marcuss-ops/PipelineGen/"
	for _, imp := range file.Imports {
		importPath := strings.Trim(strings.TrimSpace(imp.Path.Value), `"`)
		reason, forbidden := forbiddenPlatformCapabilityImport(importPath, module)
		if !forbidden {
			continue
		}
		seen[rel] = true
		if _, inventoried := platformCapabilityScriptsInventory[rel]; inventoried {
			continue
		}
		r.Violations = append(r.Violations, report.Violation{
			File: rel, Line: fset.Position(imp.Pos()).Line, Rule: platformPortBoundaryRule,
			MatchedRule: "platform_capability_business_import", Severity: string(report.SeverityError),
			Note: "platform adapter must not import " + importPath + ": " + reason +
				". Move the shared contract to internal/kernel (imported by both sides) or inventory the file in percheck_platform_port_boundary with an owner and a removal trigger.",
		})
	}
}

func forbiddenPlatformCapabilityImport(importPath, module string) (string, bool) {
	if !strings.HasPrefix(importPath, module) {
		return "", false
	}
	internal := strings.TrimPrefix(importPath, module)
	for _, allowed := range platformAllowedCapabilitySubpackages {
		if internal == allowed || strings.HasPrefix(internal, allowed+"/") {
			return "", false
		}
	}
	for pkg, reason := range platformForbiddenCapabilityPackages {
		if internal == pkg || strings.HasPrefix(internal, pkg+"/") {
			return reason, true
		}
	}
	return "", false
}

func reportStalePlatformInventory(seen map[string]bool, r *report.Report) {
	stale := make([]string, 0, len(platformCapabilityScriptsInventory))
	for rel := range platformCapabilityScriptsInventory {
		if !seen[rel] {
			stale = append(stale, rel)
		}
	}
	sort.Strings(stale)
	for _, rel := range stale {
		r.Violations = append(r.Violations, report.Violation{
			File: rel, Rule: platformPortInventoryStaleRule, MatchedRule: "platform_port_inventory_stale",
			Severity: string(report.SeverityError),
			Note:     "inventory entry is stale: this file no longer imports a forbidden capabilities package (or no longer exists). Delete the entry in the same change that migrated it — " + platformCapabilityScriptsInventory[rel],
		})
	}
}
