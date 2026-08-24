// cmd/admin/cleanup_test_youtube.go — test YouTube service wiring
package cleanup

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
)

func RunTestYouTube(args []string) error {
	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	deps, err := app.WireServices(cfg, log, "")
	if err != nil {
		log.Error("Failed to wire services", zap.Error(err))
		return err
	}

	fmt.Println("Services wired successfully!")
	fmt.Printf("Registry: %v\n", deps.Handlers.Registry != nil)

	if deps.Runtime.Lifecycle != nil {
		// AGENTS.md §7 post-write save ctx — admin one-shot composition
		// root; deferred lifecycle.Stop drains pending work and must
		// survive the deferred caller-cancel scope so all teardown
		// work runs to completion.
		defer deps.Runtime.Lifecycle.Stop(context.Background())
	}
	return nil
}
