// Package main — routes_yaml_types.go contains the output manifest types
// and HTTP method helpers extracted from generate_routes_yaml.go
// (LONG-FILES-DECOMPOSITION-2026-07-06 Band C #2).
//
// Owns: manifestDocument, manifestRoute, httpMethods, unfoundGinMethods,
// methodByBaseName, isUnfoundGinMethod.
package main

import "strings"

// ── Output shape ─────────────────────────────────────────────────────

type manifestDocument struct {
	Routes []manifestRoute `yaml:"routes"`
}

type manifestRoute struct {
	Method string `yaml:"method"`
	Path   string `yaml:"path"`
	Source string `yaml:"source,omitempty"`
}

// ── HTTP method helpers ──────────────────────────────────────────────

// HTTP methods recognised on *gin.RouterGroup / *gin.Engine receivers.
// Order matches the typical handler-method signature grouping; the
// mapping table itself is unordered.
var httpMethods = []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"}

// gin methods that the walker CANNOT resolve without runtime /
// whole-program analysis. Each emits a per-call warning so the
// operator can detect C2-E gate drift.
var unfoundGinMethods = []string{"Handle", "Any", "Match", "Redirect", "Static", "StaticFS"}

// methodByBaseName returns the uppercase HTTP method if `name`
// matches one of the gin-recognized router methods, otherwise "".
// The check is case-sensitive on the underlying name (gin uses ALL
// CAPS); function-shaped calls like `.Handle(h.Method, path, ...)`
// take the method as a string argument and are NOT matched here.
func methodByBaseName(name string) string {
	upper := strings.ToUpper(name)
	for _, m := range httpMethods {
		if upper == m {
			return m
		}
	}
	return ""
}

// isUnfoundGinMethod reports whether `name` is one of the gin
// router methods that this static-AST walker cannot fold.
func isUnfoundGinMethod(name string) bool {
	for _, m := range unfoundGinMethods {
		if m == name {
			return true
		}
	}
	return false
}
