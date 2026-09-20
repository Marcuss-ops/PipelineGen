package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// usage writes the CLI help text.
func usage(w *os.File) {
	fmt.Fprint(w, `velox — PipelineGen operations CLI

Usage:
  velox submit   <job-alias|--path PATH> --key PROJECT --payload FILE [--json]
  velox poll     <job_id> [--timeout 30m] [--interval 3s] [--json]
  velox replay   <job_id> [--json]
  velox download <asset_id> [--source SOURCE] [-o FILE]
  velox search   "<query>" [--source SOURCE] [--limit N] [--universe U] [--json]
  velox jobs     [--json]

Environment:
  VELOX_BASE_URL      server base URL (default http://127.0.0.1:$VELOX_PORT or :8000)
  VELOX_ADMIN_TOKEN   admin bearer token
  VELOX_HOME          job-store directory (default ~/.velox)

Job aliases: clips-process, clips-render, render-batch, script-generate, jobs
`)
}

// flagSet is a tiny positional flag reader. It exists because the standard
// flag package stops at the first positional argument, and these commands mix
// a positional (job_id/asset_id/query) with trailing flags.
type flagSet struct {
	pos   []string
	flags map[string]string
	bools map[string]bool
	json  bool
}

// parseFlags reads `-name value`, `--name value`, `--name=value` and bare
// `--name` pairs, treating any non-flag token as positional.
func parseFlags(args []string) (*flagSet, error) {
	fs := &flagSet{flags: map[string]string{}, bools: map[string]bool{}}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			fs.pos = append(fs.pos, a)
			continue
		}
		name := strings.TrimLeft(a, "-")
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			fs.flags[name[:eq]] = name[eq+1:]
			continue
		}
		// A flag whose next token is another flag (or absent) is boolean.
		if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
			fs.bools[name] = true
			continue
		}
		fs.flags[name] = args[i+1]
		i++
	}
	return fs, nil
}

func (f *flagSet) get(name, def string) string {
	if v, ok := f.flags[name]; ok {
		return v
	}
	return def
}

func (f *flagSet) has(name string) bool {
	if _, ok := f.flags[name]; ok {
		return true
	}
	return f.bools[name]
}

// attrInt parses an optional integer flag, falling back to def.
func attrInt(s string, def int) int {
	if strings.TrimSpace(s) == "" {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}
