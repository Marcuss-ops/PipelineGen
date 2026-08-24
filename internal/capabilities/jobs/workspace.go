package jobs

import (
	"fmt"
	"os"
	"path/filepath"
)

type Workspace struct {
	Root string
}

func NewWorkspace(root string) (*Workspace, error) {
	if root == "" {
		root = filepath.Join(os.TempDir(), "pipelinegen")
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return nil, err
	}
	return &Workspace{Root: root}, nil
}

func (w *Workspace) JobDir(jobID string) string {
	return filepath.Join(w.Root, "jobs", jobID)
}

func (w *Workspace) Prepare(jobID string) (string, error) {
	if jobID == "" {
		return "", fmt.Errorf("job_id required")
	}
	base := w.JobDir(jobID)
	for _, sub := range []string{"input", "work", "output"} {
		if err := os.MkdirAll(filepath.Join(base, sub), 0755); err != nil {
			return "", err
		}
	}
	return base, nil
}

func (w *Workspace) Cleanup(jobID string) error {
	if jobID == "" {
		return nil
	}
	return os.RemoveAll(w.JobDir(jobID))
}
