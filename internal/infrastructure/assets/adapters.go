package assets

import (
	"context"

	appassets "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"
	"go.uber.org/zap"
)

// ProcessRunnerAdapter adapts infrastructure/process to the application-layer ProcessRunner port.
type ProcessRunnerAdapter struct{}

func NewProcessRunnerAdapter() *ProcessRunnerAdapter { return &ProcessRunnerAdapter{} }

func (a *ProcessRunnerAdapter) Run(ctx context.Context, name string, args []string, opts appassets.ProcessOptions) (*appassets.ProcessResult, error) {
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	r, err := process.Run(ctx, name, args, process.Options{
		WorkDir:        opts.WorkDir,
		CombinedOutput: opts.CombinedOutput,
	})
	if err != nil {
		return nil, err
	}
	return &appassets.ProcessResult{
		Stdout: r.Stdout,
		Stderr: r.Stderr,
		Output: r.Output,
	}, nil
}

func (a *ProcessRunnerAdapter) RunSimple(ctx context.Context, name string, args ...string) (*appassets.ProcessResult, error) {
	r, err := process.RunSimple(ctx, name, args...)
	if err != nil {
		return nil, err
	}
	return &appassets.ProcessResult{
		Stdout: r.Stdout,
		Stderr: r.Stderr,
		Output: r.Output,
	}, nil
}

// ToolCheckerAdapter adapts infrastructure/process to the application-layer ToolChecker port.
type ToolCheckerAdapter struct{}

func NewToolCheckerAdapter() *ToolCheckerAdapter { return &ToolCheckerAdapter{} }

func (a *ToolCheckerAdapter) CommandExists(name string) bool {
	return process.CommandExists(name)
}

func (a *ToolCheckerAdapter) LookPath(name string) (string, error) {
	return process.LookPath(name)
}

// DBHealthCheckerAdapter adapts platform/sqlite to the application-layer DBHealthChecker port.
type DBHealthCheckerAdapter struct {
	log *zap.Logger
}

func NewDBHealthCheckerAdapter(log *zap.Logger) *DBHealthCheckerAdapter {
	return &DBHealthCheckerAdapter{log: log}
}

func (a *DBHealthCheckerAdapter) GetAllDBs() []string {
	return storage.GetAllDBs()
}

func (a *DBHealthCheckerAdapter) GetDBPath(dataDir, relPath string) string {
	return storage.GetDBPath(dataDir, relPath)
}

func (a *DBHealthCheckerAdapter) Ping(ctx context.Context, dbPath string) appassets.DBHealthCheckResult {
	db, err := storage.OpenSQLiteDB(dbPath, a.log)
	if err != nil {
		return appassets.DBHealthCheckResult{Error: err.Error()}
	}
	defer db.Close()
	if err := db.DB.Ping(); err != nil {
		return appassets.DBHealthCheckResult{Error: err.Error()}
	}
	return appassets.DBHealthCheckResult{OK: true}
}
