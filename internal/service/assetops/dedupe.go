package assetops

// ShouldSkip determines whether to skip an asset based on the strategy and state
func ShouldSkip(strategy DedupeStrategy, state AssetState) SkipDecision {
	switch strategy {
	case DedupeSkip:
		if state.LocalPathExists {
			return SkipDecision{Skip: true, Reason: "local path exists, skip strategy"}
		}
		if state.DriveLink != "" {
			return SkipDecision{Skip: true, Reason: "drive link exists, skip strategy"}
		}
		return SkipDecision{Skip: false}
	case DedupeVerify:
		return SkipDecision{Skip: false, Replace: false}
	case DedupeReplace:
		return SkipDecision{Skip: false, Replace: true}
	default:
		return SkipDecision{Skip: false}
	}
}
