package application

import (
	"errors"
	"strings"
)

// Module is the application-level facade for the scriptgen module. It
// owns the use cases and exposes them through stable method signatures.
// Agent 1 wraps *Module into an api.Module so it can plug into the
// existing internal/app/registry.go wiring without touching the
// ownership-restricted code paths.
type Module struct {
	deps     Dependencies
	generate *GenerateScript
}

// NewModule is the entry point Agent 1 calls to wire the scriptgen
// module up. It performs minimal validation (no transport or
// third‑party SDK imports) and returns a typed error when required
// dependencies are missing. Agent 1's composition root translates that
// error into a startup failure mode.
//
// Required dependencies: Repo, LLM, Jobs, Assets, Search. Docs and Log
// are optional so dry-runs and partial migrations can still boot.
func NewModule(deps Dependencies) (*Module, error) {
	var missing []string
	if deps.Repo == nil {
		missing = append(missing, "Repo")
	}
	if deps.LLM == nil {
		missing = append(missing, "LLM")
	}
	if deps.Jobs == nil {
		missing = append(missing, "Jobs")
	}
	if deps.Assets == nil {
		missing = append(missing, "Assets")
	}
	if deps.Search == nil {
		missing = append(missing, "Search")
	}
	if len(missing) > 0 {
		return nil, errors.New("scriptgen: missing required dependencies: " + strings.Join(missing, ", "))
	}
	return &Module{
		deps:     deps,
		generate: NewGenerateScript(deps),
	}, nil
}

// MustNewModule panics when dependencies are missing. Intended for
// tests and bootstrappers where failing loud is preferred.
func MustNewModule(deps Dependencies) *Module {
	m, err := NewModule(deps)
	if err != nil {
		panic(err)
	}
	return m
}

// GenerateScript returns the unified use case for script generation.
// Agent 1's bridge passes this to the HTTP handler that owns /api/script.
func (m *Module) GenerateScript() *GenerateScript {
	return m.generate
}

// Health is a typed POCO Agent 1's bridge can map into a /health-like
// endpoint without leaking port internals.
type Health struct {
	OK          bool     `json:"ok"`
	MissingDeps []string `json:"missing_deps,omitempty"`
}

// ReportHealth returns dependency health without touching IO.
func (m *Module) ReportHealth() Health {
	var missing []string
	if m.deps.Repo == nil {
		missing = append(missing, "Repo")
	}
	if m.deps.LLM == nil {
		missing = append(missing, "LLM")
	}
	if m.deps.Jobs == nil {
		missing = append(missing, "Jobs")
	}
	if m.deps.Assets == nil {
		missing = append(missing, "Assets")
	}
	if m.deps.Search == nil {
		missing = append(missing, "Search")
	}
	if m.deps.Docs == nil {
		missing = append(missing, "Docs")
	}
	return Health{
		OK:          len(missing) == 0,
		MissingDeps: missing,
	}
}
