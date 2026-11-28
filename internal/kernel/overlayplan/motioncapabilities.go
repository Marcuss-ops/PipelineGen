package overlayplan

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"
)

// MotionCapabilitiesSchemaVersion is the version of the declared capability
// catalog carried in the request.
const MotionCapabilitiesSchemaVersion = "motion_capabilities.v1"

// motionCapabilitiesSource is the embedded copy of
// `contracts/editorial-composition-v1/capabilities/motion_capabilities.json`.
// The corpus owns the file; the copy exists because an embedded asset cannot
// point outside the Go module. A test compares the two and fails on drift.
//
//go:embed motion_capabilities.v1.json
var motionCapabilitiesSource []byte

// MotionCapabilities is the declared motion capability catalog. Rust reads
// chain and hold participation from here instead of classifying a motion by an
// id prefix (ED-08): `short_phrase_burst` stays standalone while
// `short_phrase_editorial_focus_resolve` may join a link group, and the two
// share the same prefix.
type MotionCapabilities struct {
	SchemaVersion   string             `json:"schema_version"`
	CatalogRevision string             `json:"catalog_revision,omitempty"`
	Description     string             `json:"description,omitempty"`
	Motions         []MotionCapability `json:"motions"`
}

// MotionCapability is one motion's declared participation.
type MotionCapability struct {
	MotionID string `json:"motion_id"`
	Family   string `json:"family,omitempty"`
	// Target is the composition role the motion may drive: phrase, image,
	// date, metric, caption, map or map_callout.
	Target                 string `json:"target"`
	SupportsChain          bool   `json:"supports_chain"`
	SupportsSharedCanvas   bool   `json:"supports_shared_canvas"`
	SupportsPersistentHold bool   `json:"supports_persistent_hold"`
	MaxHoldUS              int64  `json:"max_hold_us,omitempty"`
}

var (
	motionCapabilitiesOnce   sync.Once
	motionCapabilitiesParsed *MotionCapabilities
	motionCapabilitiesErr    error
)

// CanonicalMotionCapabilities returns the parsed embedded catalog. It is parsed
// once: the catalog is immutable input, not run state.
func CanonicalMotionCapabilities() (*MotionCapabilities, error) {
	motionCapabilitiesOnce.Do(func() {
		motionCapabilitiesParsed, motionCapabilitiesErr = ParseMotionCapabilities(motionCapabilitiesSource)
	})
	return motionCapabilitiesParsed, motionCapabilitiesErr
}

// ParseMotionCapabilities strictly parses a declared capability catalog:
// unknown fields, an unexpected schema version and duplicate motion ids are
// errors, so an untrusted catalog never decides a chain.
func ParseMotionCapabilities(raw []byte) (*MotionCapabilities, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var catalog MotionCapabilities
	if err := decoder.Decode(&catalog); err != nil {
		return nil, fmt.Errorf("decode motion capabilities: %w", err)
	}
	if catalog.SchemaVersion != MotionCapabilitiesSchemaVersion {
		return nil, fmt.Errorf("motion capabilities: unsupported schema %q", catalog.SchemaVersion)
	}
	seen := make(map[string]struct{}, len(catalog.Motions))
	for index := range catalog.Motions {
		motion := &catalog.Motions[index]
		if motion.MotionID == "" {
			return nil, fmt.Errorf("motion capabilities: motions[%d] has no motion_id", index)
		}
		if motion.Target == "" {
			return nil, fmt.Errorf("motion capabilities: %s has no target", motion.MotionID)
		}
		if _, duplicate := seen[motion.MotionID]; duplicate {
			return nil, fmt.Errorf("motion capabilities: duplicate motion %q", motion.MotionID)
		}
		seen[motion.MotionID] = struct{}{}
	}
	if len(catalog.Motions) == 0 {
		return nil, fmt.Errorf("motion capabilities: catalog is empty")
	}
	return &catalog, nil
}
