package drive

import "github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cleanup"

// RunKeepDriveFolderFiles is a compatibility caller for the canonical
// implementation owned by the cleanup command package.
func RunKeepDriveFolderFiles(args []string) error {
	return cleanup.RunKeepDriveFolderFiles(args)
}
