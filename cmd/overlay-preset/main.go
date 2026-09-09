package main

import (
	"fmt"
	"os"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: overlay-preset <job-fingerprint> <scene-id> <item-id>")
		os.Exit(2)
	}
	fmt.Println(overlays.SelectEntityImagePreset(os.Args[1], os.Args[2], os.Args[3]))
}
