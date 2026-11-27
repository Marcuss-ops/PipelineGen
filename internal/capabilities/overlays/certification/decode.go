package certification

import (
	"encoding/json"
	"fmt"
)

// DecodePlan accepts either a bare overlay plan or the job projection that
// contains it. The canary bundle stores the bare plan, while an operator may
// point the CLI at the /full job document; both must certify the same bytes.
// Unknown fields are ignored on purpose: the plan wire is versioned and this
// package reads only the fields it owns, so a newer producer does not make an
// older certifier fail.
func DecodePlan(data []byte) (*Plan, error) {
	var direct Plan
	if err := json.Unmarshal(data, &direct); err == nil {
		if direct.SchemaVersion != "" || direct.PlanID != "" || len(direct.Items) > 0 {
			return &direct, nil
		}
	}

	var envelope struct {
		Plan        *Plan `json:"plan"`
		OverlayPlan *Plan `json:"overlay_plan"`
		Result      struct {
			OverlayPlan *Plan `json:"overlay_plan"`
			Result      struct {
				OverlayPlan *Plan `json:"overlay_plan"`
			} `json:"result"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("decode overlay plan: %w", err)
	}
	for _, candidate := range []*Plan{
		envelope.Plan,
		envelope.OverlayPlan,
		envelope.Result.OverlayPlan,
		envelope.Result.Result.OverlayPlan,
	} {
		if candidate != nil && (candidate.SchemaVersion != "" || candidate.PlanID != "" || len(candidate.Items) > 0) {
			return candidate, nil
		}
	}
	return nil, fmt.Errorf("decode overlay plan: no plan found (expected a bare plan or result.result.overlay_plan)")
}

// DecodeExecution reads the neutral worker evidence document.
func DecodeExecution(data []byte) (*ExecutionEvidence, error) {
	var evidence ExecutionEvidence
	if err := json.Unmarshal(data, &evidence); err != nil {
		return nil, fmt.Errorf("decode execution evidence: %w", err)
	}
	if version := evidence.SchemaVersion; version != "" && version != ExecutionSchemaVersion {
		return nil, fmt.Errorf("decode execution evidence: schema %q, want %q", version, ExecutionSchemaVersion)
	}
	return &evidence, nil
}
