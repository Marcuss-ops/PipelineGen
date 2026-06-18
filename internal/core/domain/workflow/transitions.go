package workflow

import "fmt"

var (
	ErrWorkflowNotFound = fmt.Errorf("workflow not found")
	ErrStepNotFound     = fmt.Errorf("workflow step not found")
)
