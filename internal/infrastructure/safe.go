// Deprecated: use pkg/concurrent.SafeGo / SafeGoFunc instead.
// This file delegates to the canonical implementation in pkg/concurrent/.
package platform

import (
	pkgconcurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// Deprecated: use pkg/concurrent.SafeGo.
func SafeGo(name string, fn func()) { pkgconcurrent.SafeGo(name, fn) }

// Deprecated: use pkg/concurrent.SafeGoFunc.
func SafeGoFunc[T any](name string, arg T, fn func(T)) { pkgconcurrent.SafeGoFunc(name, arg, fn) }
