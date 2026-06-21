package portutil

import (
	"io"
	"testing"
)

type testWriter struct{}

func (w *testWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestIsNilPort_BareNil(t *testing.T) {
	if !IsNilPort(nil) {
		t.Error("IsNilPort(nil) should be true")
	}
}

func TestIsNilPort_TypedNilPointer(t *testing.T) {
	var w *testWriter = nil
	var i io.Writer = w
	if !IsNilPort(i) {
		t.Error("IsNilPort(typed-nil *testWriter as io.Writer) should be true")
	}
}

func TestIsNilPort_NonNilPointer(t *testing.T) {
	var w io.Writer = &testWriter{}
	if IsNilPort(w) {
		t.Error("IsNilPort(non-nil *testWriter) should be false")
	}
}

func TestIsNilPort_TypedNilSlice(t *testing.T) {
	var s []int = nil
	var i any = s
	if !IsNilPort(i) {
		t.Error("IsNilPort(typed-nil []int as any) should be true")
	}
}

func TestIsNilPort_NonNilSlice(t *testing.T) {
	s := []int{1, 2, 3}
	var i any = s
	if IsNilPort(i) {
		t.Error("IsNilPort(non-nil []int) should be false")
	}
}

func TestIsNilPort_Struct(t *testing.T) {
	var i any = testWriter{}
	if IsNilPort(i) {
		t.Error("IsNilPort(struct) should be false — structs are never nil")
	}
}

func TestIsNilPort_String(t *testing.T) {
	var i any = "hello"
	if IsNilPort(i) {
		t.Error("IsNilPort(string) should be false")
	}
}

func TestIsNilPort_EmptyInterface(t *testing.T) {
	var i any
	if !IsNilPort(i) {
		t.Error("IsNilPort(empty interface) should be true")
	}
}
