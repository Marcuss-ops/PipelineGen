package overlays

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// Renderer is the Go boundary around Chronon3d. The C++ process owns pixels;
// Go owns job lifecycle, validation, cache and artifact publication.
type Renderer interface {
	Render(context.Context, []byte, string) error
}

type CommandRenderer struct{ Binary string }

func NewCommandRenderer(binary string) *CommandRenderer { return &CommandRenderer{Binary: binary} }

func (r *CommandRenderer) Render(ctx context.Context, plan []byte, output string) error {
	if r == nil || r.Binary == "" {
		return fmt.Errorf("chronon renderer is not configured")
	}
	planFile, err := os.CreateTemp("", "renderinggen-plan-*.json")
	if err != nil {
		return err
	}
	planPath := planFile.Name()
	defer os.Remove(planPath)
	if _, err := planFile.Write(plan); err != nil {
		planFile.Close()
		return err
	}
	if err := planFile.Close(); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, r.Binary, "--plan", planPath, "--output", output)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("chronon render: %w: %s", err, string(out))
	}
	return nil
}
