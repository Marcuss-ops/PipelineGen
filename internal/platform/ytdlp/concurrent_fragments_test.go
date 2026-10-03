package ytdlp

import (
	"reflect"
	"testing"
)

func TestConcurrentFragmentsArgDefault(t *testing.T) {
	t.Setenv(EnvConcurrentFragments, "")
	b := &CommandBuilder{}
	got := b.ConcurrentFragmentsArg()
	if want := []string{"--concurrent-fragments", "4"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ConcurrentFragmentsArg() = %v, want %v", got, want)
	}
}

func TestConcurrentFragmentsArgEnvOverrideAndClamp(t *testing.T) {
	b := &CommandBuilder{}

	t.Setenv(EnvConcurrentFragments, "8")
	if got := b.ConcurrentFragmentsArg(); !reflect.DeepEqual(got, []string{"--concurrent-fragments", "8"}) {
		t.Fatalf("override 8: got %v", got)
	}

	t.Setenv(EnvConcurrentFragments, "99")
	if got := b.ConcurrentFragmentsArg(); !reflect.DeepEqual(got, []string{"--concurrent-fragments", "16"}) {
		t.Fatalf("override 99: got %v, want clamp to 16", got)
	}

	t.Setenv(EnvConcurrentFragments, "0")
	if got := b.ConcurrentFragmentsArg(); !reflect.DeepEqual(got, []string{"--concurrent-fragments", "1"}) {
		t.Fatalf("override 0: got %v, want clamp to 1", got)
	}

	t.Setenv(EnvConcurrentFragments, "garbage")
	if got := b.ConcurrentFragmentsArg(); !reflect.DeepEqual(got, []string{"--concurrent-fragments", "4"}) {
		t.Fatalf("override garbage: got %v, want default 4", got)
	}
}
