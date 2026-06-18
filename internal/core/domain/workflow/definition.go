package workflow

import "time"

type Definition interface {
	Type() string
	Version() int
	Build(input []byte) ([]StepDefinition, error)
}

type StepDefinition struct {
	Key          string
	Type         string
	Dependencies []string
	MaxAttempts  int
	Timeout      time.Duration
}
