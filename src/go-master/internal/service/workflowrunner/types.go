package workflowrunner

import "context"

// Workflow represents a declarative YAML workflow
type Workflow struct {
	Name     string                 `yaml:"name"`
	Defaults map[string]interface{} `yaml:"defaults"`
	Steps    []Step                 `yaml:"steps"`
}

// Step represents a single step in the workflow
type Step struct {
	ID       string                 `yaml:"id"`
	Type     string                 `yaml:"type"` // "http", "http_job", "service"
	Uses     string                 `yaml:"uses"`
	Endpoint string                 `yaml:"endpoint"`
	Payload  map[string]interface{} `yaml:"payload"`
	Wait     *WaitConfig            `yaml:"wait"`
	With     map[string]interface{} `yaml:"with"`
	Needs    []string               `yaml:"needs"`
}

// WaitConfig defines how to wait for async operations
type WaitConfig struct {
	StatusEndpoint string   `yaml:"status_endpoint"`
	StatusPath     string   `yaml:"status_path"`
	Success        []string `yaml:"success"`
	Failure        []string `yaml:"failure"`
	IntervalMS     int      `yaml:"interval_ms"`
	TimeoutSeconds int      `yaml:"timeout_seconds"`
}

// StepExecutor is the interface for executing a workflow step
type StepExecutor interface {
	Execute(ctx context.Context, input *StepInput) (*StepOutput, error)
}

// StepInput contains the input for a step execution
type StepInput struct {
	Workflow *Workflow
	Step     *Step
	Payload  map[string]interface{}
	State    *WorkflowState
}

// AssetItem represents a single processed media asset
type AssetItem struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Source    string `json:"source"`
	URL       string `json:"url,omitempty"`
	LocalPath string `json:"local_path,omitempty"`
	DriveLink string `json:"drive_link,omitempty"`
	Hash      string `json:"hash,omitempty"`
	Status    string `json:"status,omitempty"`
	Error     string `json:"error,omitempty"`
}

// StepOutput contains the output from a step execution (standardized)
type StepOutput struct {
	OK         bool                   `json:"ok"`
	Status     string                 `json:"status"`
	RunID      string                 `json:"run_id,omitempty"`
	Items      []AssetItem            `json:"items,omitempty"`
	FolderID   string                 `json:"folder_id,omitempty"`
	FolderPath string                 `json:"folder_path,omitempty"`
	DriveLinks []string               `json:"drive_links,omitempty"`
	LocalPaths []string               `json:"local_paths,omitempty"`
	FileHashes []string               `json:"file_hashes,omitempty"`
	Error      string                 `json:"error,omitempty"`
	Raw        map[string]interface{} `json:"raw,omitempty"`
}

// WorkflowState holds the state of a running workflow
type WorkflowState struct {
	WorkflowID  string
	Status      string
	StepOutputs map[string]*StepOutput
	CurrentStep int
	Error       string
}
