package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// usage writes the CLI help text.
func usage(w *os.File) {
	_, _ = fmt.Fprint(w, `velox — PipelineGen operations CLI

Usage:
  velox preflight --payload FILE [--offline] [--server URL] [--data-dir DIR]
                 [--min-free-gb N] [--json]
  velox submit   <job-alias|--path PATH> --key PROJECT --payload FILE [--m2m] [--json]
  velox poll     <job_id> [--m2m] [--timeout 30m] [--interval 3s] [--json]
  velox replay   <job_id> [--m2m] [--json]
  velox download <asset_id> [--source SOURCE] [-o FILE] [--m2m] [--range BYTES] [--json]
  velox download-final <job_id> [-o FILE] [--m2m] [--token-file FILE] [--json]
  velox burn-subtitles --input FILE --out FILE (--job-full FILE | --ass FILE)
                 [--font NAME] [--font-size PX] [--keep-ass DIR] [--no-verify] [--json]
  velox search   "<query>" [--source SOURCE] [--kind ASSET_KIND] [--role SEMANTIC_ROLE]
                 [--limit N] [--universe U] [--mode hybrid|ann] [--m2m] [--json]
  velox jobs     [--json]
  velox gc       [--data-dir DIR] [--older-than 168h] [--include-cas] [--apply] [--json]
  velox types    --m2m [--json]

Environment:
  VELOX_BASE_URL      server base URL (default http://<loopback>:$VELOX_PORT or :8000)
  VELOX_ADMIN_TOKEN   admin bearer token (job metadata)
  VELOX_ARTIFACT_TOKEN / VELOX_MASTER_ADMIN_TOKEN
                      bearer accepted by the remote Master artifact route
  VELOX_MASTER_TOKEN_FILE
                      optional KEY=VALUE file with the artifact bearer
                      (default fallback: ~/creator-77-master.env)
  VELOX_M2M_SECRET    scoped remote submit/poll bearer token
  VELOX_M2M           set true to use /api/v1/jobs for remote submit/poll
  VELOX_HOME          job-store directory (default ~/.velox)

Job aliases: clips-process, clips-render, render-batch, script-generate, jobs

Preflight:
  velox preflight runs the canonical script.generate ingress validation on a
  payload WITHOUT submitting it (same strict decoder + GenerationEnvelopeV2.Validate
  as POST /api/script/generate), then Stage 0: every bound clip is checked on
  the live catalog (exists, ACTIVE, READY, known duration, text track when the
  segment needs subtitles) and the data directory must have free space.
  Exit 1 means the server would answer 400 OR the run would fail on an asset.
  --offline keeps the structural-only behaviour (no server needed).
  The compiled plan (certified renders, stock isolation, media certificates)
  remains server-side: a green preflight does not promise the run will finish.

M2M mode:
  velox submit jobs --m2m --key PROJECT --payload enqueue.json
  enqueue.json must be {"type":"...","payload":{...}}; the same
  VELOX_M2M_SECRET is used for POST /api/v1/jobs and GET /api/v1/jobs/{id}.
  Media and final render:
    velox download <asset_id> --m2m -o FILE   (GET /api/v1/media/assets/{id}/download)
    velox search "query" --m2m --mode ann      (POST /api/v1/media/search)
    velox download-final <job_id> --m2m -o FILE (GET /api/v1/jobs/{id}/artifact)
`)
}

// flagSet is a tiny positional flag reader. It exists because the standard
// flag package stops at the first positional argument, and these commands mix
// a positional (job_id/asset_id/query) with trailing flags.
type flagSet struct {
	pos   []string
	flags map[string]string
	bools map[string]bool
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
