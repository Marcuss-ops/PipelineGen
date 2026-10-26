// Package assembly contains the canonical PipelineGen <-> Velox contracts.
// It intentionally has no broker, storage, or HTTP dependencies.
package assembly

import (
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

const (
	ContractVersion = "pipelinegen.assembly.v1"
	PrepareJobType  = "assembly.prepare"
	FinalizeJobType = "assembly.finalize"

	// OutputContract is the assembly output contract id. The identity is
	// OWNED by internal/kernel/media (AssemblyMediaContractID); this constant
	// only re-exports it so the assembly wire contract and the media stream
	// contract cannot drift apart by being edited separately.
	OutputContract = media.AssemblyMediaContractID
)

type DispatchMode string

const (
	DispatchNever DispatchMode = "never"
	DispatchEager DispatchMode = "eager"
)

type DispatchPolicy struct {
	Target string       `json:"target,omitempty"`
	Mode   DispatchMode `json:"mode"`
}

type Availability string

const (
	AvailabilityKnown   Availability = "known"
	AvailabilityPending Availability = "pending"
)

type AssetRequirement struct {
	AssetID      string       `json:"asset_id"`
	Kind         string       `json:"kind"`
	SHA256       string       `json:"sha256,omitempty"`
	Location     string       `json:"location,omitempty"`
	Availability Availability `json:"availability"`
	Required     bool         `json:"required"`
}
type TimelineEntry struct {
	SceneID string `json:"scene_id"`
	AssetID string `json:"asset_id"`
}
type PrepareV1 struct {
	ContractVersion string             `json:"contract_version"`
	AssemblyID      string             `json:"assembly_id"`
	ParentJobID     string             `json:"parent_job_id"`
	Revision        uint64             `json:"revision"`
	PreparationHash string             `json:"preparation_hash"`
	OutputContract  string             `json:"output_contract"`
	Assets          []AssetRequirement `json:"assets"`
}
type FinalizeV1 struct {
	ContractVersion string             `json:"contract_version"`
	AssemblyID      string             `json:"assembly_id"`
	PreparationID   string             `json:"preparation_id"`
	Revision        uint64             `json:"revision"`
	OutputContract  string             `json:"output_contract"`
	Timeline        []TimelineEntry    `json:"timeline"`
	RuntimeAssets   []AssetRequirement `json:"runtime_assets,omitempty"`
}
type PrepareResultV1 struct {
	ContractVersion string `json:"contract_version"`
	AssemblyID      string `json:"assembly_id"`
	PreparationID   string `json:"preparation_id"`
	PreparationHash string `json:"preparation_hash"`
	AssetsReady     int    `json:"assets_ready"`
	AssetsMissing   int    `json:"assets_missing"`
	State           string `json:"state"`
}
type FinalizeResultV1 struct {
	ContractVersion string `json:"contract_version"`
	AssemblyID      string `json:"assembly_id"`
	ArtifactID      string `json:"artifact_id"`
	State           string `json:"state"`
	ArtifactPath    string `json:"artifact_path,omitempty"`
}
