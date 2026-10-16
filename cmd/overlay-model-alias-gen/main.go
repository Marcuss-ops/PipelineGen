// Command overlay-model-alias-gen keeps the generated facade over the overlay
// model leaf (internal/capabilities/overlays/model_alias.go) in sync with the
// model package it re-exports.
//
// The overlay model package is a leaf (types, wire model, validation, chain
// coalescing); the planning layer in package overlays consumes those symbols
// through aliases so the planning files keep their short unqualified names and
// the scripts/platform call sites keep compiling. That alias set is derived,
// never authored: this command is its sole writer.
//
//	Default mode (fail-closed): rebuild the alias set and compare it with the
//	checked-in facade byte-for-byte; exit 1 on drift.
//	--generate: rewrite internal/capabilities/overlays/model_alias.go.
//
// Wiring: run the default mode after moving a symbol between the model leaf and
// the planning layer, so a stale alias set can never be hand-maintained again.
// Follow the same pattern as cmd/model-registry-gen.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/Marcuss-ops/PipelineGen/cmd/overlay-model-alias-gen/aliasgen"
)

func main() {
	generate := flag.Bool("generate", false, "rewrite the overlay model facade from the model package")
	flag.Parse()

	err := aliasgen.Run(*generate)
	if err == nil {
		return
	}
	// Drift is a gate result (exit 1, print the regenerate hint); an I/O or
	// parse failure is a broken invocation (exit 2) — same split as the
	// pre-split command.
	code := 2
	var exit *aliasgen.ExitError
	if errors.As(err, &exit) {
		code = exit.Code
	}
	fmt.Fprintf(os.Stderr, "overlay-model-alias-gen: %v\n", err)
	os.Exit(code)
}
