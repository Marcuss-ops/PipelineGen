package filesystem

import (
	"testing"
)

func TestRandomString(t *testing.T) {
	s1 := RandomString(16)
	s2 := RandomString(16)
	if len(s1) != 16 {
		t.Errorf("expected length 16, got %d", len(s1))
	}
	if s1 == s2 {
		t.Error("expected different random strings")
	}
}

func TestRandomString_Short(t *testing.T) {
	s := RandomString(1)
	if len(s) != 1 {
		t.Errorf("expected length 1, got %d", len(s))
	}
}

func TestRandomString_Fallback(t *testing.T) {
	for n := 1; n <= 32; n++ {
		s := RandomString(n)
		if len(s) != n {
			t.Errorf("RandomString(%d) returned length %d", n, len(s))
		}
	}
}
