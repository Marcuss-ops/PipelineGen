package finaljob

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/require"
)

func TestNeedsStockWindowPreparationOwnsTheStockBoundary(t *testing.T) {
	require.False(t, NeedsStockWindowPreparation(contract.GenerateRequest{}),
		"a run without stock has no Master-owned window to prepare")

	require.True(t, NeedsStockWindowPreparation(contract.GenerateRequest{
		StockBindings: []scriptpkg.StockBindingInput{{}},
	}), "an explicit stock binding is Master-owned")

	require.True(t, NeedsStockWindowPreparation(contract.GenerateRequest{
		MediaMode: scriptpkg.MediaMode("stock_only"),
	}), "stock_only is Master-owned")

	require.True(t, NeedsStockWindowPreparation(contract.GenerateRequest{
		MediaMode: scriptpkg.MediaMode("mixed"),
	}), "mixed is Master-owned")
}
