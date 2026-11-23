package core

// NewStockDispatcherWriter adapts the outbox-shaped dispatcher port to the
// TransactionalAssetWriter contract.
func NewStockDispatcherWriter(dispatcher StockChunkDispatcher) TransactionalAssetWriter {
	return StockDispatcherWriter{dispatcher: dispatcher}
}
