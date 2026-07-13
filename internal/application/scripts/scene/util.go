package scene

func itoaLen(v int) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	n := v
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
