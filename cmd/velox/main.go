// Command velox is the operations CLI that replaces the hand-written shell
// blocks (heredoc quoting, jq over ANSI-tainted bodies, ad-hoc token variables,
// timestamp-based Idempotency-Keys, job ids parked in /tmp) with one typed
// entry point built on pkg/veloxclient.
//
// Why it exists (each is a failure observed in a real session):
//   - Idempotency-Key derived from `date +%s` defeats idempotency; `velox`
//     derives it deterministically from the project name + payload hash.
//   - Job ids written to /tmp expire; `velox` persists them in ~/.velox/jobs.json.
//   - The clip download endpoint is POST-only (a GET returns 404); the client
//     knows this so the operator does not have to.
//   - Polling was a hand-rolled loop with no exit code; `velox poll` blocks
//     until the job is terminal and exits 0/1 accordingly.
//
// Usage: see usage() in flags.go.
//
// Exit codes: 0 ok, 1 failure (job failed / not ready), 2 usage or config
// error, 3 not found.
package main

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
	exitMissing = 3
)

// config is the resolved runtime settings for one invocation.
type config struct {
	baseURL   string
	token     string
	storePath string
	json      bool
	m2m       bool
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		usage(os.Stderr)
		return exitUsage
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "-h", "--help", "help":
		usage(os.Stdout)
		return exitOK
	case "submit":
		return cmdSubmit(rest)
	case "poll":
		return cmdPoll(rest)
	case "replay":
		return cmdReplay(rest)
	case "download":
		return cmdDownload(rest)
	case "search":
		return cmdSearch(rest)
	case "jobs":
		return cmdJobs(rest)
	case "types":
		return cmdTypes(rest)
	default:
		os.Stderr.WriteString("velox: unknown command " + cmd + "\n\n")
		usage(os.Stderr)
		return exitUsage
	}
}

// loadConfig resolves base URL, token, store path and JSON mode from the
// environment. An empty base URL falls back to the canonical local default.
func loadConfig() config {
	base := strings.TrimSpace(os.Getenv("VELOX_BASE_URL"))
	if base == "" {
		base = strings.TrimSpace(os.Getenv("VELOX_MASTER_URL"))
	}
	if base == "" {
		port := strings.TrimSpace(os.Getenv("VELOX_PORT"))
		if port == "" {
			port = "8000"
		}
		base = "http://127.0.0.1:" + port
	}
	m2m := strings.EqualFold(strings.TrimSpace(os.Getenv("VELOX_M2M")), "true")
	token := strings.TrimSpace(os.Getenv("VELOX_ADMIN_TOKEN"))
	if m2m {
		token = strings.TrimSpace(os.Getenv("VELOX_M2M_SECRET"))
	} else if token == "" {
		token = strings.TrimSpace(os.Getenv("VELOX_WORKER_TOKEN"))
	}
	return config{baseURL: base, token: token, storePath: defaultStorePath(), m2m: m2m}
}

func defaultStorePath() string {
	home := strings.TrimSpace(os.Getenv("VELOX_HOME"))
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil || userHome == "" {
			userHome = os.TempDir()
		}
		home = filepath.Join(userHome, ".velox")
	}
	return filepath.Join(home, "jobs.json")
}
