package models

import "strings"
import "fmt"

type PipelineStrategy string

const (
	StrategyVerify  PipelineStrategy = "verify"
	StrategySkip    PipelineStrategy = "skip"
	StrategyReplace PipelineStrategy = "replace"
)

func NormalizeStrategy(strategy string, force bool) PipelineStrategy {
	s := PipelineStrategy(strings.ToLower(strings.TrimSpace(strategy)))
	switch s {
	case StrategySkip, StrategyVerify, StrategyReplace:
		return s
	}
	if force {
		return StrategyReplace
	}
	return StrategyVerify
}

func ActiveKey(prefix, term, folderID string, strategy string, dryRun bool) string {
	return fmt.Sprintf("%s|%s|%s|%s|%t",
		prefix,
		term,
		folderID,
		strategy,
		dryRun,
	)
}
