package script

import (
	"encoding/json"
	"fmt"
	"strings"
)

// The script.generate envelope declares its contract in `version`. Two
// versions are accepted, and the field is no longer decorative:
//
//   - 2 — the legacy wire. It keeps accepting the duplicate spellings that
//     Phase 1 reconciled with lint warnings (script_params.force_refresh,
//     script_params.voiceover_group, output.audio), so the recorded job
//     corpus replays byte-identically.
//   - 3 — the canonical wire. Every fact has exactly ONE owner: those three
//     duplicate keys are rejected instead of being reinterpreted, so a
//     payload can never carry a value that the runtime silently discards.
//
// Any other value fails closed. Before this existed the field had to be 2 and
// a body declaring 3 was accepted while nothing read the value — the same
// no-fake-availability bug as the retired `news` preset.
const (
	EnvelopeVersionLegacy    = 2
	EnvelopeVersionCanonical = 3
)

// SupportedEnvelopeVersions lists every accepted version for error messages.
func SupportedEnvelopeVersions() []int {
	return []int{EnvelopeVersionLegacy, EnvelopeVersionCanonical}
}

// IsSupportedEnvelopeVersion reports whether the envelope version is one this
// build implements.
func IsSupportedEnvelopeVersion(version int) bool {
	return version == EnvelopeVersionLegacy || version == EnvelopeVersionCanonical
}

// canonicalOnlyRetiredFields lists the V2-only spellings that version 3
// refuses. Each path uses "*" for one array element. The reason names the
// surviving owner, so the 400 is actionable instead of a diff exercise.
var canonicalOnlyRetiredFields = []struct {
	path   []string
	reason string
}{
	{
		path:   []string{"items", "*", "script_params", "force_refresh"},
		reason: `is V2-only: the item-level flag never bypasses the idempotency store; set the envelope-level "force_refresh" instead`,
	},
	{
		path:   []string{"items", "*", "script_params", "voiceover_group"},
		reason: `is V2-only: use "output.voiceover_group", the value the voiceover router resolves`,
	},
	{
		path:   []string{"items", "*", "output", "audio"},
		reason: `is V2-only: use the item-level "audio" block, which has the same shape and is the one the runner reads`,
	},
	// The legacy map spellings are one scalar each of the map_presentation
	// contract. Version 3 refuses them instead of relying on the caller: mixing
	// them with map_presentation already failed closed, so keeping them meant
	// every map fix had two ways to be expressed and one of them silently
	// resolved a smaller vocabulary.
	{
		path:   []string{"items", "*", "map_type"},
		reason: `is V2-only: name the basemap with "map_presentation.style" (chronon_editorial, esri_topo, esri_sat or random)`,
	},
	{
		path:   []string{"items", "*", "map_camera_animation"},
		reason: `is V2-only: name the camera move with "map_presentation.camera"`,
	},
	{
		path:   []string{"items", "*", "map_label_animation"},
		reason: `is V2-only: the map_presentation composition draws its own labels; omit the key instead of pinning a label animation`,
	},
}

// RetiredContractFieldReason reports the first V2-only key present in a body
// that declares version 3. It returns "" for every other version and for a
// body it cannot parse (the strict decoder owns that error). The message
// names the concrete JSON path, e.g. items[1].output.audio.
func RetiredContractFieldReason(body []byte, version int) string {
	if version != EnvelopeVersionCanonical {
		return ""
	}
	var doc any
	if err := json.Unmarshal(body, &doc); err != nil {
		return ""
	}
	for _, field := range canonicalOnlyRetiredFields {
		if where, found := locateJSONPath(doc, field.path); found {
			return fmt.Sprintf("%s: %s", where, field.reason)
		}
	}
	return ""
}

// locateJSONPath resolves a path whose "*" segment matches any element of an
// array, returning the concrete path (items[0].output.audio) of the first
// match. Objects that are missing a segment simply do not match.
func locateJSONPath(node any, path []string) (string, bool) {
	if len(path) == 0 {
		return "", false
	}
	head := path[0]
	if head == "*" {
		list, ok := node.([]any)
		if !ok {
			return "", false
		}
		for i, child := range list {
			where, found := locateJSONPath(child, path[1:])
			if !found {
				continue
			}
			if strings.HasPrefix(where, "[") {
				return fmt.Sprintf("[%d]%s", i, where), true
			}
			return fmt.Sprintf("[%d].%s", i, where), true
		}
		return "", false
	}
	object, ok := node.(map[string]any)
	if !ok {
		return "", false
	}
	child, present := object[head]
	if !present {
		return "", false
	}
	if len(path) == 1 {
		return head, true
	}
	where, found := locateJSONPath(child, path[1:])
	if !found {
		return "", false
	}
	if strings.HasPrefix(where, "[") {
		return head + where, true
	}
	return head + "." + where, true
}
