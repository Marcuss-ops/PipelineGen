package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
)

var aliasMapping = map[string]string{
	"corid":      "github.com/Marcuss-ops/PipelineGen/pkg/corid",
	"defaults":   "github.com/Marcuss-ops/PipelineGen/pkg/defaults",
	"ptrutil":    "github.com/Marcuss-ops/PipelineGen/pkg/ptrutil",
	"retry":      "github.com/Marcuss-ops/PipelineGen/pkg/retry",
	"concurrent": "github.com/Marcuss-ops/PipelineGen/pkg/concurrent",
	"sliceutil":  "github.com/Marcuss-ops/PipelineGen/pkg/sliceutil",
	"sqlutil":    "github.com/Marcuss-ops/PipelineGen/pkg/sqlutil",
	"textutil":   "github.com/Marcuss-ops/PipelineGen/pkg/textutil",
	"timeutil":   "github.com/Marcuss-ops/PipelineGen/pkg/timeutil",
	"urlutil":    "github.com/Marcuss-ops/PipelineGen/pkg/urlutil",
	"similarity": "github.com/Marcuss-ops/PipelineGen/pkg/similarity",
	"executil":   "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process",
}

func main() {
	root, err := os.Getwd()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			name := info.Name()
			if name == ".git" || name == "vendor" || name == "node_modules" || name == "archcheck" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".go") {
			return nil
		}

		data, err := ioutil.ReadFile(path)
		if err != nil {
			return err
		}

		content := string(data)
		modified := false

		// 1. Process standard alias imports
		for alias, pkgPath := range aliasMapping {
			targetImport := fmt.Sprintf(`%s "github.com/Marcuss-ops/PipelineGen/pkg/ptrutil"`, alias)
			replacement := fmt.Sprintf(`%s "%s"`, alias, pkgPath)
			if strings.Contains(content, targetImport) {
				content = strings.ReplaceAll(content, targetImport, replacement)
				modified = true
			}
		}

		// 2. Special case: un-aliased imports of internal/infrastructure in tests
		// E.g., "github.com/Marcuss-ops/PipelineGen/pkg/ptrutil" -> "github.com/Marcuss-ops/PipelineGen/pkg/corid" (or similar depending on usage)
		// Let's replace un-aliased imports if they are used as a package name
		// For example, if a test file imports it un-aliased, it gets accessed as `platform.XYZ`. We should replace it with the specific pkg.
		if strings.Contains(content, `"github.com/Marcuss-ops/PipelineGen/pkg/ptrutil"`) {
			// Find what's actually being used from `platform`
			if strings.Contains(content, "ptrutil.Ptr") {
				content = strings.ReplaceAll(content, `"github.com/Marcuss-ops/PipelineGen/pkg/ptrutil"`, `"github.com/Marcuss-ops/PipelineGen/pkg/ptrutil"`)
				content = strings.ReplaceAll(content, "ptrutil.Ptr", "ptrutil.Ptr")
				modified = true
			} else if strings.Contains(content, "platform.WithCorrelationID") {
				content = strings.ReplaceAll(content, `"github.com/Marcuss-ops/PipelineGen/pkg/ptrutil"`, `"github.com/Marcuss-ops/PipelineGen/pkg/corid"`)
				content = strings.ReplaceAll(content, "platform.WithCorrelationID", "corid.WithCorrelationID")
				content = strings.ReplaceAll(content, "platform.FromContext", "corid.FromContext")
				modified = true
			} else if strings.Contains(content, "platform.FileIDFromDriveLink") {
				content = strings.ReplaceAll(content, `"github.com/Marcuss-ops/PipelineGen/pkg/ptrutil"`, `"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"`)
				content = strings.ReplaceAll(content, "platform.FileIDFromDriveLink", "urlutil.FileIDFromDriveLink")
				modified = true
			} else if strings.Contains(content, "platform.ExtractVideoID") {
				content = strings.ReplaceAll(content, `"github.com/Marcuss-ops/PipelineGen/pkg/ptrutil"`, `"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"`)
				content = strings.ReplaceAll(content, "platform.ExtractVideoID", "urlutil.ExtractVideoID")
				modified = true
			}
		}

		// 3. Process pf imports in app files (module_artlist, lifecycle, shutdown, dependencies, bootstrap)
		if strings.Contains(content, `pf "github.com/Marcuss-ops/PipelineGen/pkg/ptrutil"`) {
			// Replace pf import with required imports:
			// Let's add the imports to the import block and rewrite the calls.
			// Instead of a generic parser, we can do text replacements:
			content = strings.ReplaceAll(content, `pf "github.com/Marcuss-ops/PipelineGen/pkg/ptrutil"`, "")

			// Replace calls
			content = strings.ReplaceAll(content, "pf.SafeGo", "concurrent.SafeGo")
			content = strings.ReplaceAll(content, "pf.FileIDFromDriveLink", "urlutil.FileIDFromDriveLink")
			content = strings.ReplaceAll(content, "pf.FormatRFC3339", "timeutil.FormatRFC3339")

			// Inject imports (simple approach: insert into import block)
			importBlockEnd := strings.Index(content, ")")
			if importBlockEnd != -1 {
				newImports := "\n\t\"github.com/Marcuss-ops/PipelineGen/pkg/concurrent\"\n\t\"github.com/Marcuss-ops/PipelineGen/pkg/urlutil\"\n\t\"github.com/Marcuss-ops/PipelineGen/pkg/timeutil\"\n"
				content = content[:importBlockEnd] + newImports + content[importBlockEnd:]
			}
			modified = true
		}

		if modified {
			err = ioutil.WriteFile(path, []byte(content), info.Mode())
			if err != nil {
				return err
			}
			fmt.Printf("Refactored: %s\n", path)
		}

		return nil
	})

	if err != nil {
		fmt.Printf("Walk error: %v\n", err)
	}
}
