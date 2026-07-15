package report

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
)

// MarshalJSON adds the checked-out commit identity to every operational report.
// The value is derived at report time so a report cannot silently claim a
// different source revision than the tree that was scanned.
func (r Report) MarshalJSON() ([]byte, error) {
	type alias Report
	return json.Marshal(struct {
		GitCommitSHA string `json:"git_commit_sha"`
		alias
	}{
		GitCommitSHA: resolveGitCommitSHA(r.Root),
		alias:        alias(r),
	})
}

func resolveGitCommitSHA(root string) string {
	if root == "" {
		root = "."
	}
	cmd := exec.Command("git", "-C", root, "rev-parse", "HEAD")
	if out, err := cmd.Output(); err == nil {
		if sha := strings.TrimSpace(string(out)); sha != "" {
			return sha
		}
	}
	if sha := strings.TrimSpace(os.Getenv("GITHUB_SHA")); sha != "" {
		return sha
	}
	return "unknown"
}
