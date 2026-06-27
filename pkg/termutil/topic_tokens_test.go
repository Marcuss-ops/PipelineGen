package termutil

import (
	"reflect"
	"testing"
)

func TestTopicTokens(t *testing.T) {
	got := TopicTokens("Hello, World!")
	want := []string{"hello", "world"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("TopicTokens() = %v, want %v", got, want)
	}
}
