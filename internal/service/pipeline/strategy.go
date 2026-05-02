package pipeline

import "strings"

type Strategy string

const (
	StrategyVerify  Strategy = "verify"
	StrategySkip    Strategy = "skip"
	StrategyReplace Strategy = "replace"
)

func NormalizeStrategy(strategy string, force bool) Strategy {
	s := Strategy(strings.ToLower(strings.TrimSpace(strategy)))
	switch s {
	case StrategySkip, StrategyVerify, StrategyReplace:
		return s
	}
	if force {
		return StrategyReplace
	}
	return StrategyVerify
}
