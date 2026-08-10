package health

import "context"

// ToolsChecker is the application port for checking required CLI tools.
// The concrete PATH lookup lives in internal/infrastructure/process.
type ToolsChecker interface {
	CheckTools(ctx context.Context) (missing []string)
}
