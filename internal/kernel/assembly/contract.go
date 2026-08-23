// Package assembly contains the canonical PipelineGen <-> Velox contracts.
// It intentionally has no broker, storage, or HTTP dependencies.
package assembly

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

const (
	ContractVersion = "pipelinegen.assembly.v1"
	PrepareJobType  = "assembly.prepare"
	FinalizeJobType = "assembly.finalize"
	OutputContract  = "VELOX_ASSEMBLY_READY_V1"
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

func (p DispatchPolicy) Enabled() bool { return p.Mode != DispatchNever }

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

func (p PrepareV1) Validate() error {
	if p.ContractVersion != ContractVersion {
		return fmt.Errorf("unsupported assembly contract %q", p.ContractVersion)
	}
	if p.AssemblyID == "" || p.ParentJobID == "" {
		return fmt.Errorf("assembly_id and parent_job_id are required")
	}
	if p.OutputContract != OutputContract {
		return fmt.Errorf("unsupported output contract %q", p.OutputContract)
	}
	if len(p.Assets) == 0 {
		return fmt.Errorf("assets must not be empty")
	}
	for _, a := range p.Assets {
		if err := validateAsset(a); err != nil {
			return err
		}
	}
	return nil
}
func (p FinalizeV1) Validate() error {
	if p.ContractVersion != ContractVersion {
		return fmt.Errorf("unsupported assembly contract %q", p.ContractVersion)
	}
	if p.AssemblyID == "" || p.PreparationID == "" {
		return fmt.Errorf("assembly_id and preparation_id are required")
	}
	if p.OutputContract != OutputContract {
		return fmt.Errorf("unsupported output contract %q", p.OutputContract)
	}
	if len(p.Timeline) == 0 {
		return fmt.Errorf("timeline must not be empty")
	}
	return nil
}
func validateAsset(a AssetRequirement) error {
	if a.AssetID == "" || a.Kind == "" {
		return fmt.Errorf("asset_id and kind are required")
	}
	if a.Required && a.Availability == AvailabilityKnown && a.Location == "" && a.SHA256 == "" {
		return fmt.Errorf("known required asset %q has no location or sha256", a.AssetID)
	}
	return nil
}
func PreparationHash(p PrepareV1) (string, error) {
	p.PreparationHash = ""
	sort.Slice(p.Assets, func(i, j int) bool { return p.Assets[i].AssetID < p.Assets[j].AssetID })
	b, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return "sha256:" + digest.SHA256Bytes(b), nil
}
