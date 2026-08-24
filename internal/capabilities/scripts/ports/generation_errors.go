package ports

import "errors"

var (
	ErrScriptGeneratorUnavailable  = errors.New("scripts: script generator is not configured")
	ErrScriptGenerationEmptyResult = errors.New("scripts: script generator returned an empty result")
)
