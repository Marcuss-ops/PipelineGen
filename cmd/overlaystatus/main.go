// Command overlaystatus prints the durable per-run state written by the
// script-generation overlay pipeline. It is a read-only operator surface: no
// job is submitted, no database is opened, and the latest stage is shown first.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/overlaystatus"
)

func main() {
	dir := flag.String("dir", overlaystatus.Dir(), "overlay status directory")
	run := flag.String("run", "", "print one run id instead of the latest list")
	asJSON := flag.Bool("json", false, "emit JSON")
	limit := flag.Int("limit", 20, "maximum number of runs to list")
	flag.Parse()

	if *run != "" {
		view, raw, err := overlaystatus.Load(filepath.Join(*dir, overlaystatus.Sanitize(*run)+".json"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "overlay-status: %v\n", err)
			os.Exit(1)
		}
		if *asJSON {
			fmt.Println(string(raw))
			return
		}
		overlaystatus.PrintDetail(view)
		return
	}

	views, err := overlaystatus.List(*dir, *limit)
	if errors.Is(err, overlaystatus.ErrNoDir) {
		fmt.Printf("no overlay status directory yet: %s\n", *dir)
		return
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "overlay-status: %v\n", err)
		os.Exit(1)
	}
	if *asJSON {
		data, _ := json.MarshalIndent(views, "", "  ")
		fmt.Println(string(data))
		return
	}
	if len(views) == 0 {
		fmt.Printf("no overlay status files in %s\n", *dir)
		return
	}
	overlaystatus.PrintList(views)
}
