package workflow

type Dependency struct {
	WorkflowID       string `json:"workflow_id"`
	StepID           string `json:"step_id"`
	DependsOnStepID  string `json:"depends_on_step_id"`
}
