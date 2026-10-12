package rustexec

// Scene Intelligence codec bridge (shadow mode).
//
// The Rust module (`pipelinegen-muscles::scene_intelligence`) is the
// authority for text signals; this bridge is the single typed seam between
// its JSON and the Go canonical contract (`kernel/script`
// SceneIntelligenceManifest.v1). It encodes canonical analysis requests and
// decodes + validates manifests. Process execution wiring (stdio op next to
// the phrase-impact worker) belongs to a later explicit phase; shadow mode
// consumes Rust-produced JSON (golden fixtures, saved runs) without
// changing any video.

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// SISceneRequest is the worker-facing alias of the kernel analysis input.
type SISceneRequest = scriptpkg.SISceneAnalysisInput

// SIAnalysisRequest is the canonical request envelope.
type SIAnalysisRequest struct {
	SchemaVersion string           `json:"schema_version"`
	Scenes        []SISceneRequest `json:"scenes"`
}

// EncodeSIAnalysisRequest marshals the canonical request JSON: scenes sorted
// by id, entities sorted per scene, so identical analyses hash identically.
func EncodeSIAnalysisRequest(scenes []SISceneRequest) ([]byte, error) {
	if len(scenes) == 0 {
		return nil, errors.New("scene intelligence request requires scenes")
	}
	cp := append([]SISceneRequest(nil), scenes...)
	for i := range cp {
		ents := append([]string(nil), cp[i].Entities...)
		sort.Strings(ents)
		cp[i].Entities = ents
	}
	sort.SliceStable(cp, func(i, j int) bool { return cp[i].SceneID < cp[j].SceneID })
	for _, s := range cp {
		if strings.TrimSpace(s.SceneID) == "" || strings.TrimSpace(s.Text) == "" {
			return nil, errors.New("scene intelligence request requires scene id and text")
		}
		if s.VideoEndMS <= s.VideoStartMS {
			return nil, errors.New("scene intelligence request requires a valid video window")
		}
	}
	return json.Marshal(SIAnalysisRequest{
		SchemaVersion: scriptpkg.SceneIntelligenceManifestVersion,
		Scenes:        cp,
	})
}

// DecodeSIManifest parses Rust-produced manifest JSON into the canonical
// contract and fail-closed validates it. Unknown schema, qualitative
// comparisons carrying values, unknown relations and malformed references are
// rejected here — never passed to planners. Semantic graph cardinality is
// preserved; a visual budget belongs to downstream admission.
func DecodeSIManifest(raw []byte) (*scriptpkg.SceneIntelligenceManifest, error) {
	var m scriptpkg.SceneIntelligenceManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}
