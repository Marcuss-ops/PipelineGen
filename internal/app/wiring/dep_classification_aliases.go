package wiring

import "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/buildbundle"

type DepClassification = buildbundle.DepClassification

const (
	DepRequired         = buildbundle.DepRequired
	DepOptionalDisabled = buildbundle.DepOptionalDisabled
	DepTestOnly         = buildbundle.DepTestOnly
)

var (
	ClassifyDepGet        = buildbundle.ClassifyDepGet
	ErrRequiredDepMissing = buildbundle.ErrRequiredDepMissing
	ErrTestOnlyDepMissing = buildbundle.ErrTestOnlyDepMissing
)
