package clips

// buildMediaAssetQuery is kept as a package-level compatibility helper for
// older call sites that were written before the method receiver was added.
func buildMediaAssetQuery(source string) string {
	query := "SELECT " + mediaAssetColumns + " FROM media_assets WHERE 1=1"
	if source != "" && source != "all" && source != "unified" {
		query += " AND source = ?"
	}
	return query
}
