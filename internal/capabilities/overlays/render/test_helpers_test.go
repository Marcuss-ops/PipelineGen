package render

// containsString is a test helper duplicated from the core overlay test
// harness (phrase_motion_pool_test.go): render tests cannot reach the
// unexported helper of another package.
func containsString(haystack []string, needle string) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}
