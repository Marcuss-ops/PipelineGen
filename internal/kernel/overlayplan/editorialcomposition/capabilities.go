package editorialcomposition

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Catalog is the declared capability surface of the motion registry. Chain and
// hold participation is read from here; the compiler never classifies a motion
// by an id prefix.
type Catalog struct {
	SchemaVersion   string             `json:"schema_version"`
	CatalogRevision string             `json:"catalog_revision,omitempty"`
	Description     string             `json:"description,omitempty"`
	Motions         []MotionCapability `json:"motions"`

	byID map[string]MotionCapability
}

// MotionCapability is one motion's declared participation.
type MotionCapability struct {
	MotionID               string `json:"motion_id"`
	Family                 string `json:"family,omitempty"`
	Target                 string `json:"target"`
	SupportsChain          bool   `json:"supports_chain"`
	SupportsSharedCanvas   bool   `json:"supports_shared_canvas"`
	SupportsPersistentHold bool   `json:"supports_persistent_hold"`
	MaxHoldUS              int64  `json:"max_hold_us,omitempty"`
}

// Motion targets.
const (
	TargetPhrase     = "phrase"
	TargetImage      = "image"
	TargetDate       = "date"
	TargetMetric     = "metric"
	TargetCaption    = "caption"
	TargetMap        = "map"
	TargetMapCallout = "map_callout"
	TargetChart      = "chart"
)

// ParseCatalog strictly parses a capability catalog. Duplicate ids and an
// unexpected schema version are errors: a catalog that cannot be trusted must
// not be used to admit a composition.
func ParseCatalog(raw []byte) (*Catalog, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var catalog Catalog
	if err := decoder.Decode(&catalog); err != nil {
		return nil, fmt.Errorf("decode motion capabilities: %w", err)
	}
	if catalog.SchemaVersion != CapabilitiesSchemaVersion {
		return nil, &Error{Code: CodeUnsupportedSchema, Path: "capabilities.schema_version", Detail: catalog.SchemaVersion}
	}
	catalog.byID = make(map[string]MotionCapability, len(catalog.Motions))
	for index, motion := range catalog.Motions {
		id := strings.TrimSpace(motion.MotionID)
		if id == "" {
			return nil, &Error{Code: CodeMissingField, Path: fmt.Sprintf("capabilities.motions[%d].motion_id", index)}
		}
		if strings.TrimSpace(motion.Target) == "" {
			return nil, &Error{Code: CodeMissingField, Path: fmt.Sprintf("capabilities.motions[%d].target", index), Detail: id}
		}
		if _, duplicate := catalog.byID[id]; duplicate {
			return nil, &Error{Code: CodeDuplicateMotion, Path: "capabilities.motions", Detail: id}
		}
		motion.MotionID = id
		catalog.byID[id] = motion
	}
	return &catalog, nil
}

// Lookup returns the declared capability of one motion id.
func (c *Catalog) Lookup(motionID string) (MotionCapability, bool) {
	if c == nil {
		return MotionCapability{}, false
	}
	capability, ok := c.byID[strings.TrimSpace(motionID)]
	return capability, ok
}

// ChainCapable reports whether a motion may join a shared-canvas link group.
// This is the replacement for the `short_phrase_` prefix rule: an unknown
// motion is not chain capable, and neither is a motion that declares
// supports_chain=false.
func (c *Catalog) ChainCapable(motionID string) bool {
	capability, ok := c.Lookup(motionID)
	return ok && capability.SupportsChain && capability.SupportsSharedCanvas
}

// HoldCapable reports whether a motion can keep its composed state while later
// members enter, and whether the requested hold fits the declared cap.
func (c *Catalog) HoldCapable(motionID string, holdUS int64) bool {
	capability, ok := c.Lookup(motionID)
	if !ok || !capability.SupportsPersistentHold {
		return false
	}
	if capability.MaxHoldUS > 0 && holdUS > capability.MaxHoldUS {
		return false
	}
	return true
}

// Len returns the number of declared motions.
func (c *Catalog) Len() int {
	if c == nil {
		return 0
	}
	return len(c.Motions)
}
