package drive

import "github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cleanup"

// RunRemoveDriveFolderRecursive is a compatibility caller for the canonical
// implementation owned by the cleanup command package.
func RunRemoveDriveFolderRecursive(args []string) error {
	return cleanup.RunRemoveDriveFolderRecursive(args)
}
