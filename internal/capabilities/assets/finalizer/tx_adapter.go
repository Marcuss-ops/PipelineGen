// Package finalizer — canonical transaction bridge for AssetFinalizerTx.
package finalizer

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	"github.com/Marcuss-ops/PipelineGen/pkg/sqlport"
)

// WrapTx exposes a caller-owned SQL transaction as the finalization domain
// transaction. The kernel port is structural: *sql.Tx satisfies it directly,
// so no engine-specific adapter or database/sql import is needed here.
func WrapTx(tx sqlport.WriteTx) finalization.Transaction {
	return tx
}
