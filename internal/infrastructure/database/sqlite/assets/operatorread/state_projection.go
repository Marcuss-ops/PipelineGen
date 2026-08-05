package operatorread

// assetStateProjectionSQL is the read-only compatibility projection of the
// two authoritative media-assets state dimensions. lifecycle_state owns
// lifecycle/deletion; index_state owns embedding and Qdrant progress.
//
// The physical asset_state column remains only for older API consumers and is
// maintained by migration 189 triggers. Operator reads derive the value here
// so stale compatibility data cannot become authoritative again.
func assetStateProjectionSQL(alias string) string {
	return "CASE " +
		"WHEN " + alias + ".lifecycle_state IN ('DELETED', 'INDEX_DELETED') OR " + alias + ".index_state = 'DELETED' THEN 'FAILED_PERMANENT' " +
		"WHEN " + alias + ".index_state IN ('EMBEDDING_FAILED', 'INDEXING_FAILED') THEN 'FAILED_RETRYABLE' " +
		"WHEN " + alias + ".lifecycle_state IN ('STAGING', 'PREPARING', 'PROCESSING') THEN 'DISCOVERED' " +
		"WHEN " + alias + ".index_state = 'NOT_INDEXABLE' THEN 'UPLOADED' " +
		"WHEN " + alias + ".index_state = 'DISCOVERED' THEN 'DISCOVERED' " +
		"WHEN " + alias + ".index_state = 'EMBEDDING' THEN 'TRANSLATED' " +
		"WHEN " + alias + ".index_state IN ('EMBEDDED', 'INDEXING') THEN 'INDEX_PENDING' " +
		"WHEN " + alias + ".index_state = 'INDEXED' AND " + alias + ".lifecycle_state = 'ACTIVE' THEN 'READY' " +
		"WHEN " + alias + ".index_state = 'INDEXED' THEN 'INDEXED' " +
		"ELSE 'DISCOVERED' END"
}
