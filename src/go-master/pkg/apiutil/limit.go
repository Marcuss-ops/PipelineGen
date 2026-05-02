package apiutil

func ClampLimit(value, def, max int) int {
	if value <= 0 {
		return def
	}
	if value > max {
		return max
	}
	return value
}
