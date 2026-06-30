package providers

// containsString is the canonical "is name in list?" guard. Used
// by Aggregate (both branches) for the Sources filter. Cheap O(N)
// check under opts.Sources cardinality (typically <10).
func containsString(list []string, name string) bool {
	for _, v := range list {
		if v == name {
			return true
		}
	}
	return false
}
