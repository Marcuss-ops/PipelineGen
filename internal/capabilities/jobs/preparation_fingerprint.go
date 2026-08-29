package jobs

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

type PreparationFingerprintInput struct {
	ContractVersion  int               `json:"contract_version"`
	Kind             string            `json:"kind"`
	JobType          string            `json:"job_type"`
	Payload          json.RawMessage   `json:"payload,omitempty"`
	Inputs           map[string]string `json:"inputs,omitempty"`
	DependsOn        []string          `json:"depends_on,omitempty"`
	ProcessorVersion string            `json:"processor_version"`
}

func BuildPreparationFingerprint(input PreparationFingerprintInput) (string, error) {
	if input.ContractVersion <= 0 {
		input.ContractVersion = 1
	}
	input.Kind = strings.TrimSpace(input.Kind)
	input.JobType = strings.TrimSpace(input.JobType)
	input.ProcessorVersion = strings.TrimSpace(input.ProcessorVersion)
	if input.Kind == "" {
		return "", fmt.Errorf("preparation fingerprint kind must not be empty")
	}
	input.Payload = append(json.RawMessage(nil), input.Payload...)
	input.DependsOn = append([]string(nil), input.DependsOn...)
	sort.Strings(input.DependsOn)
	if len(input.Payload) > 0 {
		var payload any
		if err := json.Unmarshal(input.Payload, &payload); err != nil {
			return "", fmt.Errorf("decode preparation payload: %w", err)
		}
		canonical, err := json.Marshal(payload)
		if err != nil {
			return "", fmt.Errorf("canonicalize preparation payload: %w", err)
		}
		input.Payload = canonical
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("marshal preparation fingerprint input: %w", err)
	}
	return digest.SHA256Bytes(raw), nil
}

func PreparationUnitFingerprint(kind, jobType string, payload []byte, inputs map[string]string, dependsOn []string, processorVersion string) (string, error) {
	return BuildPreparationFingerprint(PreparationFingerprintInput{Kind: kind, JobType: jobType, Payload: payload, Inputs: inputs, DependsOn: dependsOn, ProcessorVersion: processorVersion})
}

func NewPreparationUnit(id, kind, jobType string, payload []byte, inputs map[string]string, dependsOn []string, processorVersion string) (PreparationUnit, error) {
	fingerprint, err := PreparationUnitFingerprint(kind, jobType, payload, inputs, dependsOn, processorVersion)
	if err != nil {
		return PreparationUnit{}, err
	}
	return PreparationUnit{ID: id, Kind: kind, Fingerprint: fingerprint, DependsOn: append([]string(nil), dependsOn...), Reusable: true}, nil
}

func (p PreparationPlan) Validate() error {
	if p.JobID == "" {
		return fmt.Errorf("preparation plan job ID must not be empty")
	}
	seen := make(map[string]struct{}, len(p.Units))
	for _, unit := range p.Units {
		if unit.ID == "" || unit.Kind == "" {
			return fmt.Errorf("preparation unit ID and kind must not be empty")
		}
		if unit.Fingerprint == "" {
			return fmt.Errorf("preparation unit %q fingerprint must not be empty", unit.ID)
		}
		if _, exists := seen[unit.ID]; exists {
			return fmt.Errorf("duplicate preparation unit ID %q", unit.ID)
		}
		seen[unit.ID] = struct{}{}
	}
	return nil
}
