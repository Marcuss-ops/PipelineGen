package stockpipeline

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStockPipelineConstructorsRejectMissingConfiguration(t *testing.T) {
	_, err := NewProductionStockPipeline(Deps{})
	require.ErrorIs(t, err, ErrStockPipelineNilCfg)

	_, err = NewTestStockPipeline(Deps{})
	require.ErrorIs(t, err, ErrStockPipelineNilCfg)
}
