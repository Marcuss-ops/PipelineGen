package overlayplan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/animreg"
	overlayplan "github.com/Marcuss-ops/PipelineGen/internal/kernel/overlayplan"
)

// contractTargetRegistryTargets maps a contract target to the registry targets
// that certify it. A `map_callout` is an image card anchored on a pin, so an
// image/map_view motion certifies it; it is not a second motion id.
var contractTargetRegistryTargets = map[string][]string{
	"phrase":      {"phrase", "text", "short_phrase", "long_phrase", "bullet", "caption"},
	"image":       {"image"},
	"date":        {"date"},
	"metric":      {"metric"},
	"caption":     {"caption", "text"},
	"map":         {"map_view"},
	"map_callout": {"image", "map_view"},
}

// TestMotionCapabilitiesMatchTheContractCorpus keeps the embedded copy and the
// workspace corpus file identical. Without this gate the planner and the
// contract could disagree on which motion may join a chain.
func TestMotionCapabilitiesMatchTheContractCorpus(t *testing.T) {
	corpusPath := filepath.Join("..", "..", "..", "..", "..",
		"contracts", "editorial-composition-v1", "capabilities", "motion_capabilities.json")
	raw, err := os.ReadFile(corpusPath)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skip("workspace contract corpus is not available; embedded catalog cannot be compared")
		}
		t.Fatalf("read contract corpus %s: %v", corpusPath, err)
	}
	corpus, err := overlayplan.ParseMotionCapabilities(raw)
	if err != nil {
		t.Fatalf("parse contract corpus: %v", err)
	}
	embedded, err := overlayplan.CanonicalMotionCapabilities()
	if err != nil {
		t.Fatalf("parse embedded catalog: %v", err)
	}
	if !reflect.DeepEqual(corpus, embedded) {
		t.Fatalf("embedded catalog drifted from %s: copy the corpus file to "+
			"internal/kernel/overlayplan/motion_capabilities.v1.json", corpusPath)
	}
	if len(embedded.Motions) < 8 {
		t.Fatalf("catalog declares %d motions, need at least 8", len(embedded.Motions))
	}
}

// TestMotionCapabilitiesExistInTheRegistry is ED-24: every declared motion must
// be a real, active, renderer-supported registry entry whose certified targets
// cover the contract target. A declared id that does not exist is a runtime
// failure waiting for the first render.
func TestMotionCapabilitiesExistInTheRegistry(t *testing.T) {
	catalog, err := overlayplan.CanonicalMotionCapabilities()
	if err != nil {
		t.Fatalf("parse embedded catalog: %v", err)
	}
	for _, motion := range catalog.Motions {
		motion := motion
		t.Run(motion.MotionID, func(t *testing.T) {
			if _, ok := animreg.AnimationEntryFamilies(motion.MotionID); !ok {
				t.Fatalf("%q is declared but is not in animation_registry.v1.json", motion.MotionID)
			}
			if animreg.AnimationIsRetired(motion.MotionID) {
				t.Fatalf("%q is declared but the registry marks it retired", motion.MotionID)
			}
			if !animreg.AnimationRendererSupported(motion.MotionID) {
				t.Fatalf("%q is declared but the registry does not certify the renderer", motion.MotionID)
			}
			targets, ok := contractTargetRegistryTargets[motion.Target]
			if !ok {
				t.Fatalf("%q declares an unknown contract target %q", motion.MotionID, motion.Target)
			}
			supported := false
			for _, target := range targets {
				if animreg.AnimationSupportsTarget(motion.MotionID, target) {
					supported = true
					break
				}
			}
			if !supported {
				t.Fatalf("%q declares target %q but the registry certifies none of %v", motion.MotionID, motion.Target, targets)
			}
		})
	}
}

// TestV2RequestCarriesMotionCapabilities pins the wire: the catalog must travel
// in the request, otherwise the planner keeps the legacy prefix rule.
func TestV2RequestCarriesMotionCapabilities(t *testing.T) {
	catalog, err := overlayplan.CanonicalMotionCapabilities()
	if err != nil {
		t.Fatalf("parse embedded catalog: %v", err)
	}
	payload, err := json.Marshal(overlayplan.V2Request{
		SchemaVersion:      overlayplan.SchemaVersionV2,
		MotionCapabilities: catalog,
	})
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	raw, ok := wire["motion_capabilities"]
	if !ok {
		t.Fatal("request carries no motion_capabilities section")
	}
	var decoded overlayplan.MotionCapabilities
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode motion_capabilities: %v", err)
	}
	if len(decoded.Motions) != len(catalog.Motions) {
		t.Fatalf("wire carries %d motions, want %d", len(decoded.Motions), len(catalog.Motions))
	}
}

// TestMotionCapabilitiesDisagreeOnTheSamePrefix is the ED-08 invariant: two
// motions with the same prefix get opposite chain verdicts from the catalog.
func TestMotionCapabilitiesDisagreeOnTheSamePrefix(t *testing.T) {
	catalog, err := overlayplan.CanonicalMotionCapabilities()
	if err != nil {
		t.Fatalf("parse embedded catalog: %v", err)
	}
	verdicts := make(map[string]bool, len(catalog.Motions))
	for _, motion := range catalog.Motions {
		verdicts[motion.MotionID] = motion.SupportsChain && motion.SupportsSharedCanvas
	}
	if !verdicts["short_phrase_editorial_focus_resolve"] {
		t.Fatal("short_phrase_editorial_focus_resolve must be declared chain capable")
	}
	if verdicts["short_phrase_burst"] {
		t.Fatal("short_phrase_burst must not be chain capable")
	}
}
