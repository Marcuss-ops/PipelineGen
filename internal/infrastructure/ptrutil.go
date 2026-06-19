// Package ptrutil provides pointer utilities.
//
// Deprecated: use pkg/ptrutil instead. This file delegates to the canonical
// implementation in pkg/ptrutil/ so call sites can migrate incrementally.
package platform

import (
	"github.com/Marcuss-ops/PipelineGen/pkg/ptrutil"
)

// Deprecated: use pkg/ptrutil.Ptr.
func Bool(v bool) *bool { return ptrutil.Ptr(v) }

// Deprecated: use pkg/ptrutil.Ptr.
func Str(v string) *string { return ptrutil.Ptr(v) }

// Deprecated: use pkg/ptrutil.DerefOr.
func BoolDefault(v *bool, def bool) bool { return ptrutil.DerefOr(v, def) }
