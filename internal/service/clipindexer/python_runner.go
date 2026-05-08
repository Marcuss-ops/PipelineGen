package clipindexer

import (
	"context"
	"fmt"
)

// PythonRunner runs Python scripts for indexing
type PythonRunner struct {
	pythonBin  string
	scriptPath string
}

// NewPythonRunner creates a new Python runner
func NewPythonRunner(pythonBin, scriptPath string) *PythonRunner {
	return &PythonRunner{
		pythonBin:  pythonBin,
		scriptPath: scriptPath,
	}
}

// Run executes the Python indexing script
func (r *PythonRunner) Run(ctx context.Context, args ...string) error {
	// This is a placeholder - actual implementation would use exec.Command
	// For now, just log
	_ = ctx
	_ = args
	return fmt.Errorf("python runner not yet implemented")
}
