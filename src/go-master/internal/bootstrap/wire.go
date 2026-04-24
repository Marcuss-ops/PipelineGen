package bootstrap

import (
	"strings"

	"go.uber.org/zap"
	"velox/go-master/internal/api"
	"velox/go-master/internal/api/handlers/common"
	"velox/go-master/internal/api/handlers/script"
	"velox/go-master/internal/core/job"
	"velox/go-master/internal/core/worker"
	"velox/go-master/internal/runtime"
	"velox/go-master/pkg/config"
)

// AppDeps holds the minimal initialized dependencies for the server.
type AppDeps struct {
	RouterDeps    *api.RouterDepsWithHandlers
	JobService    *job.Service
	WorkerService *worker.Service
	ServiceGroup  *runtime.ServiceGroup
	Cleanup       func()
}

// WireServices initializes the minimal server composition root.
func WireServices(cfg *config.Config, log *zap.Logger) (*AppDeps, error) {
	return WireScriptDocs(cfg, log)
}

// WireScriptDocs initializes the minimal text->doc server.
func WireScriptDocs(cfg *config.Config, log *zap.Logger) (*AppDeps, error) {
	coreDeps, coreClean, err := initCoreMinimal(cfg, log)
	if err != nil {
		return nil, err
	}

	driveDeps, driveClean, err := initDrive(cfg, log)
	if err != nil {
		if coreClean != nil {
			coreClean()
		}
		return nil, err
	}

	scriptDocsHandler := script.NewScriptDocsHandler(
		coreDeps.ScriptGen,
		driveDeps.DriveHandler.GetDocClient(),
		cfg.Storage.DataDir,
	)

	handlers := &api.Handlers{
		Health:     common.NewHealthHandler(cfg, coreDeps.JobService, coreDeps.WorkerService),
		Drive:      driveDeps.DriveHandler,
		ScriptDocs: scriptDocsHandler,
		Utility:    coreDeps.Utility,
	}

	routerDeps := &api.RouterDepsWithHandlers{
		Handlers: handlers,
		Deps: &api.RouterDeps{
			ScriptGen:     coreDeps.ScriptGen,
			OllamaClient:  coreDeps.OllamaClient,
			EntityService: coreDeps.EntityService,
		},
	}

	sg := runtime.NewServiceGroup(log)
	cleanup := func() {
		_ = sg.Stop()
		if driveClean != nil {
			driveClean()
		}
		if coreClean != nil {
			coreClean()
		}
	}

	return &AppDeps{
		RouterDeps:    routerDeps,
		JobService:    coreDeps.JobService,
		WorkerService: coreDeps.WorkerService,
		ServiceGroup:  sg,
		Cleanup:       cleanup,
	}, nil
}

// WireMinimal is kept for compatibility with local tools.
func WireMinimal(cfg *config.Config, log *zap.Logger) (*AppDeps, error) {
	return WireScriptDocs(cfg, log)
}

func runCleanups(cleanups []CleanupFunc) {
	for i := len(cleanups) - 1; i >= 0; i-- {
		if cleanups[i] != nil {
			cleanups[i]()
		}
	}
}

func isAlreadyRunningSyncErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "already running")
}
