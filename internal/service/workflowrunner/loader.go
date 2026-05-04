package workflowrunner

import (
	"fmt"
	"os"
	"gopkg.in/yaml.v3"
)

// LoadFromFile loads a workflow from a YAML file
func LoadFromFile(path string) (*Workflow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read workflow file: %w", err)
	}
	return LoadFromBytes(data)
}

// LoadFromBytes loads a workflow from YAML bytes
func LoadFromBytes(data []byte) (*Workflow, error) {
	var wf Workflow
	if err := yaml.Unmarshal(data, &wf); err != nil {
		return nil, fmt.Errorf("failed to parse workflow YAML: %w", err)
	}
	if wf.Name == "" {
		return nil, fmt.Errorf("workflow name is required")
	}
	if len(wf.Steps) == 0 {
		return nil, fmt.Errorf("workflow must have at least one step")
	}
	return &wf, nil
}

// Validate checks the workflow for basic correctness
func (wf *Workflow) Validate() error {
	stepIDs := make(map[string]bool)
	for i, step := range wf.Steps {
		if step.ID == "" {
			return fmt.Errorf("step at index %d has empty id", i)
		}
		if stepIDs[step.ID] {
			return fmt.Errorf("duplicate step id: %s", step.ID)
		}
		stepIDs[step.ID] = true

		if step.Type == "" && step.Uses == "" {
			return fmt.Errorf("step %s must have either 'type' or 'uses'", step.ID)
		}
		if step.Type != "" && step.Uses != "" {
			return fmt.Errorf("step %s cannot have both 'type' and 'uses'", step.ID)
		}
	}
	return nil
}
